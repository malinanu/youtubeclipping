package httpapi

import (
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"Finding the ultimate tactical milkmobile": "Finding the ultimate tactical milkmobile",
		"What's up? (part 2) #1 & more!":           "What's up (part 2) #1 & more!",
		"ලංකා 🔥 clip":                              "ලංකා 🔥 clip",
		"a/b\\c:d":                                 "a b c d",
		"  ...  ":                                  "clip",
		"":                                         "clip",
		"line\nbreak\ttab":                         "line break tab",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := safeName(strings.Repeat("ශ", 100))
	if n := len([]rune(long)); n != 80 {
		t.Fatalf("long title: %d runes", n)
	}
}

func TestContentDisposition(t *testing.T) {
	got := contentDisposition("What's up 🔥.mp4")
	want := `attachment; filename="What's up.mp4"; filename*=UTF-8''What%27s%20up%20%F0%9F%94%A5.mp4`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := contentDisposition("ලංකා.srt"); !strings.HasPrefix(got, `attachment; filename="clip.srt";`) {
		t.Fatalf("non-ascii fallback: %s", got)
	}
}
