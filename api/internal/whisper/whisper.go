// Package whisper transcribes audio to word-timed text, locally via
// whisper.cpp or through an OpenAI-compatible transcription API.
package whisper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/malinanu/youtubeclipping/api/internal/ffmpeg"
	"github.com/malinanu/youtubeclipping/api/internal/media"
)

type Result struct {
	Language string
	Words    []media.Word
	Provider string
}

type Provider interface {
	Name() string
	Transcribe(ctx context.Context, wavPath string, durSec float64, onProgress func(float64)) (*Result, error)
}

// ---------- whisper.cpp ----------

type Local struct {
	Bin       string
	ModelsDir string
	Model     string // small, medium, base.en, large-v3-turbo ...
	Language  string // "auto" or ISO code
	Threads   int
	ChunkSec  int
	BeamSize  int // 1 = greedy (fast, low memory); whisper.cpp CLI default is 5
	BestOf    int
	// MaxContext is passed as -mc (0 avoids repetition loops); <0 keeps whisper's default.
	MaxContext int
	FF         *ffmpeg.Runner
	TmpDir     string
	// WorkDir, if set, persists chunk WAVs and per-chunk results so a killed or
	// restarted transcription resumes instead of starting over.
	WorkDir string
	// Gate, if set, is held while whisper.cpp runs (limits heavy jobs at once).
	Gate func(ctx context.Context) (release func(), err error)
}

func (l *Local) Name() string { return "whisper.cpp:" + l.Model }

// ErrConfig marks misconfiguration that retries cannot fix.
var ErrConfig = errors.New("transcription misconfigured")

// ErrKilled means the OS killed whisper.cpp (almost always out of memory).
var ErrKilled = errors.New("whisper.cpp was killed by the system — almost always out of memory. " +
	"Free RAM (stop other heavy apps), add swap, pick a smaller model (base or small.en), " +
	"or lower WHISPER_THREADS. Progress is saved; Retry resumes where it stopped")

func (l *Local) modelPath() (string, error) {
	if p, ok := FindModel(l.ModelsDir, l.Model); ok {
		return p, nil
	}
	return "", fmt.Errorf("%w: whisper model %q not found in %s", ErrConfig, l.Model, l.ModelsDir)
}

var progressRe = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

// workMarker identifies what a persisted work dir was produced with.
type workMarker struct {
	Model    string `json:"model"`
	Language string `json:"language"`
	ChunkSec int    `json:"chunkSec"`
	Beam     int    `json:"beam"`
	MaxCtx   int    `json:"maxCtx"`
}

// prepareWorkDir returns the directory for chunk files, resetting it when it
// was produced with different settings.
func (l *Local) prepareWorkDir(chunkSec int) (dir string, cleanup func(), err error) {
	if l.WorkDir == "" {
		d, err := os.MkdirTemp(l.TmpDir, "whisper-")
		return d, func() { _ = os.RemoveAll(d) }, err
	}
	want := workMarker{Model: l.Model, Language: l.Language, ChunkSec: chunkSec, Beam: l.beam(), MaxCtx: l.MaxContext}
	markerPath := filepath.Join(l.WorkDir, "marker.json")
	var have workMarker
	if b, err := os.ReadFile(markerPath); err != nil || json.Unmarshal(b, &have) != nil || have != want {
		_ = os.RemoveAll(l.WorkDir)
	}
	if err := os.MkdirAll(l.WorkDir, 0o755); err != nil {
		return "", nil, err
	}
	b, _ := json.Marshal(want)
	if err := os.WriteFile(markerPath, b, 0o644); err != nil {
		return "", nil, err
	}
	return l.WorkDir, func() {}, nil // caller removes WorkDir after success
}

func (l *Local) beam() int {
	if l.BeamSize <= 0 {
		return 1
	}
	return l.BeamSize
}

func (l *Local) bestOf() int {
	if l.BestOf <= 0 {
		return 1
	}
	return l.BestOf
}

