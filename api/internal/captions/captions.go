// Package captions renders word-timed transcripts to ASS (styled, burned in) and SRT.
package captions

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/malinanu/youtubeclipping/api/internal/media"
)

// Style mirrors caption_styles.config.
type Style struct {
	Font         string  `json:"font"`
	Weight       int     `json:"weight"`
	Size         int     `json:"size"`
	Primary      string  `json:"primary"`
	Highlight    string  `json:"highlight"`
	Outline      string  `json:"outline"`
	OutlineWidth float64 `json:"outlineWidth"`
	Shadow       float64 `json:"shadow"`
	Position     string  `json:"position"` // bottom|middle|top
	MarginV      int     `json:"marginV"`  // relative to a 1920px tall canvas
	WordsPerLine int     `json:"wordsPerLine"`
	Uppercase    bool    `json:"uppercase"`
	Animation    string  `json:"animation"` // highlight|karaoke|pop|none
}

func DefaultStyle() Style {
	return Style{Font: "Montserrat", Weight: 900, Size: 78, Primary: "#FFFFFF", Highlight: "#FFE81F",
		Outline: "#000000", OutlineWidth: 6, Shadow: 2, Position: "bottom", MarginV: 420, WordsPerLine: 3,
		Uppercase: true, Animation: "highlight"}
}

func ParseStyle(raw json.RawMessage) Style {
	s := DefaultStyle()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	if s.WordsPerLine <= 0 {
		s.WordsPerLine = 3
	}
	if s.Size <= 0 {
		s.Size = 72
	}
	return s
}

// assColor converts #RRGGBB to &H00BBGGRR.
func assColor(hex string) string {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		return "&H00FFFFFF"
	}
	if _, err := strconv.ParseUint(h, 16, 32); err != nil {
		return "&H00FFFFFF"
	}
	return "&H00" + strings.ToUpper(h[4:6]+h[2:4]+h[0:2])
}

func inlineColor(hex string) string { return strings.TrimPrefix(assColor(hex), "&H00") }

func assTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	cs := ms / 10
	h := cs / 360000
	m := (cs / 6000) % 60
	s := (cs / 100) % 60
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs%100)
}

func assEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "{", "(")
	s = strings.ReplaceAll(s, "}", ")")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// FontFamily maps a family + CSS weight to the fontconfig family libass should
// request. Static font packages expose heavy weights as their own legacy family
// ("Montserrat Black"), which is more reliable than synthetic bold.
func FontFamily(font string, weight int) (string, int) {
	font = strings.TrimSpace(font)
	if font == "" {
		font = "Montserrat"
	}
	if strings.Contains(strings.ToLower(font), "bebas") {
		return font, 0 // single-weight display face
	}
	switch {
	case weight >= 900:
		return font + " Black", 0
	case weight >= 800:
		return font + " ExtraBold", 0
	case weight >= 700:
		return font, -1
	case weight >= 600:
		return font + " SemiBold", 0
	case weight >= 500:
		return font + " Medium", 0
	}
	return font, 0
}

