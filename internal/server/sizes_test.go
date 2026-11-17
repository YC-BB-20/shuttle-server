package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
)

// Folder sizes (v0.2.101): each folder's bytes and files, cut short and
// marked "partial" when the entry budget runs out; automatic sizing skipped
// on a spinning disk or while a job runs on that disk; links not followed.
func TestFolderSizes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mk := func(rel string, n int) {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("A/x.mov", 1000)
	mk("A/sub/y.mov", 500)
	mk("A/sub/deeper/z.mov", 25)
	mk("B/one", 7)
	os.MkdirAll(filepath.Join(home, "Empty"), 0o755)
	os.Symlink(filepath.Join(home, "A"), filepath.Join(home, "B", "link"))

	got, err := measureChildren(context.Background(), home, 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]folderSize{"A": {Bytes: 1525, Files: 3}, "B": {Bytes: 7, Files: 1}, "Empty": {}}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: got %+v, want %+v", k, got[k], w)
		}
	}
	short, _ := measureChildren(context.Background(), home, 2)
	if !short["A"].Partial || !short["B"].Partial {
		t.Errorf("a size cut short is not marked: %+v", short)
	}

	s, _ := newTest()
	jobs := &viewJobs{}
	s.Jobs = jobs
	old := sizeSpinning
	t.Cleanup(func() { sizeSpinning = old })
	spin := false
	sizeSpinning = func(context.Context, string) *bool { return &spin }
	ask := func(auto bool) map[string]any {
		q := "/api/browse/sizes?path=" + url.QueryEscape(home)
		if auto {
			q += "&auto=1"
		}
		req := httptest.NewRequest("GET", q, nil)
		req.Host = "localhost:8470"
		rec := httptest.NewRecorder()
		s.HTTP().Handler.ServeHTTP(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if out := ask(true); out["sizes"] == nil {
		t.Fatalf("auto on a free solid-state disk: %v", out)
	}
	spin = true
	if out := ask(true); out["skipped"] != "spinning" {
		t.Fatalf("auto on a spinning disk: %v", out)
	}
	if out := ask(false); out["sizes"] == nil {
		t.Fatalf("asked on a spinning disk: %v", out)
	}
	spin = false
	jobs.views = []copyjob.View{{Source: filepath.Join(home, "A"), Dest: "/x", State: "running"}}
	if out := ask(true); out["skipped"] != "busy" {
		t.Fatalf("auto while a job runs on the disk: %v", out)
	}
	req := httptest.NewRequest("GET", "/api/browse/sizes?path=/etc", nil)
	req.Host = "localhost:8470"
	rec := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("a system folder was measured: %d", rec.Code)
	}
}
