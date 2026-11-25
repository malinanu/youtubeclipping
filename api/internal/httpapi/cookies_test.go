package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/malinanu/youtubeclipping/api/internal/auth"
	"github.com/malinanu/youtubeclipping/api/internal/config"
	"github.com/malinanu/youtubeclipping/api/internal/ytdlp"
)

func testServer(t *testing.T, ytdlpScript string) (*Server, *http.Cookie) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+ytdlpScript), 0o755); err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	a := auth.New("a@b.c", string(hash), []byte(strings.Repeat("s", 32)), time.Hour, false)
	cfg := &config.Config{AdminEmail: "a@b.c", CookiesFile: filepath.Join(dir, "cookies.txt")}
	s := &Server{Cfg: cfg, Auth: a, YtDlp: &ytdlp.Client{Bin: bin, CookiesFile: cfg.CookiesFile},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return s, &http.Cookie{Name: auth.CookieName, Value: a.Issue(time.Now())}
}

func upload(t *testing.T, s *Server, ck *http.Cookie, content string) (int, map[string]any) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "cookies.txt")
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/cookies", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Requested-With", "clipforge")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestUploadCookiesValidation(t *testing.T) {
	s, ck := testServer(t, "exit 0\n")
	code, out := upload(t, s, ck, ".example.com\tTRUE\t/\tFALSE\t0\tsid\t1\n")
	if code != 400 || !strings.Contains(out["error"].(string), "no youtube.com cookies") {
		t.Fatalf("non-youtube: %d %v", code, out)
	}
	code, out = upload(t, s, ck, ".youtube.com\tTRUE\t/\tTRUE\t1893456000\tVISITOR_INFO1_LIVE\tq\n")
	if code != 200 || out["loggedIn"] != false || out["warning"] == "" {
		t.Fatalf("logged-out: %d %v", code, out)
	}
	code, out = upload(t, s, ck, ".youtube.com\tTRUE\t/\tTRUE\t1893456000\tLOGIN_INFO\tabc\n")
	if code != 200 || out["loggedIn"] != true || out["warning"] != "" {
		t.Fatalf("logged-in: %d %v", code, out)
	}
	if b, _ := os.ReadFile(s.Cfg.CookiesFile); !strings.Contains(string(b), "LOGIN_INFO") {
		t.Fatal("cookies not saved")
	}
}

func youtubeTest(t *testing.T, s *Server, ck *http.Cookie) map[string]any {
	req := httptest.NewRequest(http.MethodPost, "/api/settings/youtube-test", nil)
	req.Header.Set("X-Requested-With", "clipforge")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestYouTubeTestEndpoint(t *testing.T) {
	s, ck := testServer(t, `echo '{"id":"jNQXAC9IVRw","title":"Me at the zoo","duration":19}'`+"\n")
	if out := youtubeTest(t, s, ck); out["ok"] != true || out["title"] != "Me at the zoo" {
		t.Fatalf("success case: %v", out)
	}
	s, ck = testServer(t, "echo \"ERROR: [youtube] jNQXAC9IVRw: Sign in to confirm you're not a bot.\" >&2\nexit 1\n")
	out := youtubeTest(t, s, ck)
	if out["ok"] != false || !strings.Contains(out["error"].(string), "Settings → YouTube access") {
		t.Fatalf("bot-check case: %v", out)
	}
}
