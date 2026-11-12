package location

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestWalkFindsFilesSkipsLinksAndOwnTemps(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "A", "B"), 0o700)
	os.WriteFile(filepath.Join(dir, "top.mov"), []byte("1"), 0o600)
	os.WriteFile(filepath.Join(dir, "A", "B", "deep.arx"), []byte("22"), 0o600)
	os.WriteFile(filepath.Join(dir, "A", ".x.shuttle-part"), []byte("3"), 0o600)
	os.Symlink("top.mov", filepath.Join(dir, "link"))
	var got []string
	var skipped []Skipped
	err := Folder{dir}.Walk(context.Background(), func(e Entry) error { got = append(got, e.Rel); return nil },
		func(s Skipped) { skipped = append(skipped, s) })
	sort.Strings(got)
	if err != nil || len(got) != 2 || got[0] != "A/B/deep.arx" || got[1] != "top.mov" {
		t.Fatalf("%v %v", got, err)
	}
	if len(skipped) != 1 || skipped[0].Rel != "link" {
		t.Fatalf("skipped %v", skipped)
	}
}
