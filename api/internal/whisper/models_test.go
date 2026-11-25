package whisper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEnsureModel(t *testing.T) {
	payload := strings.Repeat("x", 2<<20)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/ggml-tiny.en.bin" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()
	old := ModelBaseURL
	ModelBaseURL = srv.URL
	defer func() { ModelBaseURL = old }()

	dir := t.TempDir()
	var last float64
	if err := EnsureModel(context.Background(), dir, "tiny.en", func(f float64) { last = f }); err != nil {
		t.Fatal(err)
	}
	if !HasModel(dir, "tiny.en") || last <= 0 {
		t.Fatalf("model missing or no progress (%v)", last)
	}
	if _, err := os.Stat(ModelPath(dir, "tiny.en") + ".part"); err == nil {
		t.Fatal(".part left behind")
	}
	// second call is a no-op
	_ = EnsureModel(context.Background(), dir, "tiny.en", nil)
	if hits.Load() != 1 {
		t.Fatalf("downloaded %d times", hits.Load())
	}
	if err := EnsureModel(context.Background(), dir, "nope", nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("404 should be config error: %v", err)
	}
	if err := EnsureModel(context.Background(), dir, "../etc/passwd", nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("bad name accepted: %v", err)
	}
}

func TestBundledModel(t *testing.T) {
	writable, bundled := t.TempDir(), t.TempDir()
	if err := os.WriteFile(ModelPath(bundled, "small"), make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	old := BundledDir
	BundledDir = bundled
	defer func() { BundledDir = old }()
	p, ok := FindModel(writable, "small")
	if !ok || p != ModelPath(bundled, "small") {
		t.Fatalf("bundled model not found: %s %v", p, ok)
	}
	// EnsureModel must not download when the model is bundled.
	if err := EnsureModel(context.Background(), writable, "small", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ModelPath(writable, "small")); err == nil {
		t.Fatal("downloaded despite bundled copy")
	}
}
