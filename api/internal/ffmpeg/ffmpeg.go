// Package ffmpeg builds and runs ffmpeg/ffprobe commands.
package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Runner struct {
	FFmpeg  string
	FFprobe string
	Threads int
	// Gate, if set, is held during heavy encodes (render, preview transcode).
	Gate func(ctx context.Context) (release func(), err error)
}

// Input is a media source: a local path or a remote URL with headers.
type Input struct {
	Path    string
	Headers map[string]string
}

func (in Input) remote() bool {
	return strings.HasPrefix(in.Path, "http://") || strings.HasPrefix(in.Path, "https://")
}

type RenderOptions struct {
	Inputs   []Input // 1 muxed input, or [video, audio]
	StartSec float64 // seek offset applied to every input
	DurSec   float64
	Aspect   string  // 9:16 | 16:9 | 1:1
	CropX    float64 // 0..1 horizontal focus for cover crops
	ASSPath  string  // optional burned-in captions
	FontsDir string
	Output   string
	Loudnorm bool
	CRF      int
	Preset   string
}

// OutputSize returns the canvas for an aspect ratio.
func OutputSize(aspect string) (int, int) {
	switch aspect {
	case "16:9":
		return 1920, 1080
	case "1:1":
		return 1080, 1080
	default:
		return 1080, 1920
	}
}

// filterEscape escapes a value for use inside an ffmpeg filtergraph option.
func filterEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\\\`, `'`, `\\\'`, `:`, `\\:`, `,`, `\,`, `[`, `\[`, `]`, `\]`, `;`, `\;`)
	return r.Replace(s)
}

// VideoFilter builds the -vf chain: cover-scale, crop with focus, captions.
func VideoFilter(o RenderOptions) string {
	w, h := OutputSize(o.Aspect)
	cx := o.CropX
	if cx < 0 {
		cx = 0
	}
	if cx > 1 {
		cx = 1
	}
	parts := []string{
		fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=increase:flags=lanczos", w, h),
		fmt.Sprintf("crop=w=%d:h=%d:x=(iw-ow)*%.4f:y=(ih-oh)/2", w, h, cx),
		"setsar=1",
	}
	if o.ASSPath != "" {
		f := "subtitles=filename=" + filterEscape(o.ASSPath)
		if o.FontsDir != "" {
			f += ":fontsdir=" + filterEscape(o.FontsDir)
		}
		parts = append(parts, f)
	}
	parts = append(parts, "format=yuv420p")
	return strings.Join(parts, ",")
}

// RenderArgs returns the full ffmpeg argv (without the binary).
func (r *Runner) RenderArgs(o RenderOptions) []string {
	if o.CRF == 0 {
		o.CRF = 20
	}
	if o.Preset == "" {
		o.Preset = "veryfast"
	}
	args := []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-progress", "pipe:1", "-nostats"}
	for _, in := range o.Inputs {
		if in.remote() {
			// Never let a remote playlist pull local files or odd protocols.
			args = append(args, "-protocol_whitelist", "http,https,tcp,tls,crypto")
			if !strings.Contains(in.Path, ".m3u8") {
				args = append(args, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5")
			}
			if len(in.Headers) > 0 {
				args = append(args, "-headers", headerBlock(in.Headers))
			}
		}
		if o.StartSec > 0 {
			args = append(args, "-ss", ff(o.StartSec))
		}
		args = append(args, "-t", ff(o.DurSec), "-i", in.Path)
	}
	args = append(args, "-map", "0:v:0")
	if len(o.Inputs) > 1 {
		args = append(args, "-map", "1:a:0")
	} else {
		args = append(args, "-map", "0:a:0?")
	}
	args = append(args, "-vf", VideoFilter(o))
	if o.Loudnorm {
		args = append(args, "-af", "loudnorm=I=-14:TP=-1.5:LRA=11")
	}
	args = append(args,
		"-c:v", "libx264", "-preset", o.Preset, "-crf", strconv.Itoa(o.CRF),
		"-profile:v", "high", "-r", "30",
		"-c:a", "aac", "-b:a", "160k", "-ar", "48000", "-ac", "2",
		"-movflags", "+faststart", "-shortest",
	)
	if r.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(r.Threads))
	}
	return append(args, o.Output)
}

// Render runs the encode, reporting progress as a 0..1 fraction.
func (r *Runner) Render(ctx context.Context, o RenderOptions, onProgress func(float64)) error {
	return r.runProgress(ctx, r.RenderArgs(o), o.DurSec, onProgress)
}

