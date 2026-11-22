package captions

import (
	"strings"
	"testing"

	"github.com/malinanu/youtubeclipping/api/internal/media"
)

var sample = []media.Word{
	{Start: 0, End: 300, Text: "Hello"},
	{Start: 300, End: 600, Text: "there,"},
	{Start: 650, End: 900, Text: "this"},
	{Start: 900, End: 1200, Text: "is"},
	{Start: 1200, End: 1700, Text: "{great}."},
	{Start: 3000, End: 3400, Text: "Next"},
}

func TestAssColor(t *testing.T) {
	if got := assColor("#FFE81F"); got != "&H001FE8FF" {
		t.Fatalf("got %s", got)
	}
	if got := assColor("bogus"); got != "&H00FFFFFF" {
		t.Fatalf("got %s", got)
	}
}

func TestAssTime(t *testing.T) {
	if got := assTime(3723456); got != "1:02:03.45" {
		t.Fatalf("got %s", got)
	}
}

func TestGroup(t *testing.T) {
	g := Group(sample, 3, 32)
	if len(g) != 3 {
		t.Fatalf("want 3 groups, got %d: %+v", len(g), g)
	}
	if g[1][len(g[1])-1].Text != "{great}." || g[2][0].Text != "Next" {
		t.Fatalf("unexpected grouping %+v", g)
	}
}

func TestBuildASSHighlight(t *testing.T) {
	st := DefaultStyle()
	out := BuildASS(sample, st, 1080, 1920)
	if !strings.Contains(out, "PlayResY: 1920") {
		t.Fatal("missing PlayRes")
	}
	// One dialogue per word for highlight mode.
	if n := strings.Count(out, "Dialogue:"); n != len(sample) {
		t.Fatalf("want %d dialogues, got %d\n%s", len(sample), n, out)
	}
	if strings.Contains(out, "{great}") || !strings.Contains(out, "(GREAT).") {
		t.Fatal("braces must be escaped and text uppercased")
	}
	if !strings.Contains(out, `{\c&H1FE8FF&}HELLO`) {
		t.Fatalf("active word not highlighted\n%s", out)
	}
}

func TestBuildASSKaraoke(t *testing.T) {
	st := DefaultStyle()
	st.Animation = "karaoke"
	st.Uppercase = false
	out := BuildASS(sample, st, 1080, 1920)
	if !strings.Contains(out, `{\kf30}Hello {\kf30}there,`) {
		t.Fatalf("karaoke tags missing\n%s", out)
	}
	if !strings.Contains(out, `{\k5}`) {
		t.Fatalf("gap tag missing\n%s", out)
	}
}

func TestBuildSRT(t *testing.T) {
	out := BuildSRT(sample)
	if !strings.HasPrefix(out, "1\n00:00:00,000 --> 00:00:01,700\nHello there, this is {great}.\n\n2\n00:00:03,000") {
		t.Fatalf("unexpected srt:\n%s", out)
	}
}

func TestFontFamily(t *testing.T) {
	if f, b := FontFamily("Montserrat", 900); f != "Montserrat Black" || b != 0 {
		t.Fatal(f, b)
	}
	if f, b := FontFamily("Inter", 700); f != "Inter" || b != -1 {
		t.Fatal(f, b)
	}
	if f, _ := FontFamily("Bebas Neue", 900); f != "Bebas Neue" {
		t.Fatal(f)
	}
}
