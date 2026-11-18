package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// Move (v0.2.114): a rename within one drive, never a copy, never over
// something already there, all or nothing, never into itself, never what a
// job uses.
func TestMoveGuards(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a, b := filepath.Join(home, "A"), filepath.Join(home, "B")
	os.MkdirAll(filepath.Join(a, "Day 01", "A001"), 0o755)
	os.WriteFile(filepath.Join(a, "Day 01", "A001", "clip.mov"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(a, "note.txt"), []byte("n"), 0o644)
	os.MkdirAll(b, 0o755)
	s, _ := newTest()
	jobs := &delJobs{set: copyjob.DefaultSettings}
	s.Jobs = jobs
	h := s.HTTP().Handler
	do := func(folder, to string, names ...string) (int, string) {
		body, _ := json.Marshal(map[string]any{"folder": folder, "to": to, "names": names})
		req := httptest.NewRequest("POST", "/api/move", strings.NewReader(string(body)))
		req.Host = "localhost:8470"
		req.Header.Set("X-Shuttle", "1")
		req.Header.Set("Origin", "http://localhost:8470")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	at := func(p string) bool { _, err := os.Lstat(p); return err == nil }

	if code, _ := do(a, filepath.Join(a, "Day 01"), "Day 01"); code == http.StatusOK || !at(filepath.Join(a, "Day 01")) {
		t.Fatal("moved a folder into itself")
	}
	os.WriteFile(filepath.Join(b, "note.txt"), []byte("other"), 0o644)
	if code, body := do(a, b, "Day 01", "note.txt"); code == http.StatusOK || !strings.Contains(body, "already in") || !at(filepath.Join(a, "Day 01")) {
		t.Fatalf("a name already there: %d %s (and nothing may move)", code, body)
	}
	os.Remove(filepath.Join(b, "note.txt"))
	jobs.views = []copyjob.View{{Source: filepath.Join(a, "Day 01", "A001"), State: "running"}}
	if code, _ := do(a, b, "Day 01"); code == http.StatusOK {
		t.Fatal("moved what a running job reads")
	}
	jobs.views = nil
	old := deleteVolume
	t.Cleanup(func() { deleteVolume = old })
	deleteVolume = func(p string) (diskio.Volume, bool) {
		if strings.HasPrefix(p, b) {
			return diskio.Volume{ID: "T5", Mount: b}, true
		}
		return diskio.Volume{ID: "internal", Mount: "/"}, true
	}
	if code, body := do(a, b, "Day 01"); code == http.StatusOK || !strings.Contains(body, "use Copy") {
		t.Fatalf("another drive: %d %s", code, body)
	}
	deleteVolume = old
	if code, body := do(a, b, "Day 01", "note.txt"); code != http.StatusOK {
		t.Fatalf("move: %d %s", code, body)
	}
	if at(filepath.Join(a, "Day 01")) || !at(filepath.Join(b, "Day 01", "A001", "clip.mov")) || !at(filepath.Join(b, "note.txt")) {
		t.Fatal("not moved with its contents")
	}
	if code, _ := do(filepath.Dir(home), b, filepath.Base(home)); code == http.StatusOK {
		t.Fatal("moved the home folder")
	}
}

type moveJobs struct {
	delJobs
	folder, to string
	names      []string
}

func (m *moveJobs) Moved(folder string, names []string, to string) (int, int) {
	m.folder, m.names, m.to = folder, names, to
	return 2, 1
}

// The records hear of exactly what moved (v0.2.116).
func TestMoveTellsTheRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a, b := filepath.Join(home, "A"), filepath.Join(home, "B")
	os.MkdirAll(filepath.Join(a, "Day 01"), 0o755)
	os.MkdirAll(b, 0o755)
	s, _ := newTest()
	jobs := &moveJobs{delJobs: delJobs{set: copyjob.DefaultSettings}}
	s.Jobs = jobs
	body, _ := json.Marshal(map[string]any{"folder": a, "to": b, "names": []string{"Day 01"}})
	req := httptest.NewRequest("POST", "/api/move", strings.NewReader(string(body)))
	req.Host = "localhost:8470"
	req.Header.Set("X-Shuttle", "1")
	req.Header.Set("Origin", "http://localhost:8470")
	rec := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"records":2`) || !strings.Contains(rec.Body.String(), `"records_partial":1`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if jobs.folder != a || jobs.to != b || len(jobs.names) != 1 || jobs.names[0] != "Day 01" {
		t.Fatalf("told %q %v %q", jobs.folder, jobs.names, jobs.to)
	}
}