func (l *Local) Transcribe(ctx context.Context, wavPath string, durSec float64, onProgress func(float64)) (*Result, error) {
	model, err := l.modelPath()
	if err != nil {
		return nil, err
	}
	chunkSec := l.ChunkSec
	if chunkSec <= 0 {
		chunkSec = 600
	}
	work, cleanup, err := l.prepareWorkDir(chunkSec)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	chunks := []string{wavPath}
	if durSec > float64(chunkSec)*1.2 {
		chunks, _ = filepath.Glob(filepath.Join(work, "chunk_*.wav"))
		if len(chunks) == 0 || !fileExists(filepath.Join(work, "split.done")) {
			if chunks, err = l.FF.SplitWAV(ctx, wavPath, work, chunkSec); err != nil {
				return nil, err
			}
			_ = os.WriteFile(filepath.Join(work, "split.done"), nil, 0o644)
		}
		sort.Strings(chunks)
	}

	res := &Result{Provider: l.Name()}
	for i, chunk := range chunks {
		offset := int64(i * chunkSec * 1000)
		base := filepath.Join(work, fmt.Sprintf("out_%04d", i))
		final := base + ".json"

		raw, err := os.ReadFile(final)
		if err != nil { // not done yet (or never started): run whisper for this chunk
			if raw, err = l.runChunk(ctx, model, chunk, base, i, len(chunks), res.Language, onProgress); err != nil {
				return nil, err
			}
		} else if onProgress != nil {
			onProgress(float64(i+1) / float64(len(chunks))) // resumed from a previous run
		}
		lang2, words, err := ParseCppJSON(raw, offset)
		if err != nil {
			_ = os.Remove(final) // corrupt: redo next time
			return nil, err
		}
		if res.Language == "" {
			res.Language = lang2
		}
		res.Words = append(res.Words, words...)
	}
	res.Words = media.CollapseRepeats(res.Words)
	return res, nil
}

