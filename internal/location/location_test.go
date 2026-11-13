package location

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	err := Folder{Dir: dir}.Walk(context.Background(), func(e Entry) error { got = append(got, e.Rel); return nil },
		func(s Skipped) { skipped = append(skipped, s) })
	sort.Strings(got)
	if err != nil || len(got) != 2 || got[0] != "A/B/deep.arx" || got[1] != "top.mov" {
		t.Fatalf("%v %v", got, err)
	}
	if len(skipped) != 1 || skipped[0].Rel != "link" {
		t.Fatalf("skipped %v", skipped)
	}
}

func TestWalkOnlyChosen(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "A", "B"), 0o700)
	os.MkdirAll(filepath.Join(dir, "C"), 0o700)
	os.WriteFile(filepath.Join(dir, "one.mov"), []byte("1"), 0o600)
	os.WriteFile(filepath.Join(dir, "two.mov"), []byte("2"), 0o600)
	os.WriteFile(filepath.Join(dir, "A", "B", "deep.arx"), []byte("3"), 0o600)
	os.WriteFile(filepath.Join(dir, "C", "other"), []byte("4"), 0o600)
	os.WriteFile(filepath.Join(dir, "A", "one.mov"), []byte("5"), 0o600) // same name deeper: inside a chosen folder, taken
	var got []string
	err := Folder{Dir: dir, Only: []string{"one.mov", "A"}}.Walk(context.Background(),
		func(e Entry) error { got = append(got, e.Rel); return nil }, func(Skipped) {})
	sort.Strings(got)
	if err != nil || strings.Join(got, ",") != "A/B/deep.arx,A/one.mov,one.mov" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestIsSystemName(t *testing.T) {
	for _, n := range []string{".DS_Store", ".ds_store", ".Spotlight-V100", ".Trashes", ".fseventsd", ".TemporaryItems", "Thumbs.db", "desktop.ini"} {
		if !IsSystemName(n, false) {
			t.Errorf("%s not a system name", n)
		}
	}
	for _, n := range []string{"A001C001.ari", "DS_Store", "._A001.mxf", ".hidden"} {
		if IsSystemName(n, false) {
			t.Errorf("%s taken as a system name", n)
		}
	}
	if !IsSystemName("._A001.mxf", true) || !IsSystemPath("a/.Spotlight-V100/b", false) || IsSystemPath("a/b/c", true) {
		t.Error("AppleDouble or path check wrong")
	}
}
