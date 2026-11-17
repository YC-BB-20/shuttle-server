package linkdl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SoundCloud (v0.2.91): one track's link. Shuttle reads the track through
// SoundCloud's own web API with the public key its site uses, then fetches
// the chosen stream (MP3 or AAC) or the original upload when the artist
// allows it, and tags the file. The behaviour follows Shuttle Router's
// SoundCloud downloads, written again here.

// SCWeb and SCAPI are the sites asked; tests point them elsewhere.
var SCWeb, SCAPI = "https://soundcloud.com", "https://api-v2.soundcloud.com"

var reserved1 = map[string]bool{"discover": true, "search": true, "stream": true, "you": true, "upload": true, "pages": true, "charts": true,
	"popular": true, "feed": true, "settings": true, "notifications": true, "messages": true, "jobs": true, "imprint": true, "mobile": true, "people": true, "tags": true}
var reserved2 = map[string]bool{"likes": true, "reposts": true, "albums": true, "tracks": true, "popular-tracks": true, "following": true,
	"followers": true, "comments": true, "spotlight": true}
var reSeg = regexp.MustCompile(`^[\w\-.~%]+$`)

// SCLink reads a SoundCloud link: the track's address, or a reason
// ("set" for a playlist or album, "bad").
func SCLink(raw string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || len(raw) > 600 {
		return "", "bad"
	}
	host := strings.ToLower(u.Hostname())
	segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	for _, s := range segs {
		if !reSeg.MatchString(s) {
			return "", "bad"
		}
	}
	if host == "on.soundcloud.com" {
		if len(segs) == 1 {
			return "https://on.soundcloud.com/" + segs[0], ""
		}
		return "", "bad"
	}
	if host != "soundcloud.com" && host != "www.soundcloud.com" && host != "m.soundcloud.com" {
		return "", "bad"
	}
	if len(segs) < 2 || reserved1[strings.ToLower(segs[0])] || reserved2[strings.ToLower(segs[1])] {
		return "", "bad"
	}
	if strings.EqualFold(segs[1], "sets") || len(segs) > 2 && strings.EqualFold(segs[2], "sets") {
		return "", "set"
	}
	if len(segs) == 2 {
		return SCWeb + "/" + segs[0] + "/" + segs[1], ""
	}
	if len(segs) == 3 && regexp.MustCompile(`^s-\w+$`).MatchString(segs[2]) {
		return SCWeb + "/" + strings.Join(segs, "/"), ""
	}
	return "", "bad"
}

var scKey struct {
	sync.Mutex
	id string
	at time.Time
}

var (
	reScript   = regexp.MustCompile(`<script[^>]*?src="([^"]+\.js)"`)
	reClientID = regexp.MustCompile(`client_id\s*:\s*"([0-9a-zA-Z]{32})"`)
)

// clientID is the public web key SoundCloud's own site uses, kept 12 hours.
func clientID(ctx context.Context, c *http.Client, fresh bool) (string, error) {
	scKey.Lock()
	defer scKey.Unlock()
	if !fresh && scKey.id != "" && time.Since(scKey.at) < 12*time.Hour {
		return scKey.id, nil
	}
	code, page, _, err := get(ctx, c, SCWeb+"/", "", 4<<20)
	if err != nil || code != 200 {
		return "", Retry{"Cannot reach soundcloud.com"}
	}
	ms := reScript.FindAllSubmatch(page, -1)
	for i := len(ms) - 1; i >= 0; i-- {
		src := string(ms[i][1])
		if !strings.HasPrefix(src, "https://a-v2.sndcdn.com/") && !strings.HasPrefix(src, SCWeb) {
			continue
		}
		if code, js, _, err := get(ctx, c, src, "", 12<<20); err == nil && code == 200 {
			if m := reClientID.FindSubmatch(js); m != nil {
				scKey.id, scKey.at = string(m[1]), time.Now()
				return scKey.id, nil
			}
		}
	}
	return "", Retry{"Could not read SoundCloud's public web key (their site may have changed)"}
}

