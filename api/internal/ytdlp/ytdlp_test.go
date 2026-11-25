package ytdlp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateURL(t *testing.T) {
	cases := []struct {
		in, platform string
		ok           bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PL123&index=2", "youtube", true},
		{"https://youtu.be/dQw4w9WgXcQ", "youtube", true},
		{"https://m.youtube.com/watch?v=abc", "youtube", true},
		{"https://www.twitch.tv/videos/123456789", "twitch", true},
		{"https://clips.twitch.tv/SomeClipSlug", "twitch", true},
		{"https://evil.com/?u=youtube.com", "", false},
		{"file:///etc/passwd", "", false},
		{"--exec=rm -rf /", "", false},
		{"https://youtube.com.evil.com/watch?v=1", "", false},
	}
	for _, c := range cases {
		norm, p, err := ValidateURL(c.in)
		if c.ok != (err == nil) {
			t.Fatalf("%s: ok=%v err=%v", c.in, c.ok, err)
		}
		if c.ok && p != c.platform {
			t.Fatalf("%s: platform %s", c.in, p)
		}
		if c.in == cases[0].in && norm != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
			t.Fatalf("playlist params not stripped: %s", norm)
		}
	}
}

func TestProgressTracker(t *testing.T) {
	tr := &progressTracker{}
	f, ok := tr.parse(`CFPROG {"status":"downloading","downloaded_bytes":50,"total_bytes":100,"filename":"v.mp4"}`)
	if !ok || f <= 0 || f >= 0.5 {
		t.Fatalf("first file progress: %v %v", f, ok)
	}
	f2, _ := tr.parse(`CFPROG {"status":"downloading","downloaded_bytes":50,"total_bytes":100,"filename":"a.m4a"}`)
	if f2 < f || f2 < 0.7 || f2 > 0.8 {
		t.Fatalf("second file progress: %v", f2)
	}
	f3, _ := tr.parse(`CFPROG {"status":"downloading","fragment_index":9,"fragment_count":10,"filename":"a.m4a"}`)
	if f3 < 0.9 {
		t.Fatalf("fragment progress: %v", f3)
	}
	if _, ok := tr.parse("[download] Destination: x"); ok {
		t.Fatal("non-progress line parsed")
	}
}

func TestProbeTimeout(t *testing.T) {
	dir := t.TempDir()
	fake := dir + "/yt-dlp"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := ProbeTimeout
	ProbeTimeout = 300 * time.Millisecond
	defer func() { ProbeTimeout = old }()
	start := time.Now()
	_, err := (&Client{Bin: fake}).Probe(context.Background(), "https://youtu.be/x", "")
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

// fakeYtDlp writes a script that only succeeds for one player client and
// logs every invocation's args.
func fakeYtDlp(t *testing.T, okClient string) (string, string) {
	dir := t.TempDir()
	log := dir + "/calls.log"
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"case \"$*\" in *player_client=" + okClient + "*) echo '{\"id\":\"x\",\"title\":\"OK\",\"duration\":10}'; exit 0;; esac\n" +
		"echo 'WARNING: [youtube] x: Some web client https formats have been skipped as they are missing a url' >&2\n" +
		"echo 'ERROR: [youtube] x: Requested format is not available. Use --list-formats' >&2\nexit 1\n"
	bin := dir + "/yt-dlp"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestPlayerClientFallback(t *testing.T) {
	bin, log := fakeYtDlp(t, "web_safari")
	c := &Client{Bin: bin}
	info, err := c.Probe(context.Background(), "https://www.youtube.com/watch?v=x", "")
	if err != nil || info.Title != "OK" {
		t.Fatalf("fallback failed: %v", err)
	}
	calls, _ := os.ReadFile(log)
	if n := strings.Count(string(calls), "\n"); n != 3 { // default, tv, web_safari
		t.Fatalf("want 3 attempts, got %d:\n%s", n, calls)
	}
	if strings.Contains(string(calls), "--no-warnings") {
		t.Fatal("warnings must not be suppressed")
	}
	// The working client is remembered and tried first.
	_ = os.Remove(log)
	if _, err := c.Probe(context.Background(), "https://youtu.be/x", ""); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(log)
	if n := strings.Count(string(calls), "\n"); n != 1 || !strings.Contains(string(calls), "player_client=web_safari") {
		t.Fatalf("preferred client not reused:\n%s", calls)
	}
}

func TestPlayerClientFallbackExhausted(t *testing.T) {
	bin, _ := fakeYtDlp(t, "nonexistent")
	_, err := (&Client{Bin: bin}).Probe(context.Background(), "https://www.youtube.com/watch?v=x", "")
	if err == nil || !strings.Contains(err.Error(), "no downloadable formats from YouTube") ||
		!strings.Contains(err.Error(), "missing a url") {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(FriendlyError(err), "re-export") {
		t.Fatalf("friendly: %s", FriendlyError(err))
	}
}

func TestTwitchSkipsClientFallback(t *testing.T) {
	bin, log := fakeYtDlp(t, "x")
	_, _ = (&Client{Bin: bin}).Probe(context.Background(), "https://www.twitch.tv/videos/1", "")
	calls, _ := os.ReadFile(log)
	if n := strings.Count(string(calls), "\n"); n != 1 || strings.Contains(string(calls), "player_client") {
		t.Fatalf("twitch should run once without player_client:\n%s", calls)
	}
}
