// Package ytdlp wraps the yt-dlp CLI. Arguments are always passed as argv, never via a shell.
package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	Bin         string
	CookiesFile string
	Proxy       string
	POTURL      string // bgutil PO-token provider HTTP server
	UpdateCmd   string
	ExtraArgs   []string // operator-supplied flags, e.g. --js-runtimes node
}

// ---------- URL validation ----------

var ErrUnsupportedURL = errors.New("only YouTube and Twitch URLs are supported")

// ValidateURL normalises and checks the URL against the platform allowlist.
func ValidateURL(raw string) (normalized, platform string, err error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", ErrUnsupportedURL
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "youtube.com", "youtu.be", "music.youtube.com", "youtube-nocookie.com":
		platform = "youtube"
	case "twitch.tv", "clips.twitch.tv":
		platform = "twitch"
	default:
		return "", "", ErrUnsupportedURL
	}
	u.Scheme = "https"
	u.Fragment = ""
	// Drop playlist params so a single video is processed.
	if platform == "youtube" {
		q := u.Query()
		q.Del("list")
		q.Del("index")
		q.Del("start_radio")
		u.RawQuery = q.Encode()
	}
	return u.String(), platform, nil
}

// ---------- Metadata ----------

type Chapter struct {
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Title     string  `json:"title"`
}

type Format struct {
	FormatID    string            `json:"format_id"`
	URL         string            `json:"url"`
	Ext         string            `json:"ext"`
	Protocol    string            `json:"protocol"`
	VCodec      string            `json:"vcodec"`
	ACodec      string            `json:"acodec"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	HTTPHeaders map[string]string `json:"http_headers"`
}

type Info struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Uploader         string            `json:"uploader"`
	Channel          string            `json:"channel"`
	Duration         float64           `json:"duration"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	Thumbnail        string            `json:"thumbnail"`
	IsLive           bool              `json:"is_live"`
	LiveStatus       string            `json:"live_status"`
	Chapters         []Chapter         `json:"chapters"`
	Description      string            `json:"description"`
	WebpageURL       string            `json:"webpage_url"`
	Extractor        string            `json:"extractor_key"`
	URL              string            `json:"url"`
	Ext              string            `json:"ext"`
	HTTPHeaders      map[string]string `json:"http_headers"`
	RequestedFormats []Format          `json:"requested_formats"`
}

func (c *Client) baseArgs() []string {
	// --socket-timeout stops a stalled connection from hanging a job forever.
	args := []string{"--no-playlist", "--no-warnings", "--no-progress", "--ignore-config", "--socket-timeout", "30"}
	if c.CookiesFile != "" {
		if _, err := os.Stat(c.CookiesFile); err == nil {
			args = append(args, "--cookies", c.CookiesFile)
		}
	}
	if c.Proxy != "" {
		args = append(args, "--proxy", c.Proxy)
	}
	if c.POTURL != "" {
		args = append(args, "--extractor-args", "youtubepot-bgutilhttp:base_url="+c.POTURL)
	}
	return append(args, c.ExtraArgs...)
}

// Probe fetches metadata without downloading. If format is non-empty the
// selected format(s) are resolved too (requested_formats / url).
// ProbeTimeout bounds metadata extraction (JS challenge + PO token included).
var ProbeTimeout = 3 * time.Minute

func (c *Client) Probe(ctx context.Context, rawURL, format string) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	args := append(c.baseArgs(), "-J")
	if format != "" {
		args = append(args, "-f", format)
	}
	args = append(args, "--", rawURL)
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("yt-dlp timed out after %s reading video info (YouTube may be challenging this server; try cookies or a proxy): %s",
				ProbeTimeout, tail(stderr.String(), 600))
		}
		return nil, fmt.Errorf("yt-dlp probe: %w: %s", err, tail(stderr.String(), 800))
	}
	var info Info
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return nil, fmt.Errorf("yt-dlp probe json: %w", err)
	}
	if info.IsLive || info.LiveStatus == "is_live" || info.LiveStatus == "is_upcoming" {
		return nil, errors.New("live streams are not supported; wait until the VOD is available")
	}
	return &info, nil
}

// ---------- Downloads ----------

// Progress is reported as a fraction 0..1.
type ProgressFunc func(fraction float64)

// ProxyFormat selects a small H.264/AAC rendition for the browser editor.
const ProxyFormat = "bv*[height<=480]+ba/b[height<=480]/wv*+ba/w"

// DownloadProxy downloads a low resolution editor copy to dir/proxy.mp4.
func (c *Client) DownloadProxy(ctx context.Context, rawURL, dir string, height int, onProgress ProgressFunc) (string, error) {
	out := filepath.Join(dir, "proxy.%(ext)s")
	args := append(c.baseArgs(),
		"-f", ProxyFormat,
		"-S", fmt.Sprintf("res:%d,vcodec:h264,acodec:aac,ext:mp4:m4a", height),
		"--merge-output-format", "mp4",
		"--remux-video", "mp4",
		"--concurrent-fragments", "4",
		"-o", out,
		"--newline",
		"--progress", "--progress-template", "download:CFPROG %(progress)j",
		"--", rawURL,
	)
	// --progress overrides --no-progress from baseArgs.
	if err := c.run(ctx, args, onProgress); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "proxy.mp4")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("proxy file missing after download: %w", err)
	}
	return p, nil
}