// SCTrack is a track as SoundCloud's API describes it.
type SCTrack struct {
	ID                 int64   `json:"id"`
	Kind               string  `json:"kind"`
	Title              string  `json:"title"`
	Policy             string  `json:"policy"`
	Genre              string  `json:"genre"`
	Description        string  `json:"description"`
	PermalinkURL       string  `json:"permalink_url"`
	ArtworkURL         string  `json:"artwork_url"`
	Duration           float64 `json:"duration"`
	ReleaseDate        string  `json:"release_date"`
	DisplayDate        string  `json:"display_date"`
	CreatedAt          string  `json:"created_at"`
	Downloadable       bool    `json:"downloadable"`
	HasDownloadsLeft   *bool   `json:"has_downloads_left"`
	OriginalFormat     string  `json:"original_format"`
	OriginalSize       int64   `json:"original_content_size"`
	TrackAuthorization string  `json:"track_authorization"`
	User               struct {
		Username  string `json:"username"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
	Publisher struct {
		Artist     string `json:"artist"`
		AlbumTitle string `json:"album_title"`
	} `json:"publisher_metadata"`
	Media struct {
		Transcodings []struct {
			URL    string `json:"url"`
			Preset string `json:"preset"`
			Format struct {
				Protocol string `json:"protocol"`
				MimeType string `json:"mime_type"`
			} `json:"format"`
		} `json:"transcodings"`
	} `json:"media"`
}

// SC is an opened track.
type SC struct {
	T      SCTrack
	cid    string
	client *http.Client
}

func (s *SC) api(ctx context.Context, u, extra string, out any) error {
	for try := 0; try < 2; try++ {
		q := u + map[bool]string{true: "&", false: "?"}[strings.Contains(u, "?")] + "client_id=" + s.cid + extra
		code, b, _, err := get(ctx, s.client, q, "", 8<<20)
		switch {
		case err != nil:
			return Retry{"SoundCloud did not answer"}
		case (code == 401 || code == 403) && try == 0:
			cid, err := clientID(ctx, s.client, true)
			if err != nil {
				return Gone{fmt.Sprintf("SoundCloud refused the request (HTTP %d)", code)}
			}
			s.cid = cid
			continue
		case code == 200:
			if json.Unmarshal(b, out) != nil {
				return Retry{"SoundCloud sent an unreadable answer"}
			}
			return nil
		case code == 404:
			return Gone{"SoundCloud does not know this track (removed, or private?)"}
		case code == 401 || code == 403:
			return Gone{fmt.Sprintf("SoundCloud refused the request (HTTP %d)", code)}
		default:
			return Retry{fmt.Sprintf("SoundCloud did not answer (HTTP %d)", code)}
		}
	}
	return Retry{"SoundCloud did not answer"}
}

// OpenSC finds the track a link points to.
func OpenSC(ctx context.Context, c *http.Client, link string) (*SC, error) {
	cid, err := clientID(ctx, c, false)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(link, "https://on.soundcloud.com/") {
		_, _, final, err := get(ctx, c, link, "", 3<<20)
		l2, _ := SCLink(final)
		if err != nil || l2 == "" {
			return nil, Gone{"Cannot open this short SoundCloud link"}
		}
		link = l2
	}
	s := &SC{cid: cid, client: c}
	if err := s.api(ctx, SCAPI+"/resolve?url="+url.QueryEscape(link), "", &s.T); err != nil {
		return nil, err
	}
	switch {
	case s.T.Kind == "playlist":
		return nil, Gone{"This is a playlist or album. Paste the link of one track."}
	case s.T.Kind != "" && s.T.Kind != "track":
		return nil, Gone{"This link is not a track."}
	case strings.EqualFold(s.T.Policy, "SNIP"):
		return nil, Gone{"SoundCloud offers only a 30-second preview of this track here, so it is not downloaded."}
	case strings.EqualFold(s.T.Policy, "BLOCK"):
		return nil, Gone{"SoundCloud blocks this track (not available to listeners here)."}
	}
	return s, nil
}

// Artist is who the track is by.
func (s *SC) Artist() string {
	if s.T.Publisher.Artist != "" {
		return s.T.Publisher.Artist
	}
	return s.T.User.Username
}

// Date is the release date (YYYY-MM-DD or YYYY), when known.
func (s *SC) Date() string {
	for _, d := range []string{s.T.ReleaseDate, s.T.DisplayDate, s.T.CreatedAt} {
		if len(d) >= 10 && d[4] == '-' {
			return d[:10]
		}
		if len(d) >= 4 {
			return d[:4]
		}
	}
	return ""
}

// Artwork is the cover picture's address at 500×500, when there is one.
func (s *SC) Artwork() string {
	a := s.T.ArtworkURL
	if a == "" {
		a = s.T.User.AvatarURL
	}
	a = strings.Replace(a, "-large.", "-t500x500.", 1)
	return regexp.MustCompile(`-t\d+x\d+\.`).ReplaceAllString(a, "-t500x500.")
}

// SCFileName is "Artist - Title.ext" made safe.
func SCFileName(artist, title, ext string) string {
	a, t := CleanName(artist), CleanName(title)
	if artist == "" {
		a = ""
	}
	if title == "" {
		t = ""
	}
	b := t
	switch {
	case a != "" && t != "":
		b = a + " - " + t
	case t == "":
		b = a
	}
	if b == "" {
		b = "SoundCloud track"
	}
	if len(b) > 150 {
		b = strings.TrimRight(trimUTF8(b, 150), " .")
	}
	ext = strings.ToLower(regexp.MustCompile(`\W`).ReplaceAllString(ext, ""))
	if ext == "" {
		ext = "mp3"
	}
	return b + "." + ext
}

// SCQuality is one way to get the track.
type SCQuality struct {
	Quality
	Codec, Proto string
	tc           int // index in the transcodings
	kbps         int
}

// Qualities: the original upload first when allowed, then the streams, best
// first (AAC counts a little higher than MP3 at the same rate).
func (s *SC) Qualities() []SCQuality {
	var out []SCQuality
	have := map[string]bool{}
	for _, want := range []string{"progressive", "hls"} {
		for i, tc := range s.T.Media.Transcodings {
			if tc.Format.Protocol != want || tc.URL == "" {
				continue
			}
			q := SCQuality{Proto: want, tc: i}
			switch {
			case strings.HasPrefix(tc.Preset, "mp3") || strings.Contains(tc.Format.MimeType, "audio/mpeg"):
				q.Codec, q.Ext, q.kbps = "mp3", "mp3", 128
			case strings.HasPrefix(tc.Preset, "aac") || strings.Contains(tc.Format.MimeType, "audio/mp4"):
				if want != "hls" {
					continue
				}
				q.Codec, q.Ext, q.kbps = "aac", "m4a", 160
				if m := regexp.MustCompile(`aac_(\d+)k`).FindStringSubmatch(tc.Preset); m != nil {
					fmt.Sscan(m[1], &q.kbps)
				}
			default:
				continue
			}
			key := fmt.Sprintf("%s-%d", q.Codec, q.kbps)
			if have[key] {
				continue
			}
			have[key] = true
			q.ID = key + "-" + want
			q.Label = fmt.Sprintf("%s %d kbps (.%s)", strings.ToUpper(q.Codec), q.kbps, q.Ext)
			if q.Codec == "aac" {
				q.Label = fmt.Sprintf("AAC %d kbps (.m4a)", q.kbps)
			}
			q.Bytes = int64(s.T.Duration / 1000 * float64(q.kbps) * 125)
			out = append(out, q)
		}
	}
	rank := func(q SCQuality) float64 {
		r := float64(q.kbps)
		if q.Codec == "aac" {
			r *= 1.2
		}
		return r
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) > rank(out[j]) })
	if s.T.Downloadable && (s.T.HasDownloadsLeft == nil || *s.T.HasDownloadsLeft) {
		of := strings.ToLower(s.T.OriginalFormat)
		if !regexp.MustCompile(`^\w{2,5}$`).MatchString(of) {
			of = ""
		}
		l := "Original file as uploaded"
		if of != "" {
			l += " (." + of + ")"
		}
		out = append([]SCQuality{{Quality: Quality{ID: "original", Label: l, Bytes: s.T.OriginalSize, Ext: of}, Codec: "original", Proto: "original"}}, out...)
	}
	return out
}

// StreamURL is the address of a quality's file or stream list.
func (s *SC) StreamURL(ctx context.Context, q SCQuality) (string, error) {
	if q.Proto == "original" {
		var j struct {
			RedirectURI string `json:"redirectUri"`
		}
		if err := s.api(ctx, SCAPI+fmt.Sprintf("/tracks/%d/download", s.T.ID), "", &j); err != nil {
			return "", err
		}
		if j.RedirectURI == "" {
			return "", Gone{"SoundCloud gave no download address"}
		}
		return j.RedirectURI, nil
	}
	auth := ""
	if s.T.TrackAuthorization != "" {
		auth = "&track_authorization=" + url.QueryEscape(s.T.TrackAuthorization)
	}
	var j struct {
		URL string `json:"url"`
	}
	if err := s.api(ctx, s.T.Media.Transcodings[q.tc].URL, auth, &j); err != nil {
		return "", err
	}
	if j.URL == "" {
		return "", Gone{"SoundCloud gave no stream address"}
	}
	return j.URL, nil
}

// M3U8 reads an HLS list: its header address (EXT-X-MAP) and its pieces.
func M3U8(text, base string) (init string, segs []string) {
	abs := func(u string) string { return resolve(base, u) }
	re := regexp.MustCompile(`^#EXT-X-MAP:.*?URI="([^"]+)"`)
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			init = abs(m[1])
		} else if line != "" && !strings.HasPrefix(line, "#") {
			segs = append(segs, abs(line))
		}
	}
	return init, segs
}

// Get fetches a small file (a list, a header, a cover) with the track's client.
func (s *SC) Get(ctx context.Context, u string, max int64) ([]byte, error) {
	code, b, _, err := get(ctx, s.client, u, "", max)
	if err != nil {
		return nil, Retry{"SoundCloud's file server did not answer"}
	}
	if code != 200 {
		return nil, statusErr(code, u)
	}
	return b, nil
}