// runChunk transcribes one chunk and atomically writes <base>.json.
func (l *Local) runChunk(ctx context.Context, model, chunk, base string, i, n int, detected string,
	onProgress func(float64)) ([]byte, error) {
	if l.Gate != nil {
		release, err := l.Gate(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	lang := l.Language
	if lang == "" {
		lang = "auto"
	}
	if lang == "auto" && detected != "" {
		lang = detected // keep detection stable across chunks
	}
	tmpBase := base + ".tmp"
	args := []string{"-m", model, "-f", chunk, "-l", lang, "-t", strconv.Itoa(max(l.Threads, 1)),
		"-bs", strconv.Itoa(l.beam()), "-bo", strconv.Itoa(l.bestOf()),
		"-ml", "1", "-sow", "-oj", "-of", tmpBase, "-pp"}
	if l.MaxContext >= 0 {
		args = append(args, "-mc", strconv.Itoa(l.MaxContext))
	}
	cmd := exec.CommandContext(ctx, l.Bin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	var errBuf bytes.Buffer
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start whisper: %w", err)
	}
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		if m := progressRe.FindStringSubmatch(line); m != nil && onProgress != nil {
			p, _ := strconv.Atoi(m[1])
			onProgress((float64(i) + float64(p)/100) / float64(n))
		} else if errBuf.Len() < 64*1024 {
			errBuf.WriteString(line + "\n")
		}
	}
	if err := cmd.Wait(); err != nil {
		_ = os.Remove(tmpBase + ".json")
		if ctx.Err() != nil {
			return nil, ctx.Err() // cancelled/restarted: retry resumes from this chunk
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL {
				return nil, fmt.Errorf("%w (chunk %d of %d)", ErrKilled, i+1, n)
			}
		}
		return nil, fmt.Errorf("whisper.cpp: %w: %s", err, tailStr(errBuf.String(), 1000))
	}
	raw, err := os.ReadFile(tmpBase + ".json")
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmpBase+".json", base+".json"); err != nil {
		return nil, err
	}
	return raw, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

type cppOut struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// ParseCppJSON parses whisper-cli -oj output produced with -ml 1 -sow
// (one segment per word). Segments without a leading space are joined to the
// previous word (whisper splits punctuation/subwords).
func ParseCppJSON(raw []byte, offsetMs int64) (string, []media.Word, error) {
	var o cppOut
	if err := json.Unmarshal(raw, &o); err != nil {
		return "", nil, fmt.Errorf("parse whisper json: %w", err)
	}
	var words []media.Word
	for _, seg := range o.Transcription {
		txt := seg.Text
		if strings.TrimSpace(txt) == "" || isNoise(txt) {
			continue
		}
		startsWord := strings.HasPrefix(txt, " ") || len(words) == 0
		w := media.Word{Start: seg.Offsets.From + offsetMs, End: seg.Offsets.To + offsetMs, Text: strings.TrimSpace(txt)}
		if !startsWord {
			prev := &words[len(words)-1]
			prev.Text += w.Text
			prev.End = max(prev.End, w.End)
			continue
		}
		words = append(words, w)
	}
	return o.Result.Language, words, nil
}

var noiseRe = regexp.MustCompile(`^\s*[\[(].*[\])]\s*$`)

// isNoise filters annotations like [MUSIC] or (laughs).
func isNoise(s string) bool { return noiseRe.MatchString(s) }

// ---------- OpenAI-compatible API (OpenAI, Groq) ----------

type API struct {
	ProviderName string
	BaseURL      string // https://api.openai.com/v1 or https://api.groq.com/openai/v1
	APIKey       string
	Model        string // whisper-1, whisper-large-v3-turbo
	Language     string
	FF           *ffmpeg.Runner
	TmpDir       string
	ChunkSec     int
	HTTP         *http.Client
}

func NewOpenAI(key string, ff *ffmpeg.Runner, tmp string) *API {
	return &API{ProviderName: "openai", BaseURL: "https://api.openai.com/v1", APIKey: key, Model: "whisper-1", FF: ff, TmpDir: tmp}
}

func NewGroq(key string, ff *ffmpeg.Runner, tmp string) *API {
	return &API{ProviderName: "groq", BaseURL: "https://api.groq.com/openai/v1", APIKey: key, Model: "whisper-large-v3-turbo", FF: ff, TmpDir: tmp}
}

func (a *API) Name() string { return a.ProviderName + ":" + a.Model }

func (a *API) Transcribe(ctx context.Context, wavPath string, durSec float64, onProgress func(float64)) (*Result, error) {
	if a.APIKey == "" {
		return nil, fmt.Errorf("%w: %s api key not configured", ErrConfig, a.ProviderName)
	}
	work, err := os.MkdirTemp(a.TmpDir, "stt-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	chunk := a.ChunkSec
	if chunk <= 0 {
		chunk = 1200 // 20 min @48kbps ≈ 7 MB, well under the 25 MB limit
	}
	n := int(durSec/float64(chunk)) + 1
	res := &Result{Provider: a.Name()}
	for i := 0; i < n; i++ {
		start := float64(i * chunk)
		if start >= durSec && i > 0 {
			break
		}
		mp3 := filepath.Join(work, fmt.Sprintf("c%03d.mp3", i))
		if err := a.FF.ExtractAudioMP3(ctx, wavPath, mp3, start, float64(chunk)); err != nil {
			return nil, err
		}
		lang, words, err := a.call(ctx, mp3)
		if err != nil {
			return nil, err
		}
		if res.Language == "" {
			res.Language = lang
		}
		off := int64(start * 1000)
		for _, w := range words {
			w.Start += off
			w.End += off
			res.Words = append(res.Words, w)
		}
		if onProgress != nil {
			onProgress(float64(i+1) / float64(n))
		}
	}
	return res, nil
}

func (a *API) call(ctx context.Context, file string) (string, []media.Word, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	f, err := os.Open(file)
	if err != nil {
		return "", nil, err
	}
	fw, _ := mw.CreateFormFile("file", filepath.Base(file))
	if _, err := io.Copy(fw, f); err != nil {
		f.Close()
		return "", nil, err
	}
	f.Close()
	_ = mw.WriteField("model", a.Model)
	_ = mw.WriteField("response_format", "verbose_json")
	_ = mw.WriteField("timestamp_granularities[]", "word")
	_ = mw.WriteField("timestamp_granularities[]", "segment")
	if a.Language != "" && a.Language != "auto" {
		_ = mw.WriteField("language", a.Language)
	}
	_ = mw.Close()

	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Minute}
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/audio/transcriptions", bytes.NewReader(body.Bytes()))
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
		} else {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return parseVerbose(data)
			}
			lastErr = fmt.Errorf("%s transcription http %d: %s", a.ProviderName, resp.StatusCode, tailStr(string(data), 400))
			if resp.StatusCode != 429 && resp.StatusCode < 500 {
				return "", nil, lastErr
			}
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 5 * time.Second):
		}
	}
	return "", nil, lastErr
}

func parseVerbose(data []byte) (string, []media.Word, error) {
	var v struct {
		Language string `json:"language"`
		Words    []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return "", nil, err
	}
	words := make([]media.Word, 0, len(v.Words))
	for _, w := range v.Words {
		t := strings.TrimSpace(w.Word)
		if t == "" {
			continue
		}
		words = append(words, media.Word{Start: int64(w.Start * 1000), End: int64(w.End * 1000), Text: t})
	}
	return v.Language, words, nil
}

// WithFallback tries primary, then fallback when primary fails.
type WithFallback struct {
	Primary, Fallback Provider
}

func (w *WithFallback) Name() string { return w.Primary.Name() }

func (w *WithFallback) Transcribe(ctx context.Context, wav string, dur float64, p func(float64)) (*Result, error) {
	r, err := w.Primary.Transcribe(ctx, wav, dur, p)
	if err == nil || w.Fallback == nil || ctx.Err() != nil {
		return r, err
	}
	r2, err2 := w.Fallback.Transcribe(ctx, wav, dur, p)
	if err2 != nil {
		return nil, fmt.Errorf("primary: %v; fallback: %w", err, err2)
	}
	return r2, nil
}

func tailStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
