package ytdlp

import (
	"bufio"
	"errors"
	"strings"
)

// ErrLive is returned for streams that are live or upcoming.
var ErrLive = errors.New("live streams are not supported; wait until the stream ends and the VOD is available")

// botCheck is YouTube's "Sign in to confirm you're not a bot" challenge.
func botCheck(s string) bool {
	return strings.Contains(s, "Sign in to confirm") || strings.Contains(s, "not a bot")
}

var permanentMarkers = []string{
	"Private video", "members-only", "Join this channel",
	"Video unavailable", "This video has been removed", "This video is no longer available",
	"is not available in your country", "Unsupported URL", "does not exist",
	"subscriber-only", "must be logged into an account",
}

// IsPermanent reports yt-dlp failures that retrying won't fix. A bot check
// counts: it needs cookies or a proxy, so retrying just wastes minutes.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if botCheck(s) || errors.Is(err, ErrLive) {
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
	case strings.Contains(s, "no downloadable formats from YouTube") && strings.Contains(s, "page needs to be reloaded"):
		return "YouTube rejected every player client (\"The page needs to be reloaded\"). Your cookies were most likely rotated or invalidated by YouTube. Re-export them with the incognito steps in Settings, upload, click Test, then Retry. Details: " + trim(s, 600)
	case strings.Contains(s, "no downloadable formats from YouTube"):
		return "YouTube accepted the request but offered no downloadable streams for any player client. Usually the cookies were rotated or flagged: re-export them (incognito steps in Settings → YouTube access), upload, Test, then Retry. If it keeps happening, set YTDLP_PROXY. Details: " + trim(s, 900)
	case errors.Is(err, ErrLive):
		return "This is a live stream (or a premiere that's on air). ClipForge can clip it once the stream ends and becomes a normal video — import it again then."
	case strings.Contains(s, "subscriber-only") || strings.Contains(s, "must be logged into an account"):
		return "This video is subscriber-only. Upload cookies.txt from a browser logged into an account that's subscribed (Settings → Cookies), then Retry. " + trim(s, 200)
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

// CookieReport summarises a Netscape cookies.txt.
type CookieReport struct {
	YouTube        int  // cookies for youtube.com
	Twitch         int  // cookies for twitch.tv
	LoggedIn       bool // has a YouTube/Google session cookie
	TwitchLoggedIn bool // has a Twitch auth-token cookie
}

var loginCookies = map[string]bool{
	"LOGIN_INFO": true, "SID": true, "__Secure-1PSID": true, "__Secure-3PSID": true, "SAPISID": true,
}

// cookieSite maps a cookie domain to the site it belongs to ("" if unsupported).
func cookieSite(domain string) string {
	d := strings.TrimPrefix(strings.ToLower(domain), ".")
	switch {
	case d == "youtube.com" || strings.HasSuffix(d, ".youtube.com"):
		return "youtube"
	case d == "twitch.tv" || strings.HasSuffix(d, ".twitch.tv"):
		return "twitch"
	}
	return ""
}

type cookieLine struct {
	raw  string
	site string
}

// parseCookies returns the cookie lines (with #HttpOnly_ prefix preserved).
func parseCookies(data []byte) []cookieLine {
	var out []cookieLine
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		raw := strings.TrimRight(sc.Text(), "\r")
		line := strings.TrimPrefix(strings.TrimSpace(raw), "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		out = append(out, cookieLine{raw: strings.TrimSpace(raw), site: cookieSite(f[0])})
	}
	return out
}

// InspectCookies parses a Netscape cookies.txt (tab-separated:
// domain, flag, path, secure, expiry, name, value).
func InspectCookies(data []byte) CookieReport {
	var r CookieReport
	for _, c := range parseCookies(data) {
		f := strings.Split(strings.TrimPrefix(c.raw, "#HttpOnly_"), "\t")
		name, val := f[5], strings.TrimSpace(f[6])
		switch c.site {
		case "youtube":
			r.YouTube++
			if loginCookies[name] && val != "" {
				r.LoggedIn = true
			}
		case "twitch":
			r.Twitch++
			if name == "auth-token" && val != "" {
				r.TwitchLoggedIn = true
			}
		}
	}
	return r
}

// MergeCookies replaces the cookies of every site present in upload and keeps
// the existing cookies for other sites, so uploading Twitch cookies doesn't
// wipe working YouTube ones (and vice versa). Unsupported domains are dropped.
func MergeCookies(existing, upload []byte) []byte {
	newSites := map[string]bool{}
	up := parseCookies(upload)
	for _, c := range up {
		if c.site != "" {
			newSites[c.site] = true
		}
	}
	var lines []string
	for _, c := range parseCookies(existing) {
		if c.site != "" && !newSites[c.site] {
			lines = append(lines, c.raw)
		}
	}
	for _, c := range up {
		if c.site != "" {
			lines = append(lines, c.raw)
		}
	}
	return []byte("# Netscape HTTP Cookie File\n# Managed by ClipForge\n" + strings.Join(lines, "\n") + "\n")
}
