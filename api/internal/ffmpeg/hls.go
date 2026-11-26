package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// IsHLS reports whether a remote input is an HLS playlist.
func (in Input) IsHLS() bool {
	return in.remote() && strings.Contains(strings.ToLower(in.Path), ".m3u8")
}

// TrimHLS fetches a remote HLS playlist and writes a local VOD playlist that
// lists only the segments covering [startSec, startSec+durSec], with absolute
// segment URLs. ffmpeg seeking inside long remote playlists is unreliable:
// Twitch VODs are often EVENT playlists without #EXT-X-ENDLIST, which ffmpeg
// treats as live and then writes an empty output while still exiting 0.
// It returns the local input and the seek offset into the first kept segment.
func TrimHLS(ctx context.Context, client *http.Client, in Input, startSec, durSec float64, dir string) (Input, float64, error) {
	if client == nil {
		client = http.DefaultClient
	}
	base, err := url.Parse(in.Path)
	if err != nil {
		return in, 0, err
	}
	body, err := fetchPlaylist(ctx, client, base, in.Headers)
	if err != nil {
		return in, 0, err
	}
	if strings.Contains(body, "#EXT-X-STREAM-INF") {
		variant, err := pickVariant(body, base)
		if err != nil {
			return in, 0, err
		}
		base = variant
		if body, err = fetchPlaylist(ctx, client, base, in.Headers); err != nil {
			return in, 0, err
		}
	}
	out, first, err := trimPlaylist(body, base, startSec, durSec)
	if err != nil {
		return in, 0, err
	}
	p := filepath.Join(dir, "trim.m3u8")
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return in, 0, err
	}
	return Input{Path: p, Headers: in.Headers}, startSec - first, nil
}

func fetchPlaylist(ctx context.Context, client *http.Client, u *url.URL, headers map[string]string) (string, error) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("hls: unsupported playlist scheme %q", u.Scheme)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("hls: fetch playlist: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("hls: fetch playlist: http %d", resp.StatusCode)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "#EXTM3U") {
		return "", errors.New("hls: not an m3u8 playlist")
	}
	return string(data), nil
}

var (
	bandwidthRe  = regexp.MustCompile(`(?:^|,)BANDWIDTH=(\d+)`)
	resolutionRe = regexp.MustCompile(`RESOLUTION=\d+x(\d+)`)
	uriAttrRe    = regexp.MustCompile(`URI="([^"]*)"`)
)

// pickVariant chooses the highest-bandwidth variant at or below 1080p.
func pickVariant(body string, base *url.URL) (*url.URL, error) {
	lines := splitLines(body)
	var best *url.URL
	bestBW := -1
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "#EXT-X-STREAM-INF:") || i+1 >= len(lines) {
			continue
		}
		attrs := lines[i]
		uri := lines[i+1]
		if uri == "" || strings.HasPrefix(uri, "#") {
			continue
		}
		bw := 0
		if m := bandwidthRe.FindStringSubmatch(attrs); m != nil {
			bw, _ = strconv.Atoi(m[1])
		}
		if m := resolutionRe.FindStringSubmatch(attrs); m != nil {
			if h, _ := strconv.Atoi(m[1]); h > 1080 {
				continue
			}
		}
		u, err := resolveHTTP(base, uri)
		if err != nil {
			continue
		}
		if bw > bestBW {
			best, bestBW = u, bw
		}
	}
	if best == nil {
		return nil, errors.New("hls: no usable variant in master playlist")
	}
	return best, nil
}

