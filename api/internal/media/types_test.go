package media

import "testing"

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
