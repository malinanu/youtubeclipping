package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/malinanu/youtubeclipping/api/internal/media"
)

func mkWords(n int) []media.Word {
	ws := make([]media.Word, n)
	for i := range ws {
		ws[i] = media.Word{Start: int64(i * 500), End: int64(i*500 + 400), Text: "w"}
	}
	return ws
}

func TestParseResponse(t *testing.T) {
	got, err := ParseResponse(`{"clips":[{"start":1.5,"end":40,"title":"T","hook":"H","reason":"R","score":88}]}`)
	if err != nil || len(got) != 1 || got[0].Score != 88 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := ParseResponse("not json"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPostprocess(t *testing.T) {
	words := mkWords(400) // 200 s of speech
	in := []Suggestion{
		{StartSec: 10.2, EndSec: 40.1, Score: 70},
		{StartSec: 20, EndSec: 50, Score: 90},   // overlaps the first, higher score wins
		{StartSec: 100, EndSec: 103, Score: 99}, // too short
		{StartSec: 120, EndSec: 300, Score: 60}, // too long -> trimmed
		{StartSec: 50, EndSec: 40, Score: 50},   // invalid
	}
	out := Postprocess(in, words, 20, 60, 5)
	if len(out) != 2 {
		t.Fatalf("want 2, got %+v", out)
	}
	if out[0].StartSec != 20 || out[0].Score != 90 {
		t.Fatalf("overlap resolution wrong: %+v", out[0])
	}
	if d := out[1].EndSec - out[1].StartSec; d > 61 {
		t.Fatalf("not trimmed: %v", d)
	}
}

func TestSuggestAgainstFakeAPI(t *testing.T) {
	var got map[string]any
	var beta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beta = r.Header.Get("anthropic-beta")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
			"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":10},
			"content":[{"type":"text","text":"{\"clips\":[{\"start\":5,\"end\":40,\"title\":\"Hook\",\"hook\":\"h\",\"reason\":\"r\",\"score\":91}]}"}]}`)
	}))
	defer srv.Close()

	out, err := Suggest(context.Background(), Options{APIKey: "k", BaseURL: srv.URL, VideoTitle: "T", MinSec: 20, MaxSec: 60, Count: 3}, mkWords(200))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "Hook" || out[0].StartSec != 5 {
		t.Fatalf("%+v", out)
	}
	if got["model"] != "claude-opus-5-5" || got["fallbacks"] != "default" {
		t.Fatalf("model/fallbacks: %v %v", got["model"], got["fallbacks"])
	}
	oc := got["output_config"].(map[string]any)
	if oc["effort"] != "medium" || oc["format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("output_config: %v", oc)
	}
	if !strings.Contains(beta, "server-side-fallback-2026-07-01") {
		t.Fatalf("beta header: %q", beta)
	}
	msgs := got["messages"].([]any)
	content := fmt.Sprint(msgs[0])
	if !strings.Contains(content, "[0.0-") || !strings.Contains(content, "between 20 and 60 seconds") {
		t.Fatalf("prompt missing transcript/limits: %s", content[:200])
	}
}

func TestIsPermanent(t *testing.T) {
	if !IsPermanent(ErrNoAPIKey) || IsPermanent(errors.New("x")) {
		t.Fatal("IsPermanent wrong")
	}
}
