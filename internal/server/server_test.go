package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/events"
)

func newTest() (*Server, *events.Hub) {
	h := events.NewHub(10, 2)
	return &Server{Page: []byte("<p>page</p>"), Hub: h,
		Status: func() any { return map[string]string{"version": "0.1.0"} },
		About:  func() any { return "about" }}, h
}

func TestPageAndStatus(t *testing.T) {
	s, _ := newTest()
	ts := httptest.NewServer(s.HTTP().Handler)
	defer ts.Close()
	for path, want := range map[string]string{"/": "page", "/api/status": `"version":"0.1.0"`} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		buf := new(strings.Builder)
		bufio.NewReader(res.Body).WriteTo(buf)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(buf.String(), want) {
			t.Errorf("%s: %d %q", path, res.StatusCode, buf)
		}
	}
}

func TestRefusesOtherHosts(t *testing.T) {
	s, _ := newTest()
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Host = "evil.example:8470"
	rec := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestEventsStream(t *testing.T) {
	s, h := newTest()
	ts := httptest.NewServer(s.HTTP().Handler)
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	h.Publish([]byte(`{"n":1}`))
	go func() {
		for i := 0; i < 20; i++ {
			h.Tick()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if sc.Text() == `data: {"n":1}` {
			return
		}
	}
	t.Fatal("no event received")
}

func TestCommandsNeedTheShuttleHeader(t *testing.T) {
	s, _ := newTest()
	h := s.HTTP().Handler
	for _, c := range []struct {
		hdr, origin string
		want        int
	}{{"", "", http.StatusForbidden}, {"1", "https://evil.example", http.StatusForbidden}, {"1", "http://localhost:8470", http.StatusConflict}} {
		req := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(`{}`))
		req.Host = "localhost:8470"
		if c.hdr != "" {
			req.Header.Set("X-Shuttle", c.hdr)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want { // StatusConflict: accepted, but no job manager in this test (safe mode)
			t.Errorf("%+v: got %d", c, rec.Code)
		}
	}
}

func TestBrowseListsFolders(t *testing.T) {
	s, _ := newTest()
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Dir(dir))
	os.MkdirAll(dir+"/Card A", 0o700)
	os.MkdirAll(dir+"/.hidden", 0o700)
	req := httptest.NewRequest("GET", "/api/browse?path="+url.QueryEscape(dir), nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "Card A") || strings.Contains(rec.Body.String(), ".hidden") {
		t.Fatal(rec.Body.String())
	}
}

func TestBrowseFilesCrumbsAndPlaces(t *testing.T) {
	s, _ := newTest()
	dir := t.TempDir()
	home := filepath.Dir(dir)
	t.Setenv("HOME", home)
	os.MkdirAll(dir+"/Card A", 0o700)
	os.WriteFile(dir+"/clip.mov", []byte("1234"), 0o600)
	getAt := func(at, q string) map[string]any {
		req := httptest.NewRequest("GET", "/api/browse?path="+url.QueryEscape(at)+q, nil)
		req.Host = "localhost"
		rec := httptest.NewRecorder()
		s.HTTP().Handler.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	get := func(q string) map[string]any { return getAt(dir, q) }
	if strings.Contains(fmt.Sprint(get("")["folders"]), "clip.mov") {
		t.Fatal("files listed without being asked for")
	}
	d := get("&files=1")
	if !strings.Contains(fmt.Sprint(d["folders"]), "clip.mov") {
		t.Fatal("file not listed")
	}
	cr := d["crumbs"].([]any)
	last := cr[len(cr)-1].(map[string]any)
	if cr[0].(map[string]any)["path"] != home || last["path"] != dir || last["name"] != filepath.Base(dir) {
		t.Fatalf("crumbs %v", cr)
	}
	pl := d["places"].([]any)
	if pl[0].(map[string]any)["kind"] != "internal" || pl[0].(map[string]any)["path"] != home {
		t.Fatalf("places %v", pl)
	}
	// The system's folders above home are never shown: "/" opens home.
	for _, at := range []string{"/", "/etc", filepath.Dir(home)} {
		if d := getAt(at, ""); d["path"] != home || d["top"] != true || d["parent"] != home {
			t.Fatalf("%s: %v", at, d)
		}
	}
}

func TestCrumbsStartAtTheDisk(t *testing.T) {
	cr := crumbs("/Volumes/BCLOUD/Day 1/A001", []Place{{Name: "Home", Path: "/Users/bb", Kind: "home"},
		{Name: "BCLOUD", Path: "/Volumes/BCLOUD", Kind: "disk"}})
	if len(cr) != 3 || cr[0].Name != "BCLOUD" || cr[1].Path != "/Volumes/BCLOUD/Day 1" || cr[2].Name != "A001" {
		t.Fatalf("%+v", cr)
	}
}

func TestMkdirMakesOneNewFolderOnly(t *testing.T) {
	s, _ := newTest()
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Dir(dir))
	h := s.HTTP().Handler
	mk := func(parent, name string) (int, map[string]string) {
		b, _ := json.Marshal(map[string]string{"parent": parent, "name": name})
		req := httptest.NewRequest("POST", "/api/mkdir", strings.NewReader(string(b)))
		req.Host = "localhost:8470"
		req.Header.Set("X-Shuttle", "1")
		req.Header.Set("Origin", "http://localhost:8470")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]string
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := mk(dir, "  Day 1 ")
	if code != http.StatusOK || out["path"] != filepath.Join(dir, "Day 1") || !isDir(filepath.Join(dir, "Day 1")) {
		t.Fatalf("make: %d %v", code, out)
	}
	os.WriteFile(filepath.Join(dir, "Day 1", "keep.txt"), []byte("x"), 0o600)
	if code, _ := mk(dir, "Day 1"); code != http.StatusBadRequest {
		t.Errorf("existing name: got %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "Day 1", "keep.txt")); err != nil {
		t.Error("an existing folder's contents were touched")
	}
	for _, bad := range []string{"", ".hidden", "..", "a/b", "a:b", "x\x01"} {
		if code, _ := mk(dir, bad); code != http.StatusBadRequest {
			t.Errorf("name %q: got %d", bad, code)
		}
	}
	if code, _ := mk("/etc", "nope"); code != http.StatusBadRequest {
		t.Errorf("outside home and disks: got %d", code)
	}
	if code, _ := mk(filepath.Join(dir, "missing"), "x"); code != http.StatusBadRequest {
		t.Errorf("missing parent: got %d", code)
	}
}

func TestStorageSharedPort(t *testing.T) {
	ds := []Disk{{Place: Place{Name: "A"}}, {Place: Place{Name: "B"}}, {Place: Place{Name: "C"}}, {Place: Place{Name: "A2"}}}
	ds[0].BusKey, ds[0].Unit = "port1", "0x1"
	ds[1].BusKey, ds[1].Unit = "port1", "0x2"
	ds[2].BusKey, ds[2].Unit = "port2", "0x3"
	ds[3].BusKey, ds[3].Unit = "port1", "0x1" // a second volume of drive A
	markShared(ds)
	if len(ds[0].Shares) != 1 || ds[0].Shares[0] != "B" || len(ds[1].Shares) != 2 || ds[2].Shares != nil {
		t.Fatalf("%v %v %v", ds[0].Shares, ds[1].Shares, ds[2].Shares)
	}
}
