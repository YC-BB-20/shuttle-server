package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
)

type viewJobs struct {
	Actions
	views []copyjob.View
}

func (v *viewJobs) Views() []copyjob.View { return v.views }

// Eject (v0.2.100) only asks the system about a connected disk that no
// unfinished job uses; this computer's own disk and unknown paths are refused.
func TestEjectOnlyAFreeConnectedDisk(t *testing.T) {
	s, _ := newTest()
	jobs := &viewJobs{}
	s.Jobs = jobs
	oldP, oldE := ejectPlaces, ejectFn
	t.Cleanup(func() { ejectPlaces, ejectFn = oldP, oldE })
	ejectPlaces = func() []Place {
		return []Place{{Name: "Mac", Path: "/Users/me", Kind: "internal"}, {Name: "T5", Path: "/Volumes/T5", Kind: "disk"}, {Name: "Office", Path: "/Volumes/Office", Kind: "share", Share: "smb"}}
	}
	var asked []string
	ejectFn = func(_ context.Context, mount string, share bool) error {
		asked = append(asked, mount+map[bool]string{true: " share", false: ""}[share])
		return nil
	}
	h := s.HTTP().Handler
	do := func(path string) (int, string) {
		b, _ := json.Marshal(map[string]string{"path": path})
		req := httptest.NewRequest("POST", "/api/disks/eject", strings.NewReader(string(b)))
		req.Host = "localhost:8470"
		req.Header.Set("X-Shuttle", "1")
		req.Header.Set("Origin", "http://localhost:8470")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	for _, p := range []string{"/Users/me", "/", "/Volumes/Other", ""} {
		if code, _ := do(p); code == http.StatusOK {
			t.Errorf("%q: ejected", p)
		}
	}
	jobs.views = []copyjob.View{{Source: "/Volumes/T5/A001", Dest: "/Users/me/x", State: "queued"}, {Source: "/Volumes/Office/a", State: "finished"}}
	if code, body := do("/Volumes/T5"); code == http.StatusOK || !strings.Contains(body, "still uses this disk") {
		t.Errorf("busy disk: %d %s", code, body)
	}
	if code, body := do("/Volumes/Office"); code != http.StatusOK {
		t.Errorf("share: %d %s", code, body)
	}
	jobs.views = nil
	if code, body := do("/Volumes/T5/"); code != http.StatusOK {
		t.Errorf("free disk: %d %s", code, body)
	}
	if strings.Join(asked, "|") != "/Volumes/Office share|/Volumes/T5" {
		t.Errorf("asked the system for %v", asked)
	}
}
