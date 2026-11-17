package copyjob

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/linkdl"
	"github.com/YC-BB-20/shuttle-server/internal/mp4"
)

func mp4Piece(t *testing.T, name string) []byte {
	b, err := os.ReadFile(filepath.Join("..", "mp4", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// topBoxes lists a file's top-level boxes.
func topBoxes(b []byte) string {
	var t []string
	for _, x := range mp4.Boxes(b) {
		t = append(t, x.Type)
	}
	return strings.Join(t, " ")
}

func playable(t *testing.T, p string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", p, "-f", "null", "-").CombinedOutput(); err != nil || len(bytes.TrimSpace(out)) > 0 {
		t.Fatalf("ffmpeg cannot play %s: %v %s", filepath.Base(p), err, out)
	}
}

// v0.2.91: a Vimeo video with a password is opened, its chosen quality is
// downloaded piece by piece (a piece that keeps failing makes the job wait,
// then it continues from the pieces kept), joined into one ordinary MP4, read
// back and named. The password and the link stay out of the job file.
func TestVimeoDownload(t *testing.T) {
	linkdl.AllowLocal = true
	defer func() { linkdl.AllowLocal = false }()
	oldWait := siteWait
	siteWait = 10 * time.Millisecond
	defer func() { siteWait = oldWait }()
	var mu sync.Mutex
	hits := map[string]int{}
	failFor := 7 // the third piece fails more often than one run tries
	seg := func(names []string, starts []float64) []map[string]any {
		var o []map[string]any
		for i, n := range names {
			o = append(o, map[string]any{"start": starts[i], "url": n, "size": len(mp4Piece(t, n))})
		}
		return o
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		n := hits[r.URL.Path]
		mu.Unlock()
		switch {
		case r.URL.Path == "/_next/viewer":
			w.Write([]byte(`{"xsrft":"TOKEN1"}`))
		case r.URL.Path == "/76543210/password":
			r.ParseForm()
			if r.PostForm.Get("password") != "open sesame" || r.PostForm.Get("token") != "TOKEN1" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "vuid", Value: "in", Path: "/"})
		case r.URL.Path == "/video/76543210/config":
			if c, err := r.Cookie("vuid"); err != nil || c.Value != "in" {
				http.Error(w, "private", http.StatusForbidden)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"video": map[string]any{"title": "Day 2: Interview / take 3", "duration": 6},
				"request": map[string]any{"files": map[string]any{"dash": map[string]any{"default_cdn": "fastly",
					"cdns": map[string]any{"fastly": map[string]any{"url": srv.URL + "/exp/abc/playlist.json?sig=PLSECRET"}}}}}})
		case r.URL.Path == "/exp/abc/playlist.json":
			if r.URL.Query().Get("base64_init") != "1" {
				http.Error(w, "no", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"base_url": "../", "video": []any{
				map[string]any{"id": "v-low", "base_url": "low/", "codecs": "avc1.4d401e", "height": 180, "framerate": 25, "bitrate": 1e5, "duration": 6,
					"init_segment": base64.StdEncoding.EncodeToString(mp4Piece(t, "init-0.m4s")), "segments": seg([]string{"chunk-0-1.m4s"}, []float64{0})},
				map[string]any{"id": "v-hd", "base_url": "hd/", "codecs": "avc1.640028", "height": 360, "framerate": 25, "bitrate": 4e5, "duration": 6,
					"init_segment": base64.StdEncoding.EncodeToString(mp4Piece(t, "init-0.m4s")),
					"segments":     seg([]string{"chunk-0-1.m4s", "chunk-0-2.m4s", "chunk-0-3.m4s"}, []float64{0, 2, 4})},
			}, "audio": []any{
				map[string]any{"id": "a-1", "base_url": "snd/", "codecs": "mp4a.40.2", "bitrate": 128000, "sample_rate": 48000, "duration": 6,
					"init_segment": base64.StdEncoding.EncodeToString(mp4Piece(t, "init-1.m4s")),
					"segments":     seg([]string{"chunk-1-1.m4s", "chunk-1-2.m4s", "chunk-1-3.m4s", "chunk-1-4.m4s"}, []float64{0, 1.5, 3, 4.5})},
			}})
		case strings.HasPrefix(r.URL.Path, "/exp/hd/") || strings.HasPrefix(r.URL.Path, "/exp/snd/"):
			if r.URL.Path == "/exp/hd/chunk-0-2.m4s" && n <= failFor {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			w.Write(mp4Piece(t, filepath.Base(r.URL.Path)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldWeb, oldPlayer := linkdl.VimeoWeb, linkdl.VimeoPlayer
	linkdl.VimeoWeb, linkdl.VimeoPlayer = srv.URL, srv.URL
	defer func() { linkdl.VimeoWeb, linkdl.VimeoPlayer = oldWeb, oldPlayer }()

	dest, state := t.TempDir(), t.TempDir()
	m := newManager(t, state)
	m.Secrets = filepath.Join(t.TempDir(), "links")
	link := "https://vimeo.com/76543210"

	// Without the password the look-up says so; with it, the qualities come.
	if in := m.LookUpLinks(context.Background(), []string{link}, ""); len(in) != 1 || !strings.Contains(in[0].Error, "password") {
		t.Fatalf("no password: %+v", in)
	}
	in := m.LookUpLinks(context.Background(), []string{link}, "open sesame")
	if len(in) != 1 || in[0].Error != "" || in[0].Site != "vimeo" || len(in[0].Qualities) != 2 || in[0].Qualities[0].Height != 360 || in[0].Name != "Day 2- Interview - take 3.mp4" && !strings.HasSuffix(in[0].Name, ".mp4") {
		t.Fatalf("look-up %+v", in)
	}
	if b, _ := json.Marshal(in); bytes.Contains(b, []byte("PLSECRET")) || bytes.Contains(b, []byte("open sesame")) {
		t.Fatal("the look-up answer holds a secret")
	}
	j, err := m.AddLink(context.Background(), link, dest, LinkOpts{Password: "open sesame", Quality: "v-hd"})
	if err != nil {
		t.Fatal(err)
	}
	if j.st.Spec.Link.Site != "vimeo" || j.st.Spec.Link.Quality != "v-hd" || j.st.Spec.Link.Est <= 0 {
		t.Fatalf("spec %+v", j.st.Spec.Link)
	}
	startRun(t, m)
	v := waitState(t, m, j.st.ID, "finished")
	if v.Error != "" || v.LinkHash == "" {
		t.Fatalf("finished with %q", v.Error)
	}
	mu.Lock()
	first := hits["/exp/hd/chunk-0-1.m4s"]
	mu.Unlock()
	if first != 1 {
		t.Fatalf("the first piece was fetched %d times: the pieces kept were not continued from", first)
	}
	b, err := os.ReadFile(v.LinkFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := topBoxes(b); got != "ftyp moov free mdat" {
		t.Fatalf("layout %q", got)
	}
	playable(t, v.LinkFile)
	if _, err := os.Stat(j.path("site.json")); err == nil {
		t.Fatal("the download's progress was left in the job folder")
	}
	if _, err := os.Stat(m.linkPath(j.st.ID)); err == nil {
		t.Fatal("the saved link and password were not removed")
	}
	jb, _ := os.ReadFile(j.path("job.json"))
	if bytes.Contains(jb, []byte("open sesame")) || bytes.Contains(jb, []byte("PLSECRET")) {
		t.Fatal("a secret reached the job file")
	}
}

// v0.2.91: a SoundCloud track: the AAC stream is joined into an M4A with its
// tags and cover; the MP3 gets an ID3 tag in front; a playlist is refused.
func TestSoundCloudDownload(t *testing.T) {
	linkdl.AllowLocal = true
	defer func() { linkdl.AllowLocal = false }()
	var pb bytes.Buffer
	png.Encode(&pb, image.NewGray(image.Rect(0, 0, 4, 4)))
	cover := pb.Bytes()
	mp3 := append([]byte{0xff, 0xfb, 0x90, 0x64}, bytes.Repeat([]byte{7}, 5000)...)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := "0123456789abcdefABCDEF0123456789"
		api := func() bool {
			if r.URL.Query().Get("client_id") != key {
				http.Error(w, "no", http.StatusUnauthorized)
				return false
			}
			return true
		}
		switch p := r.URL.Path; {
		case p == "/":
			fmt.Fprintf(w, `<html><script crossorigin src="%s/assets/app-1.js"></script></html>`, srv.URL)
		case p == "/assets/app-1.js":
			fmt.Fprintf(w, `x={client_id:"%s",y:1}`, key)
		case p == "/resolve":
			if !api() {
				return
			}
			if strings.Contains(r.URL.Query().Get("url"), "/sets/") {
				json.NewEncoder(w).Encode(map[string]any{"kind": "playlist"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": 42, "kind": "track", "title": "Night Drive", "policy": "ALLOW", "genre": "Ambient",
				"permalink_url": "https://soundcloud.com/someone/night-drive", "artwork_url": srv.URL + "/art-large.png", "duration": 6000,
				"display_date": "2026-03-01T00:00:00Z", "user": map[string]any{"username": "Some One"},
				"media": map[string]any{"transcodings": []any{
					map[string]any{"url": srv.URL + "/media/aac", "preset": "aac_160k", "format": map[string]any{"protocol": "hls", "mime_type": "audio/mp4; codecs=\"mp4a.40.2\""}},
					map[string]any{"url": srv.URL + "/media/mp3", "preset": "mp3_1_0", "format": map[string]any{"protocol": "progressive", "mime_type": "audio/mpeg"}},
				}}})
		case p == "/media/aac":
			if api() {
				json.NewEncoder(w).Encode(map[string]string{"url": srv.URL + "/hls/aac/list.m3u8?Policy=XYZ"})
			}
		case p == "/media/mp3":
			if api() {
				json.NewEncoder(w).Encode(map[string]string{"url": srv.URL + "/file/track.mp3?Policy=XYZ"})
			}
		case p == "/hls/aac/list.m3u8":
			w.Write([]byte("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:1.5,\nchunk-1-1.m4s\n#EXTINF:1.5,\nchunk-1-2.m4s\n#EXTINF:1.5,\nchunk-1-3.m4s\n#EXTINF:1.5,\nchunk-1-4.m4s\n#EXT-X-ENDLIST\n"))
		case p == "/hls/aac/init.mp4":
			w.Write(mp4Piece(t, "init-1.m4s"))
		case strings.HasPrefix(p, "/hls/aac/chunk"):
			w.Write(mp4Piece(t, filepath.Base(p)))
		case p == "/file/track.mp3":
			w.Write(mp3)
		case p == "/art-t500x500.png":
			w.Write(cover)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldWeb, oldAPI := linkdl.SCWeb, linkdl.SCAPI
	linkdl.SCWeb, linkdl.SCAPI = srv.URL, srv.URL
	defer func() { linkdl.SCWeb, linkdl.SCAPI = oldWeb, oldAPI }()

	dest, state := t.TempDir(), t.TempDir()
	m := newManager(t, state)
	m.Secrets = filepath.Join(t.TempDir(), "links")
	link := "https://soundcloud.com/someone/night-drive"
	in := m.LookUpLinks(context.Background(), []string{link, "https://soundcloud.com/someone/sets/album"}, "")
	if len(in) != 2 || in[0].Error != "" || in[0].Site != "soundcloud" || len(in[0].Qualities) != 2 || in[0].Qualities[0].Ext != "m4a" {
		t.Fatalf("look-up %+v", in)
	}
	if !strings.Contains(in[1].Error, "playlist") {
		t.Fatalf("a playlist: %+v", in[1])
	}
	ja, err := m.AddLink(context.Background(), link, dest, LinkOpts{})
	if err != nil {
		t.Fatal(err)
	}
	jm, err := m.AddLink(context.Background(), link, dest, LinkOpts{Quality: in[0].Qualities[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if ja.st.Spec.Link.Name != "Some One - Night Drive.m4a" || jm.st.Spec.Link.Name != "Some One - Night Drive.mp3" {
		t.Fatalf("names %q %q", ja.st.Spec.Link.Name, jm.st.Spec.Link.Name)
	}
	startRun(t, m)
	va := waitState(t, m, ja.st.ID, "finished")
	vm := waitState(t, m, jm.st.ID, "finished")
	if va.Error != "" || vm.Error != "" || va.LinkHash == "" || vm.LinkHash == "" {
		t.Fatalf("finished with %q / %q", va.Error, vm.Error)
	}
	a, _ := os.ReadFile(va.LinkFile)
	if got := topBoxes(a); got != "ftyp moov free mdat" {
		t.Fatalf("m4a layout %q", got)
	}
	if !bytes.Contains(a, []byte("Night Drive")) || !bytes.Contains(a, []byte("Some One")) || !bytes.Contains(a, cover) {
		t.Fatal("the m4a has no tags or cover")
	}
	playable(t, va.LinkFile)
	b, _ := os.ReadFile(vm.LinkFile)
	if !bytes.HasPrefix(b, []byte("ID3")) || !bytes.HasSuffix(b, mp3) || !bytes.Contains(b, []byte("Night Drive")) {
		t.Fatal("the mp3 has no ID3 tag in front of the sound")
	}
}
