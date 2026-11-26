package ffmpeg

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mediaPlaylist(n int, extra string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:100\n#EXT-X-PLAYLIST-TYPE:EVENT\n" + extra)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "#EXTINF:10.000,\n%d.ts\n", i)
	}
	return b.String() // no ENDLIST, like a Twitch VOD
}

func TestTrimPlaylist(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/vod/chunked/index-dvr.m3u8")
	out, first, err := trimPlaylist(mediaPlaylist(30, `#EXT-X-MAP:URI="init.mp4"`+"\n"), base, 95, 30)
	if err != nil {
		t.Fatal(err)
	}
	if first != 90 {
		t.Fatalf("first=%v", first)
	}
	for _, want := range []string{
		"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-MEDIA-SEQUENCE:109", "#EXT-X-ENDLIST",
		`#EXT-X-MAP:URI="https://cdn.example/vod/chunked/init.mp4"`,
		"https://cdn.example/vod/chunked/9.ts", "https://cdn.example/vod/chunked/12.ts",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "/8.ts") || strings.Contains(out, "/13.ts") || strings.Contains(out, "EVENT") {
		t.Fatalf("unexpected content:\n%s", out)
	}

	if _, _, err := trimPlaylist(mediaPlaylist(3, ""), base, 500, 30); err == nil {
		t.Fatal("range past the end must fail")
	}
	bad := "#EXTM3U\n#EXTINF:10,\nfile:///etc/passwd\n"
	if _, _, err := trimPlaylist(bad, base, 0, 5); err == nil {
		t.Fatal("non-http segment must be rejected")
	}
}

func TestPickVariant(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/master.m3u8")
	m := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=9000000,RESOLUTION=2560x1440\nsrc/1440.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080\nsrc/1080.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=640x360\nsrc/360.m3u8\n"
	u, err := pickVariant(m, base)
	if err != nil || u.String() != "https://cdn.example/src/1080.m3u8" {
		t.Fatalf("%v %v", u, err)
	}
}

// TestTrimHLSRender reproduces the Twitch failure: seeking in a remote EVENT
// playlist without ENDLIST writes an empty file; the trimmed playlist works.
func TestTrimHLSRender(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30:duration=60",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=60",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "60", "-c:a", "aac",
		"-output_ts_offset", "10", "-f", "hls", "-hls_time", "10", "-hls_playlist_type", "event",
		filepath.Join(dir, "index.m3u8"))
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("gen: %v %s", err, out)
	}
	pl, _ := os.ReadFile(filepath.Join(dir, "index.m3u8"))
	_ = os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(strings.ReplaceAll(string(pl), "#EXT-X-ENDLIST\n", "")), 0o644)
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	work := t.TempDir()
	in, off, err := TrimHLS(context.Background(), srv.Client(), Input{Path: srv.URL + "/index.m3u8",
		Headers: map[string]string{"User-Agent": "x", "Referer": "https://www.twitch.tv/"}}, 33, 8, work)
	if err != nil {
		t.Fatal(err)
	}
	if off != 3 {
		t.Fatalf("offset %v", off)
	}
	r := &Runner{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	out := filepath.Join(work, "out.mp4")
	if err := r.Render(context.Background(), RenderOptions{Inputs: []Input{in}, StartSec: off, DurSec: 8,
		Aspect: "9:16", CropX: 0.5, Output: out, Preset: "ultrafast"}, nil); err != nil {
		t.Fatal(err)
	}
	pr, err := r.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Width == 0 || !pr.HasAudio || pr.DurationSec < 7.5 || pr.DurationSec > 8.5 {
		t.Fatalf("unexpected output %+v", pr)
	}
}

func TestRenderArgsLocalPlaylistHasNoHeaders(t *testing.T) {
	r := &Runner{}
	h := map[string]string{"User-Agent": "x"}
	local := strings.Join(r.RenderArgs(RenderOptions{Inputs: []Input{{Path: "/tmp/w/trim.m3u8", Headers: h}}, DurSec: 5, Output: "o.mp4"}), " ")
	if strings.Contains(local, "-headers") || !strings.Contains(local, "file,http,https") {
		t.Fatalf("local playlist args: %s", local)
	}
	remote := strings.Join(r.RenderArgs(RenderOptions{Inputs: []Input{{Path: "https://x/v.mp4", Headers: h}}, DurSec: 5, Output: "o.mp4"}), " ")
	if !strings.Contains(remote, "-headers") {
		t.Fatalf("remote args: %s", remote)
	}
}
