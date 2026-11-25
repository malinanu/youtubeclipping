package tasks

import (
	"errors"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/malinanu/youtubeclipping/api/internal/config"
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
