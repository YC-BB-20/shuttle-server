package linkdl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Vimeo (v0.2.91): a video page link, with its password when it has one.
// Shuttle reads the player's settings, the DASH stream list, and fetches the
// picture and sound pieces itself; package mp4 joins them. The behaviour
// follows Shuttle Router's Vimeo downloads, written again here.

// VimeoWeb and VimeoPlayer are the sites asked; tests point them elsewhere.
var VimeoWeb, VimeoPlayer = "https://vimeo.com", "https://player.vimeo.com"

var (
	reVimeoVideo = regexp.MustCompile(`/video/(\d+)`)
	reVimeoTop   = regexp.MustCompile(`^/(\d{5,})`)
	reVimeoAny   = regexp.MustCompile(`/(\d{6,})`)
	reXsrft      = regexp.MustCompile(`"xsrft"\s*:\s*"([^"]+)"`)
	reConfigURL  = regexp.MustCompile(`"config_url"\s*:\s*"([^"]+)"`)
)

// VimeoID reads a Vimeo link: the video's number and its private hash.
func VimeoID(raw string) (id, h string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || len(raw) > 600 {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "vimeo.com" && !strings.HasSuffix(host, ".vimeo.com") {
		return "", ""
	}
	for _, re := range []*regexp.Regexp{reVimeoVideo, reVimeoTop, reVimeoAny} {
		if m := re.FindStringSubmatch(u.Path); m != nil {
			id = m[1]
			break
		}
	}
	if id == "" {
		return "", ""
	}
	if hq := u.Query().Get("h"); regexp.MustCompile(`^[0-9a-fA-F]+$`).MatchString(hq) {
		h = hq
	} else if m := regexp.MustCompile(`/` + id + `/([0-9a-fA-F]{6,})`).FindStringSubmatch(u.Path); m != nil {
		h = m[1]
	}
	return id, h
}

// VimeoSegment is one piece of a stream.
type VimeoSegment struct {
	Start float64 `json:"start"`
	URL   string  `json:"url"`
	Size  int64   `json:"size"`
}

// VimeoStream is one picture or sound stream of the list.
type VimeoStream struct {
	ID             json.RawMessage `json:"id"`
	BaseURL        string          `json:"base_url"`
	Codecs         string          `json:"codecs"`
	Bitrate        float64         `json:"bitrate"`
	AvgBitrate     float64         `json:"avg_bitrate"`
	Height         int             `json:"height"`
	Width          int             `json:"width"`
	Framerate      float64         `json:"framerate"`
	Duration       float64         `json:"duration"`
	SampleRate     float64         `json:"sample_rate"`
	InitSegment    string          `json:"init_segment"`
	InitSegmentURL string          `json:"init_segment_url"`
	Segments       []VimeoSegment  `json:"segments"`
}

// SID is the stream's id as text.
func (s *VimeoStream) SID() string { return strings.Trim(string(s.ID), `"`) }

// Codec names the picture codec in plain words.
func (s *VimeoStream) Codec() string {
	switch c := s.Codecs; {
	case strings.HasPrefix(c, "av01"):
		return "AV1"
	case strings.HasPrefix(c, "hvc1"), strings.HasPrefix(c, "hev1"):
		return "HEVC"
	case strings.HasPrefix(c, "avc"):
		return "H.264"
	default:
		return c
	}
}

// Bytes estimates the stream's size: its pieces' sizes, or rate × length.
func (s *VimeoStream) Bytes(duration float64) int64 {
	var n int64
	all := len(s.Segments) > 0
	for _, g := range s.Segments {
		if g.Size <= 0 {
			all = false
		}
		n += g.Size
	}
	if all {
		return n
	}
	br, du := s.AvgBitrate, s.Duration
	if br == 0 {
		br = s.Bitrate
	}
	if du == 0 {
		du = duration
	}
	return int64(br * du / 8)
}

// Vimeo is an opened video: its title, length and streams, and where its
// pieces are.
type Vimeo struct {
	ID, Title   string
	Duration    float64
	Videos      []*VimeoStream // best first
	Audio       *VimeoStream
	playlistURL string
	plBase      string
	client      *http.Client
}

// Quality is one choice offered in the Add link pop-up.
type Quality struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Bytes  int64  `json:"bytes,omitempty"`
	Height int    `json:"height,omitempty"`
	Ext    string `json:"ext,omitempty"`
}

// VimeoClient is a client with its own cookies (the password login).
func VimeoClient() *http.Client {
	c := Client()
	c.Jar, _ = cookiejar.New(nil)
	return c
}

func get(ctx context.Context, c *http.Client, u, referer string, max int64) (int, []byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("User-Agent", UA)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, max))
	return res.StatusCode, b, res.Request.URL.String(), err
}

