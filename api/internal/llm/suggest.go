// Package llm uses Claude to find highlight clips in a transcript.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/malinanu/youtubeclipping/api/internal/media"
)

type Suggestion struct {
	StartSec float64 `json:"start"`
	EndSec   float64 `json:"end"`
	Title    string  `json:"title"`
	Hook     string  `json:"hook"`
	Reason   string  `json:"reason"`
	Score    float64 `json:"score"`
}

type Options struct {
	APIKey       string
	Model        string
	VideoTitle   string
	MinSec       int
	MaxSec       int
	Count        int
	Instructions string // optional user guidance, e.g. "focus on funny moments"
	BaseURL      string // tests only
}

var schema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"clips": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"start":  map[string]any{"type": "number", "description": "Clip start in seconds from video start"},
					"end":    map[string]any{"type": "number", "description": "Clip end in seconds from video start"},
					"title":  map[string]any{"type": "string", "description": "Short punchy title for the short-form post (max 70 chars)"},
					"hook":   map[string]any{"type": "string", "description": "The opening line or idea that grabs attention"},
					"reason": map[string]any{"type": "string", "description": "Why this segment works as a standalone clip"},
					"score":  map[string]any{"type": "number", "description": "Virality potential 0-100"},
				},
				"required":             []string{"start", "end", "title", "hook", "reason", "score"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"clips"},
	"additionalProperties": false,
}

const systemPrompt = `You are an expert short-form video editor who finds the best standalone moments in long videos for YouTube Shorts, TikTok and Instagram Reels.

You will receive a timestamped transcript. Pick segments that:
- Start on a strong hook (a bold claim, question, surprising fact, punchline setup, or emotional moment) - never mid-sentence.
- Are self-contained: a viewer with no context understands and enjoys it.
- End on a natural conclusion, payoff or punchline - never mid-sentence.
- Do not overlap each other.

Use the timestamps exactly as given in the transcript; start at the beginning of a line and end at the end of a line. Score honestly: reserve 80+ for moments with genuine viral potential.`

// formatTranscript renders segments as "[start-end] text" with seconds.
func formatTranscript(segs []media.Segment) string {
	var b strings.Builder
	for _, s := range segs {
		fmt.Fprintf(&b, "[%.1f-%.1f] %s\n", float64(s.Start)/1000, float64(s.End)/1000, s.Text)
	}
	return b.String()
}

// windows splits a transcript into chunks of at most maxChars so very long
// streams stay well inside the context window.
func windows(segs []media.Segment, maxChars int) [][]media.Segment {
	var out [][]media.Segment
	var cur []media.Segment
	n := 0
	for _, s := range segs {
		l := len(s.Text) + 20
		if n+l > maxChars && len(cur) > 0 {
			out = append(out, cur)
			cur, n = nil, 0
		}
		cur = append(cur, s)
		n += l
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// Suggest asks Claude for highlight clips and snaps them to word boundaries.
func Suggest(ctx context.Context, opt Options, words []media.Word) ([]Suggestion, error) {
	if opt.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	if len(words) == 0 {
		return nil, errors.New("transcript is empty")
	}
	if opt.MinSec <= 0 {
		opt.MinSec = 20
	}
	if opt.MaxSec <= opt.MinSec {
		opt.MaxSec = max(opt.MinSec+10, 75)
	}
	if opt.Count <= 0 {
		opt.Count = 8
	}
	if opt.Model == "" {
		opt.Model = "claude-opus-5-5"
	}
	reqOpts := []option.RequestOption{option.WithAPIKey(opt.APIKey), option.WithRequestTimeout(8 * time.Minute), option.WithMaxRetries(3)}
	if opt.BaseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(opt.BaseURL))
	}
	client := anthropic.NewClient(reqOpts...)

	segs := media.BuildSegments(words)
	wins := windows(segs, 400_000) // ~100k tokens per request
	perWin := opt.Count
	if len(wins) > 1 {
		perWin = max(3, (opt.Count*2)/len(wins)+1)
	}

	var all []Suggestion
	for _, w := range wins {
		user := fmt.Sprintf("Video title: %s\n\nFind up to %d clips, each between %d and %d seconds long.\n",
			opt.VideoTitle, perWin, opt.MinSec, opt.MaxSec)
		if s := strings.TrimSpace(opt.Instructions); s != "" {
			user += "Additional instructions from the editor: " + s + "\n"
		}
		user += "\n<transcript>\n" + formatTranscript(w) + "</transcript>"

		params := anthropic.BetaMessageNewParams{
			Model:     anthropic.Model(opt.Model),
			MaxTokens: 16000,
			System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt}},
			Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user))},
			OutputConfig: anthropic.BetaOutputConfigParam{
				Effort: anthropic.BetaOutputConfigEffortMedium,
				Format: anthropic.BetaJSONOutputFormatParam{Schema: schema},
			},
			// Re-serve on a fallback model if the primary declines (e.g. classifier false positive).
			Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
			Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		}
		resp, err := client.Beta.Messages.New(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("claude: %w", err)
		}
		switch resp.StopReason {
		case anthropic.BetaStopReasonRefusal:
			return nil, errors.New("Claude declined to analyse this transcript")
		case anthropic.BetaStopReasonMaxTokens:
			return nil, errors.New("Claude response was truncated (max_tokens)")
		}
		var text strings.Builder
		for _, block := range resp.Content {
			if tb, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
				text.WriteString(tb.Text)
			}
		}
		got, err := ParseResponse(text.String())
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	return Postprocess(all, words, opt.MinSec, opt.MaxSec, opt.Count), nil
}

