package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/malinanu/youtubeclipping/api/internal/captions"
	"github.com/malinanu/youtubeclipping/api/internal/media"
)

// TestRenderEndToEnd encodes a synthetic 16:9 source into a captioned 9:16 clip.
func TestRenderEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30:duration=12",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=12",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("gen: %v %s", err, out)
	}
	words := []media.Word{
		{Start: 5000, End: 5400, Text: "This"}, {Start: 5400, End: 5800, Text: "is"},
		{Start: 5800, End: 6500, Text: "ClipForge!"}, {Start: 7000, End: 7600, Text: "Captions"},
		{Start: 7600, End: 8200, Text: "work."},
	}
	clip := media.WordsInRange(words, 4000, 9000)
	ass := filepath.Join(dir, "c.ass")
	if err := os.WriteFile(ass, []byte(captions.BuildASS(clip, captions.DefaultStyle(), 1080, 1920)), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	out := filepath.Join(dir, "out.mp4")
	var last float64
	err := r.Render(context.Background(), RenderOptions{
		Inputs: []Input{{Path: src}}, StartSec: 4, DurSec: 5, Aspect: "9:16", CropX: 0.5,
		ASSPath: ass, Output: out, Loudnorm: true, Preset: "ultrafast",
	}, func(f float64) { last = f })
	if err != nil {
		t.Fatal(err)
	}
	pr, err := r.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Width != 1080 || pr.Height != 1920 || !pr.HasAudio || pr.VideoCodec != "h264" {
		t.Fatalf("unexpected output %+v", pr)
	}
	if pr.DurationSec < 4.8 || pr.DurationSec > 5.3 {
		t.Fatalf("duration %v", pr.DurationSec)
	}
	if last < 0.9 {
		t.Fatalf("progress not reported: %v", last)
	}
	if err := r.Thumbnail(context.Background(), out, filepath.Join(dir, "t.jpg"), 2); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("KEEP_RENDER") != "" {
		_ = exec.Command("cp", out, os.Getenv("KEEP_RENDER")).Run()
	}
}
