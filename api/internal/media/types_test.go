package media

import (
	"strings"
	"testing"
)

func TestMergeOverridesAndRange(t *testing.T) {
	words := []Word{{0, 100, "a", 0}, {100, 200, "b", 0}, {200, 300, "c", 0}, {300, 400, "d", 0}}
	ov := []Word{{100, 200, "B!", 0}, {200, 300, " ", 0}}
	m := MergeOverrides(words, ov)
	if len(m) != 3 || m[1].Text != "B!" || m[2].Text != "d" {
		t.Fatalf("%+v", m)
	}
	r := WordsInRange(m, 150, 350)
	if len(r) != 2 || r[0].Start != 0 || r[0].End != 50 || r[1].Start != 150 || r[1].End != 200 {
		t.Fatalf("%+v", r)
	}
}

func TestCollapseRepeats(t *testing.T) {
	mk := func(s string) []Word {
		var ws []Word
		for i, f := range strings.Fields(s) {
			ws = append(ws, Word{int64(i * 100), int64(i*100 + 90), f, 0})
		}
		return ws
	}
	join := func(ws []Word) string {
		var p []string
		for _, w := range ws {
			p = append(p, w.Text)
		}
		return strings.Join(p, " ")
	}
	loop := "Intro. " + strings.Repeat("Click the video to get a photo of the video. ", 6) + "Bye."
	if got := join(CollapseRepeats(mk(loop))); got != "Intro. Click the video to get a photo of the video. Bye." {
		t.Fatalf("loop not collapsed: %q", got)
	}
	keep := "no no no I said go go go now"
	if got := join(CollapseRepeats(mk(keep))); got != keep {
		t.Fatalf("emphasis must be kept: %q", got)
	}
}
