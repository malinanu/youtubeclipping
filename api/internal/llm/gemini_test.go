package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const geminiOK = `{"candidates":[{"content":{"role":"model","parts":[
	{"text":"thinking...","thought":true},
	{"text":"{\"clips\":[{\"start\":5,\"end\":40,\"title\":\"G\",\"hook\":\"h\",\"reason\":\"r\",\"score\":77}]}"}]},
	"finishReason":"STOP"}]}`

func TestGeminiSuggest(t *testing.T) {
	var body map[string]any
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("x-goog-api-key")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, geminiOK)
	}))
	defer srv.Close()

	out, err := Suggest(context.Background(), Options{Provider: ProviderGemini, APIKey: "gk", BaseURL: srv.URL,
		VideoTitle: "T", MinSec: 20, MaxSec: 60, Count: 3}, mkWords(200))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "G" || out[0].Score != 77 {
		t.Fatalf("%+v", out)
	}
	if path != "/models/"+DefaultGeminiModel+":generateContent" || key != "gk" {
		t.Fatalf("path=%s key=%s", path, key)
	}
	gc := body["generationConfig"].(map[string]any)
	if gc["responseMimeType"] != "application/json" || gc["responseJsonSchema"] == nil {
		t.Fatalf("generationConfig: %v", gc)
	}
	si := body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(si, "short-form video editor") {
		t.Fatal("system instruction missing")
	}
	user := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(user, "<transcript>") || !strings.Contains(user, `{"clips":[`) {
		t.Fatal("user prompt missing transcript or format hint")
	}
}

func TestGeminiSchemaFallbackAndRetry(t *testing.T) {
	var calls atomic.Int32
	var sawSchema []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		_, has := b["generationConfig"].(map[string]any)["responseJsonSchema"]
		sawSchema = append(sawSchema, has)
		switch n {
		case 1:
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"Invalid JSON payload received. Unknown name \"responseJsonSchema\""}}`)
		case 2:
			w.WriteHeader(429)
		default:
			_, _ = io.WriteString(w, geminiOK)
		}
	}))
	defer srv.Close()
	g := newGemini(Options{APIKey: "k", Model: "m", BaseURL: srv.URL})
	g.retryWait = time.Millisecond
	txt, err := g.complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt, `"clips"`) || len(sawSchema) != 3 || !sawSchema[0] || sawSchema[1] || sawSchema[2] {
		t.Fatalf("txt=%q schema=%v", txt, sawSchema)
	}
}

func TestGeminiErrors(t *testing.T) {
	cases := map[string]string{
		"truncated": `{"candidates":[{"content":{"parts":[{"text":"{"}]},"finishReason":"MAX_TOKENS"}]}`,
		"declined":  `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`,
		"blocked":   `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`,
	}
	for want, resp := range cases {
		if _, err := parseGemini([]byte(resp)); err == nil || !strings.Contains(strings.ToLower(err.Error()), map[string]string{
			"truncated": "truncated", "declined": "declined", "blocked": "declined"}[want]) {
			t.Fatalf("%s: %v", want, err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"error":{"message":"API key not valid"}}`)
	}))
	defer srv.Close()
	_, err := Suggest(context.Background(), Options{Provider: ProviderGemini, APIKey: "bad", BaseURL: srv.URL}, mkWords(100))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("want permanent error, got %v", err)
	}
	if _, err := Suggest(context.Background(), Options{Provider: ProviderGemini}, mkWords(10)); !IsPermanent(err) ||
		!strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("missing key: %v", err)
	}
}

func TestParseResponseCodeFence(t *testing.T) {
	got, err := ParseResponse("```json\n{\"clips\":[{\"start\":1,\"end\":30,\"title\":\"x\",\"hook\":\"\",\"reason\":\"\",\"score\":5}]}\n```")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
}
