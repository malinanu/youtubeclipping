// Package media holds shared transcript types and filesystem layout helpers.
package media

import (
	"os"
	"path/filepath"
	"strings"
)

// Word is a single transcribed word with millisecond timestamps.
type Word struct {
	Start int64   `json:"s"`
	End   int64   `json:"e"`
	Text  string  `json:"t"`
	Prob  float64 `json:"p,omitempty"`
}

// Segment is a sentence-level grouping of words.
type Segment struct {
	Start int64  `json:"s"`
	End   int64  `json:"e"`
	Text  string `json:"t"`
}

// WordsInRange returns words overlapping [start,end), rebased so start==0.
func WordsInRange(words []Word, start, end int64) []Word {
	out := make([]Word, 0, 64)
	for _, w := range words {
		if w.End <= start || w.Start >= end {
			continue
		}
		nw := w
		nw.Start = max(w.Start, start) - start
		nw.End = min(w.End, end) - start
		if nw.End <= nw.Start {
			nw.End = nw.Start + 1
		}
		out = append(out, nw)
	}
	return out
}

// MergeOverrides replaces transcript words inside the overrides' time span with
// the overrides (per-clip caption fixes) and drops words whose text is blank.
func MergeOverrides(words, overrides []Word) []Word {
	if len(overrides) == 0 {
		return words
	}
	a, b := overrides[0].Start, overrides[len(overrides)-1].End
	out := make([]Word, 0, len(words)+len(overrides))
	for _, w := range words {
		if w.End <= a {
			out = append(out, w)
		}
	}
	for _, w := range overrides {
		if strings.TrimSpace(w.Text) != "" {
			out = append(out, w)
		}
	}
	for _, w := range words {
		if w.Start >= b {
			out = append(out, w)
		}
	}
	return out
}

// BuildSegments groups words into sentence-ish segments for display and LLM input.
func BuildSegments(words []Word) []Segment {
	var segs []Segment
	var cur []Word
	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, len(cur))
		for i, w := range cur {
			parts[i] = w.Text
		}
		segs = append(segs, Segment{Start: cur[0].Start, End: cur[len(cur)-1].End, Text: strings.Join(parts, " ")})
		cur = cur[:0]
	}
	for i, w := range words {
		cur = append(cur, w)
		endsSentence := strings.HasSuffix(w.Text, ".") || strings.HasSuffix(w.Text, "?") || strings.HasSuffix(w.Text, "!")
		gap := i+1 < len(words) && words[i+1].Start-w.End > 1200
		if (endsSentence && len(cur) >= 4) || gap || len(cur) >= 40 {
			flush()
		}
	}
	flush()
	return segs
}

// Layout describes where files live under DATA_DIR.
type Layout struct{ Root string }

func (l Layout) VideoDir(id string) string { return filepath.Join(l.Root, "videos", id) }
func (l Layout) ClipDir(id string) string  { return filepath.Join(l.Root, "clips", id) }
func (l Layout) TmpDir() string            { return filepath.Join(l.Root, "tmp") }

func (l Layout) Ensure(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Rel converts an absolute path under Root to a root-relative path for storage.
func (l Layout) Rel(p string) string {
	r, err := filepath.Rel(l.Root, p)
	if err != nil {
		return p
	}
	return r
}

// Abs resolves a stored relative path, refusing anything escaping Root.
func (l Layout) Abs(rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	p := filepath.Join(l.Root, filepath.Clean("/"+rel))
	if !strings.HasPrefix(p, filepath.Clean(l.Root)+string(os.PathSeparator)) {
		return "", false
	}
	return p, true
}
