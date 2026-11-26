package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// geminiRetryUnit scales the backoff on 429/5xx: waits are 1x, 3x, 6x, 12x
// (5s, 15s, 30s, 60s by default). Tests shrink it.
var geminiRetryUnit = 5 * time.Second

var geminiBackoff = []int{1, 3, 6, 12}

// APIError is a non-2xx response from an HTTP LLM provider.
type APIError struct {
	Provider   string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: http %d: %s", e.Provider, e.StatusCode, e.Body)
}

// jsonFormatHint spells out the output shape in the prompt, so the reply is
// parseable even if the API ignores or rejects the structured-output schema.
const jsonFormatHint = `

Respond with JSON only, no prose and no code fences, in exactly this shape:
{"clips":[{"start":<seconds number>,"end":<seconds number>,"title":"<max 70 chars>","hook":"<opening line or idea>","reason":"<why it works standalone>","score":<0-100>}]}`

// geminiCompleter calls the Gemini API generateContent endpoint with an API key.
type geminiCompleter struct {
	key, model, base string
	http             *http.Client
	retryWait        time.Duration
}

func newGemini(opt Options) *geminiCompleter {
	base := opt.BaseURL
	if base == "" {
		base = geminiBaseURL
	}
	return &geminiCompleter{key: opt.APIKey, model: opt.Model, base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 8 * time.Minute}, retryWait: geminiRetryUnit}
}

type gPart struct {
	Text    string `json:"text,omitempty"`
	Thought bool   `json:"thought,omitempty"`
}

type gContent struct {
	Role  string  `json:"role,omitempty"`
	Parts []gPart `json:"parts"`
}

type gResponse struct {
	Candidates []struct {
		Content      gContent `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

func (g *geminiCompleter) complete(ctx context.Context, system, user string) (string, error) {
	text, err := g.completeModel(ctx, g.model, system, user)
	// The chosen model stays overloaded after all retries: try the default
	// (usually the lighter, higher-capacity) model once for this request.
	var apierr *APIError
	if errors.As(err, &apierr) && apierr.StatusCode == http.StatusServiceUnavailable && g.model != DefaultGeminiModel {
		text, err = g.completeModel(ctx, DefaultGeminiModel, system, user)
	}
	return text, err
}

func (g *geminiCompleter) completeModel(ctx context.Context, model, system, user string) (string, error) {
	text, err := g.call(ctx, model, system, user, true)
	// Older/other models may reject the JSON-schema field; retry once relying on
	// JSON mode + the format spelled out in the prompt.
	var apierr *APIError
	if errors.As(err, &apierr) && apierr.StatusCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(apierr.Body), "schema") {
		text, err = g.call(ctx, model, system, user, false)
	}
	return text, err
}

func (g *geminiCompleter) call(ctx context.Context, model, system, user string, withSchema bool) (string, error) {
	cfg := map[string]any{
		"responseMimeType": "application/json",
		"temperature":      0.4,
		"maxOutputTokens":  32768,
	}
	if withSchema {
		cfg["responseJsonSchema"] = schema
	}
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": gContent{Parts: []gPart{{Text: system}}},
		"contents":          []gContent{{Role: "user", Parts: []gPart{{Text: user + jsonFormatHint}}}},
		"generationConfig":  cfg,
	})
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", g.base, url.PathEscape(model))

	var lastErr error
	var retryAfter time.Duration
	for attempt := 0; attempt <= len(geminiBackoff); attempt++ {
		if attempt > 0 {
			wait := time.Duration(geminiBackoff[attempt-1]) * g.retryWait
			if retryAfter > wait {
				wait = min(retryAfter, 2*time.Minute)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", g.key)
		resp, err := g.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gemini: %w", err)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			lastErr = &APIError{Provider: "gemini", StatusCode: resp.StatusCode, Body: trunc(string(data), 600)}
			if resp.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(string(data)), "not found") {
				lastErr = &APIError{Provider: "gemini", StatusCode: resp.StatusCode,
					Body: fmt.Sprintf("model %q not found for this API key; choose a model in Settings → Gemini model", model)}
			}
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				retryAfter = 0
				if sec, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && sec > 0 {
					retryAfter = time.Duration(sec) * time.Second
				}
				continue
			}
			return "", lastErr
		}
		return parseGemini(data)
	}
	return "", lastErr
}

func parseGemini(data []byte) (string, error) {
	var r gResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("gemini: decode response: %w", err)
	}
	if br := r.PromptFeedback.BlockReason; br != "" && br != "BLOCK_REASON_UNSPECIFIED" {
		return "", fmt.Errorf("Gemini declined to analyse this transcript (%s)", br)
	}
	if len(r.Candidates) == 0 {
		return "", errors.New("gemini: empty response")
	}
	c := r.Candidates[0]
	switch c.FinishReason {
	case "", "STOP", "FINISH_REASON_UNSPECIFIED":
	case "MAX_TOKENS":
		return "", errors.New("Gemini response was truncated (MAX_TOKENS)")
	default:
		return "", fmt.Errorf("Gemini declined to analyse this transcript (%s)", c.FinishReason)
	}
	var text strings.Builder
	for _, p := range c.Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("gemini: response had no text")
	}
	return text.String(), nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ListGeminiModels returns the model IDs (without the "models/" prefix) that
// support generateContent for this key, sorted. An empty base uses the public API.
func ListGeminiModels(ctx context.Context, base, key string) ([]string, error) {
	if base == "" {
		base = geminiBaseURL
	}
	base = strings.TrimRight(base, "/")
	client := &http.Client{Timeout: 20 * time.Second}
	var out []string
	token := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-goog-api-key", key)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gemini: %w", err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return nil, &APIError{Provider: "gemini", StatusCode: resp.StatusCode, Body: trunc(string(data), 600)}
		}
		var r struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			Next string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("gemini: list models: %w", err)
		}
		for _, m := range r.Models {
			for _, meth := range m.Methods {
				if meth == "generateContent" {
					out = append(out, strings.TrimPrefix(m.Name, "models/"))
					break
				}
			}
		}
		if r.Next == "" {
			break
		}
		token = r.Next
	}
	sort.Strings(out)
	return out, nil
}
