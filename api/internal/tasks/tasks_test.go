package tasks

import (
	"testing"

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