// Group splits words into caption lines.
func Group(words []media.Word, perLine int, maxChars int) [][]media.Word {
	var groups [][]media.Word
	var cur []media.Word
	chars := 0
	for i, w := range words {
		wl := utf8.RuneCountInString(w.Text)
		if len(cur) > 0 && (len(cur) >= perLine || chars+wl+1 > maxChars || w.Start-cur[len(cur)-1].End > 700) {
			groups = append(groups, cur)
			cur, chars = nil, 0
		}
		cur = append(cur, w)
		chars += wl + 1
		t := strings.TrimSpace(w.Text)
		if strings.HasSuffix(t, ".") || strings.HasSuffix(t, "?") || strings.HasSuffix(t, "!") {
			if i+1 < len(words) {
				groups = append(groups, cur)
				cur, chars = nil, 0
			}
		}
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// BuildASS renders an ASS subtitle file for a canvas of w×h.
func BuildASS(words []media.Word, st Style, w, h int) string {
	scale := float64(h) / 1920.0
	if w > h { // landscape: size relative to the shorter edge
		scale = float64(h) / 1080.0 * 0.75
	}
	fontSize := int(float64(st.Size) * scale)
	marginV := int(float64(st.MarginV) * float64(h) / 1920.0)
	align := 2
	switch st.Position {
	case "middle":
		align, marginV = 5, 0
	case "top":
		align = 8
	}
	family, bold := FontFamily(st.Font, st.Weight)
	primary, secondary := assColor(st.Primary), assColor(st.Primary)
	if st.Animation == "karaoke" {
		// Karaoke fills from Secondary to Primary.
		primary, secondary = assColor(st.Highlight), assColor(st.Primary)
	}

	var b strings.Builder
	b.WriteString("[Script Info]\nScriptType: v4.00+\nWrapStyle: 0\nScaledBorderAndShadow: yes\nYCbCr Matrix: TV.709\n")
	fmt.Fprintf(&b, "PlayResX: %d\nPlayResY: %d\n\n", w, h)
	b.WriteString("[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(&b, "Style: Default,%s,%d,%s,%s,%s,&H80000000,%d,0,0,0,100,100,0,0,1,%.1f,%.1f,%d,%d,%d,%d,1\n\n",
		family, fontSize, primary, secondary, assColor(st.Outline), bold, st.OutlineWidth*scale, st.Shadow*scale,
		align, int(60*scale), int(60*scale), marginV)
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	maxChars := 32
	if w > h {
		maxChars = 48
	}
	groups := Group(words, st.WordsPerLine, maxChars)
	text := func(wd media.Word) string {
		t := strings.TrimSpace(wd.Text)
		if st.Uppercase {
			t = strings.ToUpper(t)
		}
		return assEscape(t)
	}
	hl := inlineColor(st.Highlight)
	pc := inlineColor(st.Primary)

	for gi, g := range groups {
		gStart := g[0].Start
		gEnd := g[len(g)-1].End
		// Hold the line until the next one starts if the gap is short.
		if gi+1 < len(groups) {
			if next := groups[gi+1][0].Start; next-gEnd < 400 {
				gEnd = next
			}
		}
		switch st.Animation {
		case "karaoke":
			var line strings.Builder
			cursor := gStart
			for _, wd := range g {
				if gap := wd.Start - cursor; gap > 0 {
					fmt.Fprintf(&line, "{\\k%d}", gap/10)
				}
				fmt.Fprintf(&line, "{\\kf%d}%s ", max((wd.End-wd.Start)/10, 1), text(wd))
				cursor = wd.End
			}
			fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(gStart), assTime(gEnd), strings.TrimSpace(line.String()))
		case "highlight", "pop":
			for wi, active := range g {
				start := active.Start
				if wi == 0 {
					start = gStart
				}
				end := gEnd
				if wi+1 < len(g) {
					end = g[wi+1].Start
				}
				if end <= start {
					continue
				}
				parts := make([]string, len(g))
				for j, wd := range g {
					if j == wi {
						anim := ""
						if st.Animation == "pop" {
							anim = `\fscx85\fscy85\t(0,90,\fscx115\fscy115)\t(90,180,\fscx105\fscy105)`
						}
						parts[j] = fmt.Sprintf("{\\c&H%s&%s}%s{\\c&H%s&\\fscx100\\fscy100}", hl, anim, text(wd), pc)
					} else {
						parts[j] = text(wd)
					}
				}
				fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(start), assTime(end), strings.Join(parts, " "))
			}
		default:
			parts := make([]string, len(g))
			for j, wd := range g {
				parts[j] = text(wd)
			}
			fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(gStart), assTime(gEnd), strings.Join(parts, " "))
		}
	}
	return b.String()
}

func srtTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, (ms/60000)%60, (ms/1000)%60, ms%1000)
}

// BuildSRT renders standard subtitle cues (max ~42 chars, 8 words).
func BuildSRT(words []media.Word) string {
	var b strings.Builder
	for i, g := range Group(words, 8, 42) {
		parts := make([]string, len(g))
		for j, w := range g {
			parts[j] = strings.TrimSpace(w.Text)
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(g[0].Start), srtTime(g[len(g)-1].End), strings.Join(parts, " "))
	}
	return b.String()
}