// trimPlaylist keeps the segments overlapping [start, start+dur+2s] and
// returns the new playlist plus the start time of its first segment.
func trimPlaylist(body string, base *url.URL, start, dur float64) (string, float64, error) {
	lines := splitLines(body)
	var head []string // tags that apply to the whole playlist
	var b strings.Builder
	end := start + dur + 2
	t := 0.0
	seq := 0
	firstSeq := -1
	first := 0.0
	kept := 0
	var pending []string // per-segment tags seen since the last URI
	var curMap, curKey string
	segDur := -1.0
	for _, l := range lines {
		switch {
		case l == "" || l == "#EXTM3U":
		case strings.HasPrefix(l, "#EXT-X-VERSION"), strings.HasPrefix(l, "#EXT-X-TARGETDURATION"),
			strings.HasPrefix(l, "#EXT-X-INDEPENDENT-SEGMENTS"):
			head = append(head, l)
		case strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.Atoi(strings.TrimPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"))
		case strings.HasPrefix(l, "#EXT-X-MAP:"), strings.HasPrefix(l, "#EXT-X-KEY:"):
			r, err := resolveURIAttr(l, base)
			if err != nil {
				return "", 0, err
			}
			if strings.HasPrefix(l, "#EXT-X-MAP:") {
				curMap = r
			} else {
				curKey = r
			}
		case strings.HasPrefix(l, "#EXTINF:"):
			v := strings.TrimPrefix(l, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			d, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return "", 0, fmt.Errorf("hls: bad EXTINF %q", l)
			}
			segDur = d
			pending = append(pending, l)
		case l == "#EXT-X-DISCONTINUITY":
			pending = append(pending, l)
		case strings.HasPrefix(l, "#"):
			// Other tags (program date time, Twitch extensions...) are not needed.
		default:
			if segDur < 0 {
				return "", 0, errors.New("hls: segment without EXTINF")
			}
			segStart, segEnd := t, t+segDur
			if segEnd > start && segStart < end {
				u, err := resolveHTTP(base, l)
				if err != nil {
					return "", 0, err
				}
				if kept == 0 {
					firstSeq, first = seq, segStart
					if curKey != "" {
						fmt.Fprintln(&b, curKey)
					}
					if curMap != "" {
						fmt.Fprintln(&b, curMap)
					}
				}
				for _, p := range pending {
					if kept == 0 && p == "#EXT-X-DISCONTINUITY" {
						continue
					}
					fmt.Fprintln(&b, p)
				}
				fmt.Fprintln(&b, u.String())
				kept++
			}
			t, seq, segDur, pending = segEnd, seq+1, -1, nil
		}
		if t >= end && kept > 0 {
			break
		}
	}
	if kept == 0 {
		return "", 0, fmt.Errorf("hls: no segments cover %.1fs-%.1fs (playlist is %.1fs long)", start, start+dur, t)
	}
	var out strings.Builder
	fmt.Fprintln(&out, "#EXTM3U")
	for _, h := range head {
		fmt.Fprintln(&out, h)
	}
	fmt.Fprintln(&out, "#EXT-X-PLAYLIST-TYPE:VOD")
	fmt.Fprintf(&out, "#EXT-X-MEDIA-SEQUENCE:%d\n", firstSeq)
	out.WriteString(b.String())
	fmt.Fprintln(&out, "#EXT-X-ENDLIST")
	return out.String(), first, nil
}

func resolveURIAttr(line string, base *url.URL) (string, error) {
	m := uriAttrRe.FindStringSubmatchIndex(line)
	if m == nil {
		return line, nil
	}
	u, err := resolveHTTP(base, line[m[2]:m[3]])
	if err != nil {
		return "", err
	}
	return line[:m[2]] + u.String() + line[m[3]:], nil
}

// resolveHTTP resolves ref against base and only allows http(s) results, so a
// playlist can never make ffmpeg open local files or other protocols.
func resolveHTTP(base *url.URL, ref string) (*url.URL, error) {
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("hls: bad uri %q", ref)
	}
	u := base.ResolveReference(r)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("hls: refusing non-http uri %q", ref)
	}
	return u, nil
}

func splitLines(s string) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range raw {
		raw[i] = strings.TrimSpace(raw[i])
	}
	return raw
}
