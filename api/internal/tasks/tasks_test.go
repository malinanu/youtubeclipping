package tasks

import (
	"errors"
	"strings"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/malinanu/youtubeclipping/api/internal/config"
	"github.com/malinanu/youtubeclipping/api/internal/ffmpeg"
	"github.com/malinanu/youtubeclipping/api/internal/store"
)

func TestSuggestProvider(t *testing.T) {
	cfg := &config.Config{AIProvider: "auto", ClaudeModel: "claude-opus-5-5", GeminiModel: "gemini-3.5-flash-lite"}
	cases := []struct {
		s                 store.Settings
		wantProv, wantKey string
	}{
		{store.Settings{}, "claude", ""},
		{store.Settings{GeminiAPIKey: "g"}, "gemini", "g"},
		{store.Settings{GeminiAPIKey: "g", AnthropicAPIKey: "a"}, "claude", "a"},
		{store.Settings{GeminiAPIKey: "g", AnthropicAPIKey: "a", AIProvider: "gemini"}, "gemini", "g"},
		{store.Settings{GeminiAPIKey: "g", AIProvider: "claude"}, "claude", ""},
	}
	for i, c := range cases {
		p, k, m := Resolve(cfg, &c.s).SuggestProvider()
		if p != c.wantProv || k != c.wantKey || m == "" {
			t.Fatalf("case %d: got %s %s %s", i, p, k, m)
		}
	}
}

func TestAutoSuggest(t *testing.T) {
	cfg := &config.Config{AIProvider: "auto"}
	off := false
	if e := Resolve(cfg, &store.Settings{GeminiAPIKey: "g"}); !e.AutoSuggest || !e.HasAIKey() {
		t.Fatal("auto-suggest should default on with a Gemini key")
	}
	if e := Resolve(cfg, &store.Settings{GeminiAPIKey: "g", AutoSuggest: &off}); e.AutoSuggest {
		t.Fatal("auto-suggest should respect explicit off")
	}
	if Resolve(cfg, &store.Settings{}).HasAIKey() {
		t.Fatal("no key configured")
	}
	if Resolve(cfg, &store.Settings{GeminiAPIKey: "g", AIProvider: "claude"}).HasAIKey() {
		t.Fatal("forced claude without its key has no usable key")
	}
}

func TestOutputOK(t *testing.T) {
	good := &ffmpeg.ProbeResult{DurationSec: 30, Width: 1080, Height: 1920, VideoCodec: "h264"}
	if err := outputOK(good, 32); err != nil {
		t.Fatal(err)
	}
	for name, pr := range map[string]*ffmpeg.ProbeResult{
		"nil":      nil,
		"no video": {DurationSec: 30, HasAudio: true},
		"empty":    {DurationSec: 0, Width: 1080, VideoCodec: "h264"},
		"short":    {DurationSec: 10, Width: 1080, VideoCodec: "h264"},
	} {
		if outputOK(pr, 32) == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

func TestVideoFirst(t *testing.T) {
	audio := &ffmpeg.ProbeResult{HasAudio: true, DurationSec: 10}
	video := &ffmpeg.ProbeResult{Width: 1920, VideoCodec: "h264", DurationSec: 10}
	if o, err := videoFirst([]*ffmpeg.ProbeResult{audio, video}); err != nil || o[0] != 1 || o[1] != 0 {
		t.Fatalf("%v %v", o, err)
	}
	if o, err := videoFirst([]*ffmpeg.ProbeResult{video}); err != nil || len(o) != 1 || o[0] != 0 {
		t.Fatalf("%v %v", o, err)
	}
	if _, err := videoFirst([]*ffmpeg.ProbeResult{audio}); err == nil || !strings.Contains(err.Error(), "no video") {
		t.Fatalf("want no-video error, got %v", err)
	}
}

func TestRetryableBotCheck(t *testing.T) {
	bot := errors.New("yt-dlp probe: exit status 1: ERROR: [youtube] W7Ut51YeQ6U: Sign in to confirm you’re not a bot.")
	if !errors.Is(retryable(bot), asynq.SkipRetry) {
		t.Fatal("bot check should skip retries")
	}
	transient := errors.New("connection reset by peer")
	if errors.Is(retryable(transient), asynq.SkipRetry) {
		t.Fatal("transient error must be retried")
	}
}
