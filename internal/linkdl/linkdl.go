// Package linkdl is "Add link" for plain download links: any https link to a
// file, such as the per-file links Frame.io's "Download in Browser" gives
// (v0.2.90). The behaviour follows Shuttle Router's "Get from link", written
// again here, not translated. A link may hold a signature, so it is treated
// as a secret: never in a job file, a log line, the page or a command line.
package linkdl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxLinks is how many links one Add takes.
const MaxLinks = 50

// UA is the browser name sent: some sites refuse unknown programs.
const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"

// AllowLocal lets tests use http:// and 127.0.0.1.
var AllowLocal = false

// Gone is a link that will not work again (expired, not a file): adding it
// again with a fresh link is the only way on.
type Gone struct{ Msg string }

func (g Gone) Error() string { return g.Msg }

// Retry is a problem that may pass (no connection, a busy site).
type Retry struct{ Msg string }

func (r Retry) Error() string { return r.Msg }

// ErrNoResume: the site sent the whole file again instead of the rest.
var ErrNoResume = errors.New("the site would not continue the partial file")

// Parse checks a pasted link. It returns the link without its #part, or a
// reason: "bad", "share" (a Frame.io page, not a file), "local", "vimeo",
// "soundcloud".
func Parse(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 || strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", "bad"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || !(u.Scheme == "https" || AllowLocal && u.Scheme == "http") {
		return "", "bad"
	}
	h := strings.ToLower(u.Hostname())
	switch {
	case h == "vimeo.com" || strings.HasSuffix(h, ".vimeo.com"):
		return "", "vimeo"
	case h == "soundcloud.com" || strings.HasSuffix(h, ".soundcloud.com") || h == "on.soundcloud.com":
		return "", "soundcloud"
	case isSharePage(h, u.Path):
		return "", "share"
	case !AllowLocal && isLocal(h):
		return "", "local"
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String(), ""
}

func isSharePage(h, p string) bool {
	if h == "f.io" || h == "www.f.io" {
		return true
	}
	if strings.HasSuffix(h, "frame.io") && !strings.HasPrefix(h, "assets.") {
		return strings.HasPrefix(p, "/share/") || strings.HasPrefix(p, "/reviews/") || strings.HasPrefix(p, "/presentations/") || p == "/" || p == ""
	}
	return false
}

func isLocal(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".lan") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

// Why says in plain words why a link was refused.
func Why(reason string) string {
	switch reason {
	case "share":
		return "That is a Frame.io share page, not a file. Open it in a browser, click Download (or Download All → Download in Browser), cancel the download, and copy the file's link from the browser's Downloads list."
	case "local":
		return "Links to this computer or the home network are not downloaded."
	case "vimeo":
		return "Vimeo links are not supported here yet (Shuttle Router downloads them today). Paste a direct download link instead."
	case "soundcloud":
		return "SoundCloud links are not supported here yet (Shuttle Router downloads them today)."
	}
	return "Paste a download link that starts with https://"
}

// Host is the link's site, the only part of a link ever shown or logged.
func Host(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// fragment reads key=value after # (the Frame.io links bookmark adds
// rq-path and rq-share there; that part never reaches the site).
func fragment(raw, key string) string {
	i := strings.IndexByte(raw, '#')
	if i < 0 {
		return ""
	}
	for _, kv := range strings.FieldsFunc(raw[i+1:], func(r rune) bool { return r == '&' || r == '#' }) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			if d, err := url.PathUnescape(v); err == nil {
				return d
			}
			return v
		}
	}
	return ""
}

// SubFolder is the folder inside the download folder carried after the link
// as #rq-path=A/B, each part cleaned like a file name; "" when absent.
func SubFolder(raw string) string {
	var parts []string
	for _, seg := range strings.FieldsFunc(fragment(raw, "rq-path"), func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.Trim(seg, " .") == "" {
			continue // "." and ".." dropped
		}
		parts = append(parts, CleanName(seg))
		if len(parts) == 8 {
			break
		}
	}
	return strings.Join(parts, "/")
}

// ShareName is the name of the share the link came from (#rq-share=…).
func ShareName(raw string) string {
	s := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, fragment(raw, "rq-share"))), " ")
	if len(s) > 120 {
		s = trimUTF8(s, 120)
	}
	return s
}

// CleanName makes a safe file name: no characters NTFS or Kodi refuse, no
// path, at most about 200 bytes, never empty.
func CleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " .")
	if ext := path.Ext(s); ext != "" && strings.HasSuffix(strings.TrimSuffix(s, ext), " ") {
		s = strings.TrimRight(strings.TrimSuffix(s, ext), " ") + ext
	}
	if len(s) > 200 {
		ext := path.Ext(s)
		if len(ext) > 6 {
			ext = ""
		}
		s = trimUTF8(s[:len(s)-len(ext)], 200-len(ext)) + ext
	}
	if s == "" {
		s = "download"
	}
	return s
}

func trimUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// NameFrom picks the file name: Content-Disposition (filename* first), then a
// response-content-disposition in the link (Frame.io, S3), then the link's
// path.
func NameFrom(h http.Header, link string) string {
	fromCD := func(cd string) string {
		if cd == "" {
			return ""
		}
		if _, p, err := mime.ParseMediaType(cd); err == nil && p["filename"] != "" {
			return p["filename"] // mime decodes filename* into filename
		}
		return ""
	}
	n := fromCD(h.Get("Content-Disposition"))
	u, _ := url.Parse(link)
	if n == "" && u != nil {
		n = fromCD(u.Query().Get("response-content-disposition"))
	}
	if n == "" && u != nil {
		n = path.Base(u.Path)
		if n == "/" || n == "." {
			n = ""
		}
	}
	return CleanName(n)
}

// Client is the one HTTP client for links: pooled, at most 8 redirects, a
// timeout on connecting and on each answer's headers. A download's body has
// no overall timeout; a stall is caught by the reader instead.
func Client() *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext,
		TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 40 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 90 * time.Second}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

// Info is what a look-up found. No link in it.
type Info struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"` // -1 when the site does not say
	Type      string `json:"type,omitempty"`
	Resumable bool   `json:"resumable"`
	Host      string `json:"host"`
	Sub       string `json:"sub,omitempty"`
	Share     string `json:"share,omitempty"`
}

// Probe looks a link up without downloading it: a GET of one byte (signed
// links often refuse HEAD).
func Probe(ctx context.Context, c *http.Client, raw, link string) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return Info{}, Gone{"Paste a download link that starts with https://"}
	}
	req.Header.Set("User-Agent", UA)
	req.Header.Set("Range", "bytes=0-0")
	res, err := c.Do(req)
	if err != nil {
		return Info{}, Retry{"No answer from " + Host(link) + " (check the link and the internet connection)"}
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	if err := statusErr(res.StatusCode, link); err != nil {
		return Info{}, err
	}
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "text/html") {
		return Info{}, Gone{"This link opens a web page, not a file. Copy the link of the download itself."}
	}
	in := Info{Name: NameFrom(res.Header, res.Request.URL.String()), Size: -1, Resumable: res.StatusCode == http.StatusPartialContent,
		Host: Host(link), Sub: SubFolder(raw), Share: ShareName(raw)}
	if t, _, err := mime.ParseMediaType(ct); err == nil {
		in.Type = t
	}
	if in.Name == "download" { // the first link's name, before redirects, may be better
		if n := NameFrom(http.Header{}, link); n != "download" {
			in.Name = n
		}
	}
	if res.StatusCode == http.StatusPartialContent {
		if _, tot, ok := strings.Cut(res.Header.Get("Content-Range"), "/"); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(tot), 10, 64); err == nil {
				in.Size = n
			}
		}
	} else if res.ContentLength >= 0 {
		in.Size = res.ContentLength
	}
	return in, nil
}

func statusErr(code int, link string) error {
	switch {
	case code == 401 || code == 403 || code == 404 || code == 410:
		return Gone{fmt.Sprintf("The link has expired or is not valid (HTTP %d). Copy a fresh link and add it again.", code)}
	case code >= 500 || code == 429:
		return Retry{fmt.Sprintf("%s is busy or having trouble (HTTP %d)", Host(link), code)}
	case code != 200 && code != 206:
		return Gone{fmt.Sprintf("%s answered HTTP %d", Host(link), code)}
	}
	return nil
}

// Open starts the download of link from byte from. The answer's body is the
// rest of the file (from > 0 and 206) or the whole file (from == 0, 200).
// A site that answers 200 to a request for the rest gives ErrNoResume.
func Open(ctx context.Context, c *http.Client, link string, from int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, Gone{"The saved link cannot be read"}
	}
	req.Header.Set("User-Agent", UA)
	if from > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-")
	}
	res, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, Retry{"No connection to " + Host(link)}
	}
	if from > 0 && res.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		res.Body.Close()
		return io.NopCloser(strings.NewReader("")), nil // the part was already whole
	}
	if err := statusErr(res.StatusCode, link); err != nil {
		res.Body.Close()
		return nil, err
	}
	if from > 0 && res.StatusCode == http.StatusOK {
		res.Body.Close()
		return nil, ErrNoResume
	}
	return res.Body, nil
}
