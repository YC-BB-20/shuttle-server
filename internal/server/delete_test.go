package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

type delJobs struct {
	viewJobs
	set copyjob.Settings
}

func (d *delJobs) Settings() copyjob.Settings { return d.set }

// The Delete function (v0.2.106): off until switched on, a second switch for
// shares, everything checked before anything is touched, the Trash where
// there is one, and never what a job still uses, a whole disk or the home
// folder.
func TestDeleteGuards(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := filepath.Join(home, "Card")
	os.MkdirAll(filepath.Join(work, "A001"), 0o755)
	os.WriteFile(filepath.Join(work, "A001", "clip.mov"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(work, "note.txt"), []byte("y"), 0o644)
	os.WriteFile(filepath.Join(work, "busy.mov"), []byte("z"), 0o644)

	s, _ := newTest()
	jobs := &delJobs{set: copyjob.DefaultSettings}
	s.Jobs = jobs
	h := s.HTTP().Handler
	do := func(folder string, names ...string) (int, string) {
		b, _ := json.Marshal(deleteReq{Folder: folder, Names: names})
		req := httptest.NewRequest("POST", "/api/delete", strings.NewReader(string(b)))
		req.Host = "localhost:8470"
		req.Header.Set("X-Shuttle", "1")
		req.Header.Set("Origin", "http://localhost:8470")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	there := func(n string) bool { _, err := os.Lstat(filepath.Join(work, n)); return err == nil }

	if code, body := do(work, "note.txt"); code == http.StatusOK || !strings.Contains(body, "switched off") || !there("note.txt") {
		t.Fatalf("off by default: %d %s", code, body)
	}
	jobs.set.DeleteOn = true
	for _, bad := range [][]string{{"../Card"}, {"a/b"}, {".."}, {""}, {"note.txt", "note.txt"}, {"missing"}} {
		if code, _ := do(work, bad...); code == http.StatusOK {
			t.Errorf("%q: accepted", bad)
		}
	}
	if code, _ := do(filepath.Dir(home), filepath.Base(home)); code == http.StatusOK {
		t.Error("the home folder itself was accepted")
	}
	if code, _ := do(work+"/../Card", "note.txt"); code == http.StatusOK {
		t.Error("a folder path with .. was accepted")
	}
	// A job in the queue uses busy.mov: nothing at all is deleted, not even note.txt.
	jobs.views = []copyjob.View{{Source: filepath.Join(work, "busy.mov"), Dest: "/Volumes/T5/x", State: "queued"}}
	if code, body := do(work, "note.txt", "busy.mov"); code == http.StatusOK || !strings.Contains(body, "still uses") || !there("note.txt") || !there("busy.mov") {
		t.Fatalf("busy: %d %s", code, body)
	}
	jobs.views = []copyjob.View{{Source: work, State: "running"}}
	if code, _ := do(work, "A001"); code == http.StatusOK || !there("A001") {
		t.Fatal("deleted inside a running job's source")
	}
	jobs.views = []copyjob.View{{Source: work, State: "finished"}}
	if code, body := do(work, "note.txt", "A001"); code != http.StatusOK || there("note.txt") || there("A001") {
		t.Fatalf("to the Trash: %d %s", code, body)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".local", "share", "Trash", "files", "A001", "clip.mov")); string(b) != "x" {
		t.Fatal("not in the Trash with its contents")
	}

	// A share: nothing goes to a Trash, so it needs the second switch.
	old := deleteVolume
	t.Cleanup(func() { deleteVolume = old })
	deleteVolume = func(p string) (diskio.Volume, bool) {
		return diskio.Volume{Mount: work, Share: diskio.ShareLucid, Network: true}, true
	}
	if code, body := do(work, "busy.mov"); code == http.StatusOK || !strings.Contains(body, "for good") || !there("busy.mov") {
		t.Fatalf("share without its switch: %d %s", code, body)
	}
	jobs.set.DeleteShares = true
	if code, body := do(work, "busy.mov"); code != http.StatusOK {
		t.Fatalf("share: %d %s", code, body)
	}
	for i := 0; i < 100 && there("busy.mov"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if there("busy.mov") {
		t.Fatal("not deleted on the share")
	}
	deleteVolume = func(p string) (diskio.Volume, bool) { return diskio.Volume{Mount: "/", ReadOnly: true}, true }
	os.WriteFile(filepath.Join(work, "card.mov"), nil, 0o644)
	if code, _ := do(work, "card.mov"); code == http.StatusOK || !there("card.mov") {
		t.Fatal("deleted on a read-only disk")
	}
}

func TestDeleteSettingsDefaults(t *testing.T) {
	d := copyjob.DefaultSettings
	if d.DeleteOn || d.DeleteShares || d.DeleteConfirms != 2 {
		t.Fatalf("defaults %+v", d)
	}
}
