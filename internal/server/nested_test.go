package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// A folder in /Volumes holding plain folders (left behind by an unplugged
// disk) lists nothing; a real mount one level down is listed (v0.2.102).
func TestNestedPlaces(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "filespace", "Media"), 0o755); err != nil {
		t.Fatal(err)
	}
	v, ok, _ := volume(dir, 2*time.Second)
	if !ok {
		t.Skip("no volume info here")
	}
	if got := nestedPlaces(dir, v.Mount, map[string]bool{}, 100); len(got) != 0 {
		t.Fatalf("plain folders listed: %+v", got)
	}
	pv, ok, _ := volume("/proc", 2*time.Second)
	if !ok || pv.Mount != "/proc" {
		t.Skip("no /proc mount to stand in for a nested mount")
	}
	defer func(n int) { nestedMax = n }(nestedMax)
	nestedMax = 1000 // "/" has more than 20 names
	rv, _, _ := volume("/", 2*time.Second)
	seen := map[string]bool{}
	found := false
	for _, p := range nestedPlaces("/", rv.Mount, seen, 100) {
		if p.Path == "/proc" {
			found = true
		}
		if p.Path == rv.Mount {
			t.Fatalf("the parent's own mount listed: %+v", p)
		}
	}
	if !found || !seen["/proc"] {
		t.Fatal("a mount one level down was not listed")
	}
	if got := nestedPlaces("/", rv.Mount, seen, 100); len(got) != 0 && got[0].Path == "/proc" {
		t.Fatal("listed twice")
	}
}

// A share whose lookup is slow is shown as it last answered, for a while;
// a disk that is slow is not (v0.2.103).
func TestBusyShareKeepsItsPlace(t *testing.T) {
	p := "/Volumes/work/filespace"
	volMu.Lock()
	volPending[p] = true // an earlier lookup is still waiting
	volShare[p] = shareSeen{diskio.Volume{Mount: p, Share: diskio.ShareLucid}, time.Now()}
	volMu.Unlock()
	defer func() {
		volMu.Lock()
		delete(volPending, p)
		delete(volShare, p)
		volMu.Unlock()
	}()
	if v, ok, hung := volume(p, time.Millisecond); !ok || hung || v.Share != diskio.ShareLucid {
		t.Fatalf("busy share dropped: %+v %v %v", v, ok, hung)
	}
	volMu.Lock()
	volShare[p] = shareSeen{diskio.Volume{Mount: p, Share: diskio.ShareLucid}, time.Now().Add(-shareGrace - time.Second)}
	volMu.Unlock()
	if _, ok, hung := volume(p, time.Millisecond); ok || !hung {
		t.Fatal("an old answer stood in for a share that stopped answering")
	}
	d := "/Volumes/T5"
	volMu.Lock()
	volPending[d] = true
	volMu.Unlock()
	defer func() { volMu.Lock(); delete(volPending, d); volMu.Unlock() }()
	if _, ok, hung := volume(d, time.Millisecond); ok || !hung {
		t.Fatal("a slow disk was not shown as not responding")
	}
}