// OpenVimeo logs in when a password is given, reads the player's settings
// and the stream list.
func OpenVimeo(ctx context.Context, c *http.Client, raw, password string) (*Vimeo, error) {
	id, h := VimeoID(raw)
	if id == "" {
		return nil, Gone{"This is not a Vimeo video link"}
	}
	if password != "" {
		_, b, _, err := get(ctx, c, VimeoWeb+"/_next/viewer", "", 2<<20)
		if err != nil {
			return nil, Retry{"Vimeo did not answer"}
		}
		m := reXsrft.FindSubmatch(b)
		if m == nil {
			return nil, Retry{"Vimeo did not answer as expected"}
		}
		form := url.Values{"password": {password}, "token": {string(m[1])}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, VimeoWeb+"/"+id+"/password", strings.NewReader(form.Encode()))
		req.Header.Set("User-Agent", UA)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Referer", VimeoWeb+"/"+id)
		res, err := c.Do(req)
		if err != nil {
			return nil, Retry{"Vimeo did not answer"}
		}
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return nil, Gone{"Vimeo refused the password"}
		}
		if res.StatusCode >= 400 {
			return nil, Retry{fmt.Sprintf("Vimeo's password step failed (HTTP %d)", res.StatusCode)}
		}
	}
	ref := VimeoWeb + "/" + id
	cfgURL := VimeoPlayer + "/video/" + id + "/config"
	if h != "" {
		ref += "/" + h
		cfgURL += "?h=" + h
	}
	code, body, _, err := get(ctx, c, cfgURL, ref, 8<<20)
	if err == nil && code != 200 { // the second way: the address the video page itself uses
		if c2, page, _, e2 := get(ctx, c, ref, "", 8<<20); e2 == nil && c2 == 200 {
			if m := reConfigURL.FindSubmatch(page); m != nil {
				cu := strings.NewReplacer(`\/`, "/", `&`, "&").Replace(string(m[1]))
				code, body, _, err = get(ctx, c, cu, ref, 8<<20)
			}
		}
	}
	if err != nil {
		return nil, Retry{"Vimeo did not answer"}
	}
	var cfg struct {
		Message string `json:"message"`
		Video   struct {
			Title    string  `json:"title"`
			Duration float64 `json:"duration"`
		} `json:"video"`
		Request struct {
			Files struct {
				Dash struct {
					DefaultCDN string `json:"default_cdn"`
					CDNs       map[string]struct {
						URL    string `json:"url"`
						AvcURL string `json:"avc_url"`
					} `json:"cdns"`
				} `json:"dash"`
			} `json:"files"`
		} `json:"request"`
	}
	if code != 200 || json.Unmarshal(body, &cfg) != nil {
		if code == 401 || code == 403 {
			if password != "" {
				return nil, Gone{"Vimeo refused the password, or the video is private"}
			}
			return nil, Gone{"This video needs its password"}
		}
		return nil, Gone{fmt.Sprintf("Could not open the video (HTTP %d)", code)}
	}
	d := cfg.Request.Files.Dash
	cdn, ok := d.CDNs[d.DefaultCDN]
	if !ok {
		keys := make([]string, 0, len(d.CDNs))
		for k := range d.CDNs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			cdn = d.CDNs[keys[0]]
		}
	}
	pu := cdn.URL
	if pu == "" {
		pu = cdn.AvcURL
	}
	if pu == "" {
		return nil, Gone{"Vimeo offers no downloadable stream for this video"}
	}
	if (strings.Contains(pu, "master.json") || strings.Contains(pu, "playlist.json")) && !strings.Contains(pu, "base64_init") {
		pu += map[bool]string{true: "&", false: "?"}[strings.Contains(pu, "?")] + "base64_init=1"
	}
	code, body, final, err := get(ctx, c, pu, VimeoWeb+"/", 64<<20)
	if err != nil {
		return nil, Retry{"Could not read the video's stream list"}
	}
	var pl struct {
		BaseURL  string         `json:"base_url"`
		Duration float64        `json:"duration"`
		Video    []*VimeoStream `json:"video"`
		Audio    []*VimeoStream `json:"audio"`
	}
	if code != 200 || json.Unmarshal(body, &pl) != nil {
		return nil, Retry{fmt.Sprintf("Could not read the video's stream list (HTTP %d)", code)}
	}
	v := &Vimeo{ID: id, Title: cfg.Video.Title, Duration: cfg.Video.Duration, playlistURL: final, client: c}
	if v.Duration == 0 {
		v.Duration = pl.Duration
	}
	pdir := final
	if i := strings.IndexByte(pdir, '?'); i >= 0 {
		pdir = pdir[:i]
	}
	pdir = pdir[:strings.LastIndexByte(pdir, '/')+1]
	v.plBase = resolve(pdir, pl.BaseURL)
	for _, s := range pl.Video {
		if len(s.Segments) > 0 && len(s.ID) > 0 {
			v.Videos = append(v.Videos, s)
		}
	}
	sort.SliceStable(v.Videos, func(i, j int) bool {
		a, b := v.Videos[i], v.Videos[j]
		if a.Height != b.Height {
			return a.Height > b.Height
		}
		if (a.Codec() == "AV1") != (b.Codec() == "AV1") {
			return b.Codec() == "AV1"
		}
		return a.Bitrate > b.Bitrate
	})
	score := func(s *VimeoStream) float64 {
		n := s.Bitrate
		if strings.HasPrefix(s.Codecs, "mp4a") {
			n += 1e9
		}
		return n
	}
	for _, a := range pl.Audio {
		if len(a.Segments) > 0 && (v.Audio == nil || score(a) > score(v.Audio)) {
			v.Audio = a
		}
	}
	if len(v.Videos) == 0 {
		return nil, Gone{"No picture stream was found for this video"}
	}
	return v, nil
}

