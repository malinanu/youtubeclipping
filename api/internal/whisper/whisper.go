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
	"strconv"
	"strings"
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
	FF        *ffmpeg.Runner
	TmpDir    string
}

func (l *Local) Name() string { return "whisper.cpp:" + l.Model }

// ErrConfig marks misconfiguration that retries cannot fix.
var ErrConfig = errors.New("transcription misconfigured")

func (l *Local) modelPath() (string, error) {
	p := filepath.Join(l.ModelsDir, "ggml-"+l.Model+".bin")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%w: whisper model %q not found at %s (run deploy/download-models.sh)", ErrConfig, l.Model, p)
	}
	return p, nil
}

var progressRe = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

func (l *Local) Transcribe(ctx context.Context, wavPath string, durSec float64, onProgress func(float64)) (*Result, error) {
	model, err := l.modelPath()
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(l.TmpDir, "whisper-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	chunkSec := l.ChunkSec
	if chunkSec <= 0 {
		chunkSec = 600
	}
	chunks := []string{wavPath}
	if durSec > float64(chunkSec)*1.2 {
		if chunks, err = l.FF.SplitWAV(ctx, wavPath, work, chunkSec); err != nil {
			return nil, err
		}
	}

	res := &Result{Provider: l.Name()}
	for i, chunk := range chunks {
		offset := int64(i * chunkSec * 1000)
		base := filepath.Join(work, fmt.Sprintf("out_%04d", i))
		lang := l.Language
		if lang == "" {
			lang = "auto"
		}
		if i > 0 && res.Language != "" && lang == "auto" {
			lang = res.Language // keep detection stable across chunks
		}
		args := []string{"-m", model, "-f", chunk, "-l", lang, "-t", strconv.Itoa(max(l.Threads, 1)),
			"-ml", "1", "-sow", "-oj", "-of", base, "-pp"}
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
				onProgress((float64(i) + float64(p)/100) / float64(len(chunks)))
			} else if errBuf.Len() < 64*1024 {
				errBuf.WriteString(line + "\n")
			}
		}
		if err := cmd.Wait(); err != nil {
			return nil, fmt.Errorf("whisper.cpp: %w: %s", err, tailStr(errBuf.String(), 1000))
		}
		raw, err := os.ReadFile(base + ".json")
		if err != nil {
			return nil, err
		}
		lang2, words, err := ParseCppJSON(raw, offset)
		if err != nil {
			return nil, err
		}
		if res.Language == "" {
			res.Language = lang2
		}
		res.Words = append(res.Words, words...)
	}
	return res, nil
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
