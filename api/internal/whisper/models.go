package whisper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// ModelBaseURL serves ggml-<name>.bin files for whisper.cpp.
var ModelBaseURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main"

var modelNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{0,40}$`)

var dlMu sync.Mutex // one download at a time per process

// BundledDir holds models baked into the Docker image (read-only). It is
// searched after the writable models dir.
var BundledDir = ""

// ModelPath returns where a model lives inside dir.
func ModelPath(dir, name string) string { return filepath.Join(dir, "ggml-"+name+".bin") }

// FindModel returns the path of a complete model in dir or BundledDir.
func FindModel(dir, name string) (string, bool) {
	for _, d := range []string{dir, BundledDir} {
		if d == "" {
			continue
		}
		p := ModelPath(d, name)
		if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
			return p, true
		}
	}
	return "", false
}

// HasModel reports whether the model is available (downloaded or bundled).
func HasModel(dir, name string) bool {
	_, ok := FindModel(dir, name)
	return ok
}

// EnsureModel downloads ggml-<name>.bin into dir if it is missing.
// Progress is reported as 0..1 when the size is known.
func EnsureModel(ctx context.Context, dir, name string, progress func(float64)) error {
	if !modelNameRe.MatchString(name) {
		return fmt.Errorf("%w: invalid whisper model name %q", ErrConfig, name)
	}
	if HasModel(dir, name) {
		return nil
	}
	dlMu.Lock()
	defer dlMu.Unlock()
	if HasModel(dir, name) { // another job finished it while we waited
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return writeErr(dir, err)
	}
	dst := ModelPath(dir, name)
	part := dst + ".part"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelBaseURL+"/ggml-"+name+".bin", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Hour}).Do(req)
	if err != nil {
		return fmt.Errorf("download whisper model %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: whisper model %q does not exist (try tiny, base, small, medium, large-v3-turbo, or add .en)", ErrConfig, name)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download whisper model %s: http %d", name, resp.StatusCode)
	}

	f, err := os.Create(part)
	if err != nil {
		return writeErr(dir, err)
	}
	pw := &progressWriter{total: resp.ContentLength, fn: progress}
	_, err = io.Copy(io.MultiWriter(f, pw), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("download whisper model %s: %w", name, err)
	}
	if resp.ContentLength > 0 && pw.n != resp.ContentLength {
		_ = os.Remove(part)
		return fmt.Errorf("download whisper model %s: incomplete (%d of %d bytes)", name, pw.n, resp.ContentLength)
	}
	if err := os.Rename(part, dst); err != nil {
		return writeErr(dir, err)
	}
	if progress != nil {
		progress(1)
	}
	return nil
}

func writeErr(dir string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w: cannot write whisper models to %s (if it is a host folder, run: sudo chown -R 10001:10001 %s): %v",
			ErrConfig, dir, dir, err)
	}
	return err
}

type progressWriter struct {
	n, total int64
	last     time.Time
	fn       func(float64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if p.fn != nil && p.total > 0 && time.Since(p.last) > 500*time.Millisecond {
		p.last = time.Now()
		p.fn(float64(p.n) / float64(p.total))
	}
	return len(b), nil
}
