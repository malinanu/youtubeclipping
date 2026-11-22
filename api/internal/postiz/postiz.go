// Package postiz is a minimal client for the Postiz Public API.
//
// Self-hosted base URL is usually https://<postiz-host>/api/public/v1
// (cloud: https://api.postiz.com/public/v1). Auth header is the raw API key.
package postiz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(base, key string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), APIKey: key, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) Configured() bool { return c.BaseURL != "" && c.APIKey != "" }

type Integration struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
	Picture    string `json:"picture"`
	Disabled   bool   `json:"disabled"`
	Profile    string `json:"profile"`
}

type Media struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func (c *Client) do(req *http.Request, out any) (json.RawMessage, error) {
	if !c.Configured() {
		return nil, errors.New("postiz is not configured")
	}
	req.Header.Set("Authorization", c.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if resp.StatusCode >= 300 {
		return data, fmt.Errorf("postiz %s %s: http %d: %s", req.Method, req.URL.Path, resp.StatusCode, trunc(string(data), 500))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return data, fmt.Errorf("postiz decode: %w", err)
		}
	}
	return data, nil
}

func (c *Client) Integrations(ctx context.Context) ([]Integration, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/integrations", nil)
	var out []Integration
	_, err := c.do(req, &out)
	return out, err
}

// Upload streams a file to Postiz media storage.
func (c *Client) Upload(ctx context.Context, path string) (*Media, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filepath.Base(path))
		if err == nil {
			_, err = io.Copy(part, f)
		}
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/upload", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var m Media
	if _, err := c.do(req, &m); err != nil {
		return nil, err
	}
	if m.Path == "" {
		return nil, errors.New("postiz upload returned no path")
	}
	return &m, nil
}

// Target is one channel to post to, with provider-specific settings.
type Target struct {
	IntegrationID string         `json:"integrationId"`
	Identifier    string         `json:"identifier"`
	Settings      map[string]any `json:"settings"`
}

type CreatePostInput struct {
	Content string
	Title   string
	When    *time.Time // nil = post now
	Media   *Media
	Targets []Target
	Draft   bool
}

// DefaultSettings returns settings for common video providers. Callers can
// override any key via Target.Settings.
func DefaultSettings(identifier, title string) map[string]any {
	s := map[string]any{"__type": identifier}
	switch identifier {
	case "youtube":
		s["title"] = trunc(title, 100)
		s["type"] = "public"
		s["selfDeclaredMadeForKids"] = "no"
	case "tiktok":
		s["privacy_level"] = "PUBLIC_TO_EVERYONE"
		s["duet"] = false
		s["stitch"] = false
		s["comment"] = true
		s["autoAddMusic"] = "no"
		s["brand_content_toggle"] = false
		s["brand_organic_toggle"] = false
		s["content_posting_method"] = "DIRECT_POST"
	case "instagram", "instagram-standalone":
		s["post_type"] = "post"
	case "x":
		s["who_can_reply_post"] = "everyone"
	}
	return s
}

func (c *Client) CreatePost(ctx context.Context, in CreatePostInput) (json.RawMessage, error) {
	if len(in.Targets) == 0 {
		return nil, errors.New("no integrations selected")
	}
	typ := "now"
	date := time.Now().UTC()
	if in.Draft {
		typ = "draft"
	} else if in.When != nil && in.When.After(time.Now()) {
		typ = "schedule"
		date = in.When.UTC()
	}
	var images []Media
	if in.Media != nil {
		images = []Media{*in.Media}
	}
	posts := make([]map[string]any, 0, len(in.Targets))
	for _, t := range in.Targets {
		settings := DefaultSettings(t.Identifier, in.Title)
		for k, v := range t.Settings {
			settings[k] = v
		}
		posts = append(posts, map[string]any{
			"integration": map[string]any{"id": t.IntegrationID},
			"value":       []map[string]any{{"content": in.Content, "image": images}},
			"settings":    settings,
		})
	}
	body, _ := json.Marshal(map[string]any{
		"type":      typ,
		"date":      date.Format("2006-01-02T15:04:05.000Z"),
		"shortLink": false,
		"tags":      []any{},
		"posts":     posts,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/posts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, nil)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
