package ytdlp

import (
	"errors"
	"strings"
	"testing"
)

// Exact line from a production worker log.
const prodBotCheck = "yt-dlp probe: exit status 1: ERROR: [youtube] W7Ut51YeQ6U: Sign in to confirm you’re not a bot. This helps protect our community. Use --cookies-from-browser or --cookies for the authentication."

func TestIsPermanentAndFriendly(t *testing.T) {
	cases := []struct {
		err       string
		permanent bool
		friendly  string
	}{
		{prodBotCheck, true, "Settings → YouTube access"},
		{"ERROR: [youtube] x: Private video. Sign in if you've been granted access", true, "private or members-only"},
		{"ERROR: [youtube] x: Video unavailable", true, "unavailable"},
		{"ERROR: unable to download video data: HTTP Error 403: Forbidden", false, "403"},
		{"connection reset by peer", false, "connection reset"},
	}
	for _, c := range cases {
		err := errors.New(c.err)
		if IsPermanent(err) != c.permanent {
			t.Errorf("IsPermanent(%q) != %v", c.err, c.permanent)
		}
		if got := FriendlyError(err); !strings.Contains(got, c.friendly) {
			t.Errorf("FriendlyError(%q) = %q", c.err, got)
		}
	}
	if IsPermanent(nil) {
		t.Error("nil is not permanent")
	}
}

func TestInspectCookies(t *testing.T) {
	loggedIn := "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t1893456000\tPREF\tf6=40000000\n" +
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t1893456000\t__Secure-3PSID\tabc123\n" +
		".google.com\tTRUE\t/\tTRUE\t1893456000\tSID\txyz\n"
	r := InspectCookies([]byte(loggedIn))
	if r.YouTube != 2 || !r.LoggedIn {
		t.Fatalf("logged-in file: %+v", r)
	}
	loggedOut := ".youtube.com\tTRUE\t/\tTRUE\t1893456000\tVISITOR_INFO1_LIVE\tq\n"
	if r := InspectCookies([]byte(loggedOut)); r.YouTube != 1 || r.LoggedIn {
		t.Fatalf("logged-out file: %+v", r)
	}
	other := ".example.com\tTRUE\t/\tFALSE\t0\tsid\t1\n"
	if r := InspectCookies([]byte(other)); r.YouTube != 0 {
		t.Fatalf("non-youtube file: %+v", r)
	}
}

func TestMergeCookies(t *testing.T) {
	yt := ".youtube.com\tTRUE\t/\tTRUE\t1893456000\tLOGIN_INFO\tyt1\n"
	tw := ".twitch.tv\tTRUE\t/\tTRUE\t1893456000\tauth-token\ttw1\n.example.com\tTRUE\t/\tFALSE\t0\tx\t1\n"
	merged := MergeCookies([]byte(yt), []byte(tw))
	r := InspectCookies(merged)
	if r.YouTube != 1 || r.Twitch != 1 || !r.LoggedIn || !r.TwitchLoggedIn {
		t.Fatalf("merge lost a site: %+v\n%s", r, merged)
	}
	if strings.Contains(string(merged), "example.com") {
		t.Fatal("unsupported domains must be dropped")
	}
	// Re-uploading YouTube replaces only YouTube cookies.
	yt2 := "#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t1893456000\t__Secure-3PSID\tyt2\n"
	merged = MergeCookies(merged, []byte(yt2))
	if strings.Contains(string(merged), "yt1") || !strings.Contains(string(merged), "yt2") || !strings.Contains(string(merged), "tw1") {
		t.Fatalf("replace by site failed:\n%s", merged)
	}
	if !strings.HasPrefix(string(merged), "# Netscape HTTP Cookie File") {
		t.Fatal("header missing")
	}
}

func TestLiveAndSubscriberOnly(t *testing.T) {
	if !IsPermanent(ErrLive) || !strings.Contains(FriendlyError(ErrLive), "stream ends") {
		t.Fatal("live")
	}
	sub := errors.New("ERROR: [twitch:vod] 2932443336: You must be logged into an account that has access to this subscriber-only content.")
	if !IsPermanent(sub) || !strings.Contains(FriendlyError(sub), "subscriber-only") {
		t.Fatalf("sub-only: %s", FriendlyError(sub))
	}
}
