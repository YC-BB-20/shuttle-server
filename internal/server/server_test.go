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
	os.MkdirAll(dir+"/Card A", 0o700)
	os.WriteFile(dir+"/clip.mov", []byte("1234"), 0o600)
	get := func(q string) map[string]any {
		req := httptest.NewRequest("GET", "/api/browse?path="+url.QueryEscape(dir)+q, nil)
		req.Host = "localhost"
		rec := httptest.NewRecorder()
		s.HTTP().Handler.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if strings.Contains(fmt.Sprint(get("")["folders"]), "clip.mov") {
		t.Fatal("files listed without being asked for")
	}
	d := get("&files=1")
	if !strings.Contains(fmt.Sprint(d["folders"]), "clip.mov") {
		t.Fatal("file not listed")
	}
	cr := d["crumbs"].([]any)
	last := cr[len(cr)-1].(map[string]any)
	if cr[0].(map[string]any)["path"] != "/" || last["path"] != dir || last["name"] != filepath.Base(dir) {
		t.Fatalf("crumbs %v", cr)
	}
	pl := d["places"].([]any)
	if pl[0].(map[string]any)["kind"] != "home" || pl[1].(map[string]any)["kind"] != "internal" {
		t.Fatalf("places %v", pl)
	}
}

func TestCrumbsStartAtTheDisk(t *testing.T) {
	cr := crumbs("/Volumes/BCLOUD/Day 1/A001", []Place{{Name: "Home", Path: "/Users/bb", Kind: "home"},
		{Name: "BCLOUD", Path: "/Volumes/BCLOUD", Kind: "disk"}})
	if len(cr) != 3 || cr[0].Name != "BCLOUD" || cr[1].Path != "/Volumes/BCLOUD/Day 1" || cr[2].Name != "A001" {
		t.Fatalf("%+v", cr)
	}
}
