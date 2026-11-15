package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeLogin struct{ on bool }

func (f *fakeLogin) Enabled() bool     { return f.on }
func (f *fakeLogin) Set(on bool) error { f.on = on; return nil }

func TestLoginSetting(t *testing.T) {
	do := func(h http.Handler, method, body string, cmd bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/settings/login", strings.NewReader(body))
		req.Host = "localhost:8470"
		if cmd {
			req.Header.Set("X-Shuttle", "1")
			req.Header.Set("Origin", "http://localhost:8470")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	// Not supported: reads as unsupported, writes are refused.
	s, _ := newTest()
	h := s.HTTP().Handler
	if b := do(h, "GET", "", false).Body.String(); !strings.Contains(b, `"supported":false`) {
		t.Fatal(b)
	}
	if c := do(h, "POST", `{"on":true}`, true).Code; c == http.StatusOK {
		t.Fatal("unsupported computer accepted a change")
	}
	// Supported: a foreign page cannot change it; the owner's page can.
	f := &fakeLogin{}
	s.Login = f
	h = s.HTTP().Handler
	if c := do(h, "POST", `{"on":true}`, false).Code; c != http.StatusForbidden || f.on {
		t.Fatalf("no header: %d", c)
	}
	if rec := do(h, "POST", `{"on":true}`, true); rec.Code != http.StatusOK || !f.on || !strings.Contains(rec.Body.String(), `"on":true`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	do(h, "POST", `{"on":false}`, true)
	if f.on {
		t.Fatal("not turned off")
	}
}