// ExtractAudio writes 16 kHz mono PCM WAV (whisper's input format).
func (r *Runner) ExtractAudio(ctx context.Context, in, out string) error {
	return r.run(ctx, "-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-i", in,
		"-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", out)
}

// ExtractAudioMP3 writes a compact mono mp3 for upload to transcription APIs.
func (r *Runner) ExtractAudioMP3(ctx context.Context, in, out string, startSec, durSec float64) error {
	args := []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error"}
	if startSec > 0 {
		args = append(args, "-ss", ff(startSec))
	}
	if durSec > 0 {
		args = append(args, "-t", ff(durSec))
	}
	args = append(args, "-i", in, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", "48k", out)
	return r.run(ctx, args...)
}

// SplitWAV splits a WAV into chunkSec segments; returns paths in order.
func (r *Runner) SplitWAV(ctx context.Context, in, dir string, chunkSec int) ([]string, error) {
	pattern := filepath.Join(dir, "chunk_%04d.wav")
	if err := r.run(ctx, "-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-i", in,
		"-f", "segment", "-segment_time", strconv.Itoa(chunkSec), "-c", "copy", "-reset_timestamps", "1", pattern); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "chunk_*.wav"))
	sort.Strings(files)
	return files, err
}

// Thumbnail grabs a frame at atSec.
func (r *Runner) Thumbnail(ctx context.Context, in, out string, atSec float64) error {
	return r.run(ctx, "-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-ss", ff(atSec), "-i", in,
		"-frames:v", "1", "-vf", "scale=540:-2", "-q:v", "3", out)
}

type ProbeResult struct {
	DurationSec float64
	Width       int
	Height      int
	HasAudio    bool
	VideoCodec  string
	AudioCodec  string
	FormatName  string // e.g. "mov,mp4,m4a,3gp,3g2,mj2" or "matroska,webm"
}

func (r *Runner) Probe(ctx context.Context, path string) (*ProbeResult, error) {
	out, err := exec.CommandContext(ctx, r.FFprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var pr struct {
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, err
	}
	res := &ProbeResult{}
	res.DurationSec, _ = strconv.ParseFloat(pr.Format.Duration, 64)
	res.FormatName = pr.Format.FormatName
	for _, s := range pr.Streams {
		switch s.CodecType {
		case "video":
			if res.Width == 0 {
				res.Width, res.Height, res.VideoCodec = s.Width, s.Height, s.CodecName
			}
		case "audio":
			if !res.HasAudio {
				res.AudioCodec = s.CodecName
			}
			res.HasAudio = true
		}
	}
	return res, nil
}

// TranscodeProxy re-encodes a proxy to browser-safe H.264/AAC (used when the
// downloaded rendition is not playable in all browsers).
func (r *Runner) TranscodeProxy(ctx context.Context, in, out string, height int, durSec float64, onProgress func(float64)) error {
	args := []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-progress", "pipe:1", "-nostats",
		"-i", in, "-vf", fmt.Sprintf("scale=-2:%d", height), "-c:v", "libx264", "-preset", "veryfast", "-crf", "28",
		"-c:a", "aac", "-b:a", "96k", "-movflags", "+faststart"}
	if r.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(r.Threads))
	}
	args = append(args, out)
	return r.runProgress(ctx, args, durSec, onProgress)
}

func (r *Runner) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, r.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, tail(stderr.String(), 1200))
	}
	return nil
}

func (r *Runner) runProgress(ctx context.Context, args []string, durSec float64, onProgress func(float64)) error {
	if r.Gate != nil {
		release, err := r.Gate(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	cmd := exec.CommandContext(ctx, r.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if f, ok := ParseProgress(sc.Text(), durSec); ok && onProgress != nil {
			onProgress(f)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, tail(stderr.String(), 1500))
	}
	return nil
}

// ParseProgress handles a `-progress` key=value line.
func ParseProgress(line string, durSec float64) (float64, bool) {
	if durSec <= 0 {
		return 0, false
	}
	v, ok := strings.CutPrefix(line, "out_time_us=")
	if !ok {
		v, ok = strings.CutPrefix(line, "out_time_ms=") // also microseconds, despite the name
	}
	if !ok {
		return 0, false
	}
	us, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || us < 0 {
		return 0, false
	}
	f := us / 1e6 / durSec
	if f > 1 {
		f = 1
	}
	return f, true
}

func headerBlock(h map[string]string) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.NewReplacer("\r", "", "\n", "").Replace(h[k])
		b.WriteString(k + ": " + v + "\r\n")
	}
	return b.String()
}

func ff(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