// ErrNoAPIKey is returned when no Anthropic key is configured.
var ErrNoAPIKey = errors.New("Anthropic API key not configured (Settings or ANTHROPIC_API_KEY)")

// IsPermanent reports errors that retrying will not fix (bad key, bad model, bad request).
func IsPermanent(err error) bool {
	if errors.Is(err, ErrNoAPIKey) {
		return true
	}
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		c := apierr.StatusCode
		return c >= 400 && c < 500 && c != 408 && c != 409 && c != 429
	}
	return false
}

func ParseResponse(s string) ([]Suggestion, error) {
	var out struct {
		Clips []Suggestion `json:"clips"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &out); err != nil {
		return nil, fmt.Errorf("parse claude json: %w", err)
	}
	return out.Clips, nil
}

// Postprocess snaps to word boundaries, enforces duration bounds, removes
// overlaps (keeping higher scores) and returns the top `count` by score.
func Postprocess(in []Suggestion, words []media.Word, minSec, maxSec, count int) []Suggestion {
	if len(words) == 0 {
		return nil
	}
	videoEnd := float64(words[len(words)-1].End) / 1000
	var cleaned []Suggestion
	for _, s := range in {
		if s.EndSec <= s.StartSec || s.StartSec < 0 || s.StartSec >= videoEnd {
			continue
		}
		s.StartSec = snapStart(words, s.StartSec)
		s.EndSec = snapEnd(words, s.EndSec)
		d := s.EndSec - s.StartSec
		if d < float64(minSec)*0.7 {
			continue
		}
		if d > float64(maxSec)*1.15 {
			s.EndSec = snapEnd(words, s.StartSec+float64(maxSec))
		}
		if s.Score < 0 {
			s.Score = 0
		}
		if s.Score > 100 {
			s.Score = 100
		}
		cleaned = append(cleaned, s)
	}
	sort.SliceStable(cleaned, func(i, j int) bool { return cleaned[i].Score > cleaned[j].Score })
	var picked []Suggestion
	for _, s := range cleaned {
		overlap := false
		for _, p := range picked {
			if s.StartSec < p.EndSec && p.StartSec < s.EndSec {
				overlap = true
				break
			}
		}
		if !overlap {
			picked = append(picked, s)
		}
		if len(picked) >= count {
			break
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].StartSec < picked[j].StartSec })
	return picked
}

// snapStart moves to the start of the first word at or after t-0.3s.
func snapStart(words []media.Word, t float64) float64 {
	ms := int64(t*1000) - 300
	for _, w := range words {
		if w.Start >= ms {
			return float64(w.Start) / 1000
		}
	}
	return t
}

// snapEnd moves to the end of the last word ending at or before t+0.5s, plus a short tail.
func snapEnd(words []media.Word, t float64) float64 {
	ms := int64(t*1000) + 500
	best := int64(-1)
	for _, w := range words {
		if w.End <= ms {
			best = w.End
		} else {
			break
		}
	}
	if best < 0 {
		return t
	}
	return float64(best+250) / 1000
}