// SourceFormat picks the best rendition up to 1080p for final renders.
const SourceFormat = "bv*[height<=1080]+ba/b[height<=1080]/bv*+ba/b"

// StreamInput is a direct media URL ffmpeg can read, with request headers.
type StreamInput struct {
	URL     string
	Headers map[string]string
}

// ResolveStreams returns direct URLs (video+audio, or a single muxed URL)
// for SourceFormat, letting ffmpeg seek precisely without downloading the VOD.
func (c *Client) ResolveStreams(ctx context.Context, rawURL string) ([]StreamInput, *Info, error) {
	info, err := c.Probe(ctx, rawURL, SourceFormat)
	if err != nil {
		return nil, nil, err
	}
	var ins []StreamInput
	if len(info.RequestedFormats) > 0 {
		for _, f := range info.RequestedFormats {
			if f.URL == "" {
				return nil, info, errors.New("format has no direct url")
			}
			ins = append(ins, StreamInput{URL: f.URL, Headers: f.HTTPHeaders})
		}
	} else if info.URL != "" {
		ins = append(ins, StreamInput{URL: info.URL, Headers: info.HTTPHeaders})
	}
	if len(ins) == 0 {
		return nil, info, errors.New("no playable stream url resolved")
	}
	return ins, info, nil
}

// DownloadSection downloads only [startSec,endSec] at source quality. Used as a
// fallback when direct streaming fails. Cuts are keyframe-forced so the output
// starts exactly at startSec.
func (c *Client) DownloadSection(ctx context.Context, rawURL, dir string, startSec, endSec float64, onProgress ProgressFunc) (string, error) {
	out := filepath.Join(dir, "section.%(ext)s")
	args := append(c.baseArgs(),
		"-f", SourceFormat,
		"--download-sections", fmt.Sprintf("*%.3f-%.3f", startSec, endSec),
		"--force-keyframes-at-cuts",
		"--merge-output-format", "mkv",
		"-o", out,
		"--newline",
		"--progress", "--progress-template", "download:CFPROG %(progress)j",
		"--", rawURL,
	)
	if err := c.run(ctx, args, onProgress); err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "section.*"))
	for _, m := range matches {
		if !strings.HasSuffix(m, ".part") && !strings.HasSuffix(m, ".ytdl") {
			return m, nil
		}
	}
	return "", errors.New("section file missing after download")
}

// Update runs the configured self-update command (yt-dlp extractors break often).
func (c *Client) Update(ctx context.Context) (string, error) {
	parts := strings.Fields(c.UpdateCmd)
	if len(parts) == 0 {
		return "", nil
	}
	out, err := exec.CommandContext(ctx, parts[0], parts[1:]...).CombinedOutput()
	return string(out), err
}

func (c *Client) Version(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, c.Bin, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func (c *Client) run(ctx context.Context, args []string, onProgress ProgressFunc) error {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	tracker := &progressTracker{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if f, ok := tracker.parse(sc.Text()); ok && onProgress != nil {
			onProgress(f)
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("yt-dlp: %w: %s", err, tail(stderr.String(), 1200))
	}
	return nil
}

// ---------- Progress parsing ----------

type progressLine struct {
	Status             string   `json:"status"`
	DownloadedBytes    *float64 `json:"downloaded_bytes"`
	TotalBytes         *float64 `json:"total_bytes"`
	TotalBytesEstimate *float64 `json:"total_bytes_estimate"`
	FragmentIndex      *float64 `json:"fragment_index"`
	FragmentCount      *float64 `json:"fragment_count"`
	Filename           string   `json:"filename"`
}

// progressTracker combines per-file progress across the video and audio
// downloads of a merged format into one 0..1 value.
type progressTracker struct {
	files []string
	last  float64
}

func (t *progressTracker) parse(line string) (float64, bool) {
	idx := strings.Index(line, "CFPROG ")
	if idx < 0 {
		return 0, false
	}
	return t.parseJSON(line[idx+len("CFPROG "):])
}

func (t *progressTracker) parseJSON(s string) (float64, bool) {
	var p progressLine
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return 0, false
	}
	frac, ok := fileFraction(p)
	if !ok {
		return 0, false
	}
	// Track which file in the sequence this is (video first, then audio).
	pos := -1
	for i, f := range t.files {
		if f == p.Filename {
			pos = i
		}
	}
	if pos < 0 {
		t.files = append(t.files, p.Filename)
		pos = len(t.files) - 1
	}
	n := float64(max(len(t.files), 2))
	// Assume two parts when merging; clamp so progress never moves backwards.
	total := (float64(pos) + frac) / n
	if len(t.files) == 1 && pos == 0 {
		total = frac * 0.85
	}
	if total < t.last {
		total = t.last
	}
	if total > 1 {
		total = 1
	}
	t.last = total
	return total, true
}

func fileFraction(p progressLine) (float64, bool) {
	if p.Status == "finished" {
		return 1, true
	}
	if p.DownloadedBytes != nil {
		total := p.TotalBytes
		if total == nil || *total == 0 {
			total = p.TotalBytesEstimate
		}
		if total != nil && *total > 0 {
			return clamp(*p.DownloadedBytes / *total), true
		}
	}
	if p.FragmentIndex != nil && p.FragmentCount != nil && *p.FragmentCount > 0 {
		return clamp(*p.FragmentIndex / *p.FragmentCount), true
	}
	return 0, false
}

func clamp(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
