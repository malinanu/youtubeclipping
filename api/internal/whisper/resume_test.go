package whisper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/malinanu/youtubeclipping/api/internal/ffmpeg"
)

// fakeWhisper emulates whisper-cli: it logs each call, writes <-of>.json with
// one word named after the chunk, and SIGKILLs itself on chunk 1 the first time.
const fakeWhisper = `#!/bin/sh
all="$*"; out=""; in=""
while [ $# -gt 0 ]; do case "$1" in -of) out="$2"; shift;; -f) in="$2"; shift;; esac; shift; done
echo "$all" >> "$LOG"
case "$in" in *chunk_0001*) if [ ! -f "$MARK" ]; then touch "$MARK"; kill -9 $$; fi;; esac
name=$(basename "$in" .wav)
printf '{"result":{"language":"en"},"transcription":[{"offsets":{"from":100,"to":400},"text":" %s"}]}' "$name" > "$out.json"
`

func setupFake(t *testing.T) (bin, log, wav string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	bin = filepath.Join(dir, "whisper-cli")
	if err := os.WriteFile(bin, []byte(fakeWhisper), 0o755); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(dir, "calls.log")
	t.Setenv("LOG", log)
	t.Setenv("MARK", filepath.Join(dir, "killed-once"))
	wav = filepath.Join(dir, "a.wav")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
		"sine=frequency=300:duration=25", "-ac", "1", "-ar", "16000", wav).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return
}

func newLocal(t *testing.T, bin, work string) *Local {
	models := t.TempDir()
	if err := os.WriteFile(ModelPath(models, "tiny"), make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Local{Bin: bin, ModelsDir: models, Model: "tiny", Language: "en", Threads: 1, ChunkSec: 10,
		FF: &ffmpeg.Runner{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}, TmpDir: t.TempDir(), WorkDir: work}
}

func TestTranscribeResumesAfterKill(t *testing.T) {
	bin, log, wav := setupFake(t)
	work := filepath.Join(t.TempDir(), "whisper")
	l := newLocal(t, bin, work)

	_, err := l.Transcribe(context.Background(), wav, 25, nil)
	if !errors.Is(err, ErrKilled) || !strings.Contains(err.Error(), "chunk 2 of 3") {
		t.Fatalf("want ErrKilled on chunk 2, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "out_0000.json")); err != nil {
		t.Fatal("chunk 0 result should be persisted")
	}
	before, _ := os.ReadFile(log)

	res, err := l.Transcribe(context.Background(), wav, 25, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(log)
	second := strings.TrimPrefix(string(after), string(before))
	if strings.Contains(second, "chunk_0000") || strings.Count(second, "\n") != 2 {
		t.Fatalf("resume should only run chunks 1 and 2, ran:\n%s", second)
	}
	if !strings.Contains(second, "-bs 1 -bo 1") {
		t.Fatalf("greedy decoding flags missing:\n%s", second)
	}
	if len(res.Words) != 3 || res.Words[1].Start != 10100 || res.Words[2].Start != 20100 || res.Words[2].Text != "chunk_0002" {
		t.Fatalf("bad merged words: %+v", res.Words)
	}
}

func TestSettingsChangeResetsWorkDir(t *testing.T) {
	bin, log, wav := setupFake(t)
	_ = os.WriteFile(os.Getenv("MARK"), nil, 0o644) // no kill
	work := filepath.Join(t.TempDir(), "whisper")
	l := newLocal(t, bin, work)
	if _, err := l.Transcribe(context.Background(), wav, 25, nil); err != nil {
		t.Fatal(err)
	}
	l.Model = "tiny.en"
	_ = os.WriteFile(ModelPath(l.ModelsDir, "tiny.en"), make([]byte, 2<<20), 0o644)
	before, _ := os.ReadFile(log)
	if _, err := l.Transcribe(context.Background(), wav, 25, nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(log)
	if n := strings.Count(strings.TrimPrefix(string(after), string(before)), "\n"); n != 3 {
		t.Fatalf("model change must redo all 3 chunks, ran %d", n)
	}
}

func TestCancelIsNotKilled(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "whisper-cli")
	_ = os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755)
	l := newLocal(t, bin, "")
	wav := filepath.Join(dir, "a.wav")
	_ = os.WriteFile(wav, []byte("x"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := l.Transcribe(ctx, wav, 5, nil)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrKilled) {
		t.Fatalf("want ctx error, got %v", err)
	}
}
