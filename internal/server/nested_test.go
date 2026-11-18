package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
