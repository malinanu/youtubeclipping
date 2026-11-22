package ffmpeg

import (
	"slices"
	"strings"
	"testing"
)

func TestRenderArgsTwoInputs(t *testing.T) {
	r := &Runner{Threads: 3}
	args := r.RenderArgs(RenderOptions{
		Inputs: []Input{
			{Path: "https://rr1.googlevideo.com/v", Headers: map[string]string{"User-Agent": "x"}},
			{Path: "https://rr1.googlevideo.com/a"},
		},
		StartSec: 61.5, DurSec: 30, Aspect: "9:16", CropX: 0.3,
		ASSPath: "/data/tmp/c1/captions.ass", FontsDir: "/fonts", Output: "/out.mp4", Loudnorm: true,
	})
	s := strings.Join(args, " ")
	for _, want := range []string{
		"-ss 61.500 -t 30.000 -i https://rr1.googlevideo.com/v",
		"-map 0:v:0 -map 1:a:0",
		"scale=w=1080:h=1920:force_original_aspect_ratio=increase",
		"x=(iw-ow)*0.3000",
		`subtitles=filename=/data/tmp/c1/captions.ass:fontsdir=/fonts`,
		"loudnorm",
		"-threads 3",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if args[len(args)-1] != "/out.mp4" {
		t.Error("output must be last")
	}
	if i := slices.Index(args, "-headers"); i < 0 || args[i+1] != "User-Agent: x\r\n" {
		t.Error("headers not set")
	}
}

func TestRenderArgsHLSAndAspect(t *testing.T) {
	r := &Runner{}
	args := r.RenderArgs(RenderOptions{
		Inputs: []Input{{Path: "https://usher.ttvnw.net/x.m3u8"}}, StartSec: 0, DurSec: 10, Aspect: "16:9", Output: "o.mp4",
	})
	s := strings.Join(args, " ")
	if strings.Contains(s, "-reconnect") {
		t.Error("reconnect flags should not be used for HLS")
	}
	if strings.Contains(s, "-ss") {
		t.Error("no seek for start 0")
	}
	if !strings.Contains(s, "-map 0:a:0?") || !strings.Contains(s, "w=1920:h=1080") {
		t.Errorf("unexpected args: %s", s)
	}
}

func TestFilterEscape(t *testing.T) {
	if got := filterEscape(`C:\a'b,c`); got != `C\\:\\\\a\\\'b\,c` {
		t.Fatalf("got %s", got)
	}
}

func TestParseProgress(t *testing.T) {
	f, ok := ParseProgress("out_time_us=15000000", 30)
	if !ok || f != 0.5 {
		t.Fatalf("%v %v", f, ok)
	}
	if _, ok := ParseProgress("frame=10", 30); ok {
		t.Fatal("parsed non-time line")
	}
}
