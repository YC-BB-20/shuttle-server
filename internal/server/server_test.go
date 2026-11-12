package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
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
