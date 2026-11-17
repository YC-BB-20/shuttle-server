package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/remote"
)

// Adding a server needs the Shuttle header; the list never holds the secret.
func TestServersKeepTheLoginOffThePage(t *testing.T) {
	s, _ := newTest()
	s.Servers = &remote.Store{Dir: t.TempDir()}
	h := s.HTTP().Handler
	do := func(method, path, body string, header bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "localhost:8470"
		if header {
			req.Header.Set("X-Shuttle", "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	add := `{"name":"CTERA","kind":"webdav","url":"https://portal.example.com/webdav","user":"owner","secret":"hunter2-secret"}`
	if rec := do("POST", "/api/servers", add, false); rec.Code != http.StatusForbidden {
		t.Fatalf("without the header: %d", rec.Code)
	}
	rec := do("POST", "/api/servers", add, true)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var p remote.Public
	json.Unmarshal(rec.Body.Bytes(), &p)
	rec = do("GET", "/api/servers", "", false)
	if !strings.Contains(rec.Body.String(), `"name":"CTERA"`) || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("list: %s", rec.Body)
	}
	if rec := do("POST", "/api/servers", `{"name":"x","kind":"webdav","url":"http://portal.example.com","user":"u","secret":"p"}`, true); rec.Code != 400 || !strings.Contains(rec.Body.String(), "https") {
		t.Fatalf("plain http accepted: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/servers/"+p.ID+"/remove", "", true); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", "/api/servers", "", false); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("after remove: %s", rec.Body)
	}
}
