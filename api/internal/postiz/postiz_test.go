package postiz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadAndCreatePost(t *testing.T) {
	var post map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "key123" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/public/v1/upload":
			f, h, err := r.FormFile("file")
			if err != nil || h.Filename != "clip.mp4" {
				w.WriteHeader(400)
				return
			}
			b, _ := io.ReadAll(f)
			if string(b) != "video" {
				w.WriteHeader(400)
				return
			}
			_, _ = io.WriteString(w, `{"id":"m1","path":"https://cdn/x.mp4"}`)
		case "/api/public/v1/posts":
			_ = json.NewDecoder(r.Body).Decode(&post)
			_, _ = io.WriteString(w, `[{"postId":"p1","integration":"i1"}]`)
		case "/api/public/v1/integrations":
			_, _ = io.WriteString(w, `[{"id":"i1","name":"My Channel","identifier":"youtube","disabled":false}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := New(srv.URL+"/api/public/v1/", "key123")
	ints, err := c.Integrations(context.Background())
	if err != nil || len(ints) != 1 || ints[0].Identifier != "youtube" {
		t.Fatalf("%v %+v", err, ints)
	}
	file := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(file, []byte("video"), 0o644)
	m, err := c.Upload(context.Background(), file)
	if err != nil || m.ID != "m1" {
		t.Fatalf("upload: %v %+v", err, m)
	}
	when := time.Now().Add(2 * time.Hour)
	if _, err := c.CreatePost(context.Background(), CreatePostInput{
		Content: "hello", Title: "T", When: &when, Media: m,
		Targets: []Target{{IntegrationID: "i1", Identifier: "youtube", Settings: map[string]any{"type": "unlisted"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if post["type"] != "schedule" {
		t.Fatalf("type %v", post["type"])
	}
	p := post["posts"].([]any)[0].(map[string]any)
	st := p["settings"].(map[string]any)
	if st["__type"] != "youtube" || st["type"] != "unlisted" || st["title"] != "T" {
		t.Fatalf("settings %v", st)
	}
	img := p["value"].([]any)[0].(map[string]any)["image"].([]any)[0].(map[string]any)
	if img["path"] != "https://cdn/x.mp4" {
		t.Fatalf("image %v", img)
	}
	bad := New(srv.URL+"/api/public/v1", "wrong")
	if _, err := bad.Integrations(context.Background()); err == nil {
		t.Fatal("expected auth error")
	}
}
