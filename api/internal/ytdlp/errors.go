package ytdlp

import (
	"bufio"
	"strings"
)

// botCheck is YouTube's "Sign in to confirm you're not a bot" challenge.
func botCheck(s string) bool {
	return strings.Contains(s, "Sign in to confirm") || strings.Contains(s, "not a bot")
}

var permanentMarkers = []string{
	"Private video", "members-only", "Join this channel",
	"Video unavailable", "This video has been removed", "This video is no longer available",
	"is not available in your country", "Unsupported URL", "does not exist",
}

// IsPermanent reports yt-dlp failures that retrying won't fix. A bot check
// counts: it needs cookies or a proxy, so retrying just wastes minutes.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if botCheck(s) {
		return true
	}
	for _, m := range permanentMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// FriendlyError maps common yt-dlp failures to actionable messages.
func FriendlyError(err error) string {
	s := err.Error()
	switch {
	case botCheck(s):
		return "YouTube is blocking this server (\"Sign in to confirm you're not a bot\"). Upload a fresh cookies.txt from a logged-in browser in Settings → YouTube access, click \"Test YouTube access\", then Retry. Details: " + trim(s, 300)
	case strings.Contains(s, "Private video") || strings.Contains(s, "members-only") || strings.Contains(s, "Join this channel"):
		return "This video is private or members-only. Upload cookies for an account that can watch it. " + trim(s, 200)
	case strings.Contains(s, "Video unavailable") || strings.Contains(s, "has been removed") || strings.Contains(s, "no longer available"):
		return "This video is unavailable (removed, region-locked or wrong link). " + trim(s, 200)
	case strings.Contains(s, "no downloadable formats from YouTube"):
		return "YouTube accepted the request but offered no downloadable streams for any player client. Usually the cookies were rotated or flagged: re-export them (incognito steps in Settings → YouTube access), upload, Test, then Retry. If it keeps happening, set YTDLP_PROXY. Details: " + trim(s, 900)
	case strings.Contains(s, "HTTP Error 403"):
		return "The platform refused the download (403). Update yt-dlp or upload fresh cookies. " + trim(s, 300)
	}
	return trim(s, 1500)
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// CookieReport summarises a Netscape cookies.txt for YouTube use.
type CookieReport struct {
	YouTube  int  // cookies for youtube.com
	LoggedIn bool // has a Google/YouTube session cookie
}

var loginCookies = map[string]bool{
	"LOGIN_INFO": true, "SID": true, "__Secure-1PSID": true, "__Secure-3PSID": true, "SAPISID": true,
}

// InspectCookies parses a Netscape cookies.txt (tab-separated:
// domain, flag, path, secure, expiry, name, value).
func InspectCookies(data []byte) CookieReport {
	var r CookieReport
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(f[0]), ".")
		if domain != "youtube.com" && !strings.HasSuffix(domain, ".youtube.com") {
			continue
		}
		r.YouTube++
		if loginCookies[f[5]] && strings.TrimSpace(f[6]) != "" {
			r.LoggedIn = true
		}
	}
	return r
}