func resolve(base, rel string) string {
	b, err := url.Parse(base)
	if err != nil {
		return rel
	}
	r, err := url.Parse(rel)
	if err != nil {
		return rel
	}
	return b.ResolveReference(r).String()
}

// FileName is the title as a safe file name, ending in .mp4.
func VimeoFileName(title, id string) string {
	if strings.TrimSpace(title) == "" {
		title = "Vimeo " + id
	}
	n := CleanName(title)
	if len(n) > 150 {
		n = strings.TrimRight(trimUTF8(n, 150), " .")
	}
	return n + ".mp4"
}

// Qualities lists the picture streams with the sound added to their size.
func (v *Vimeo) Qualities() []Quality {
	var ab int64
	if v.Audio != nil {
		ab = v.Audio.Bytes(v.Duration)
	}
	var out []Quality
	for _, s := range v.Videos {
		l := fmt.Sprintf("%dp %s", s.Height, s.Codec())
		if s.Framerate > 0 {
			l += fmt.Sprintf(" · %.0f fps", s.Framerate)
		}
		out = append(out, Quality{ID: s.SID(), Label: l, Bytes: s.Bytes(v.Duration) + ab, Height: s.Height, Ext: "mp4"})
	}
	return out
}

// Pick finds the chosen picture stream (by id, then by height), or the best.
func (v *Vimeo) Pick(id string, height int) *VimeoStream {
	for _, s := range v.Videos {
		if s.SID() == id {
			return s
		}
	}
	for _, s := range v.Videos {
		if height > 0 && s.Height == height {
			return s
		}
	}
	if id == "" && height == 0 {
		return v.Videos[0]
	}
	return nil
}

// StreamBase is where a stream's pieces are.
func (v *Vimeo) StreamBase(s *VimeoStream) string { return resolve(v.plBase, s.BaseURL) }

// Query is the stream list's query (some pieces need it).
func (v *Vimeo) Query() string {
	if i := strings.IndexByte(v.playlistURL, '?'); i >= 0 {
		return v.playlistURL[i+1:]
	}
	return ""
}

// Init returns a stream's header bytes.
func (v *Vimeo) Init(ctx context.Context, s *VimeoStream) ([]byte, error) {
	if s.InitSegment != "" { // the list was asked with base64_init=1
		if b, err := base64.StdEncoding.DecodeString(s.InitSegment); err == nil && len(b) > 8 {
			return b, nil
		}
		if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s.InitSegment, "=")); err == nil && len(b) > 8 {
			return b, nil
		}
	}
	u := s.InitSegmentURL
	if u == "" {
		u = s.InitSegment
	}
	if u == "" {
		return nil, errors.New("the stream has no header")
	}
	u = resolve(v.StreamBase(s), u)
	code, b, _, err := get(ctx, v.client, u, "", 16<<20)
	if (err != nil || code != 200) && v.Query() != "" {
		code, b, _, err = get(ctx, v.client, u+map[bool]string{true: "&", false: "?"}[strings.Contains(u, "?")]+v.Query(), "", 16<<20)
	}
	if err != nil || code != 200 || len(b) == 0 {
		return nil, Retry{"Could not get the stream headers from Vimeo"}
	}
	return b, nil
}

// Piece opens one piece. With q, the stream list's query is added.
func (v *Vimeo) Piece(ctx context.Context, s *VimeoStream, i int, q bool) (io.ReadCloser, error) {
	u := resolve(v.StreamBase(s), s.Segments[i].URL)
	if q && !strings.Contains(u, "?") && v.Query() != "" {
		u += "?" + v.Query()
	}
	return Open(ctx, v.client, u, 0)
}
