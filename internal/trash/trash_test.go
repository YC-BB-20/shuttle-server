package trash

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMoveToTrashKeepsEverything(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Linux Trash layout")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := filepath.Join(home, "work")
	os.MkdirAll(filepath.Join(work, "Day 01", "A001"), 0o755)
	os.WriteFile(filepath.Join(work, "Day 01", "A001", "clip.mov"), []byte("picture"), 0o644)
	os.WriteFile(filepath.Join(work, "note.txt"), []byte("one"), 0o644)
	os.Symlink(filepath.Join(work, "Day 01"), filepath.Join(work, "link"))

	to, err := MoveToTrash(filepath.Join(work, "Day 01"), "/")
	if err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(home, ".local", "share", "Trash", "files")
	if to != filepath.Join(files, "Day 01") {
		t.Fatalf("moved to %s", to)
	}
	if b, _ := os.ReadFile(filepath.Join(to, "A001", "clip.mov")); string(b) != "picture" {
		t.Fatal("the folder's contents did not move with it")
	}
	info, _ := os.ReadFile(filepath.Join(home, ".local", "share", "Trash", "info", "Day 01.trashinfo"))
	if !strings.Contains(string(info), "Path="+strings.ReplaceAll(filepath.Join(work, "Day 01"), " ", "%20")) {
		t.Fatalf("trashinfo %q", info)
	}
	// A second item of the same name gets " 2", never replacing the first.
	os.WriteFile(filepath.Join(work, "Day 01"), []byte("a file now"), 0o644)
	if to, err := MoveToTrash(filepath.Join(work, "Day 01"), "/"); err != nil || to != filepath.Join(files, "Day 01 2") {
		t.Fatalf("%s %v", to, err)
	}
	os.WriteFile(filepath.Join(work, "x"), nil, 0o644)
	os.WriteFile(filepath.Join(work, "note 2.txt"), nil, 0o644)
	MoveToTrash(filepath.Join(work, "note.txt"), "/")
	os.WriteFile(filepath.Join(work, "note.txt"), []byte("two"), 0o644)
	if to, _ := MoveToTrash(filepath.Join(work, "note.txt"), "/"); filepath.Base(to) != "note 2.txt" {
		t.Fatalf("second note went to %s", to)
	}
	// A link is moved itself; what it points to stays (it is in the Trash already, and untouched).
	if to, err := MoveToTrash(filepath.Join(work, "link"), "/"); err != nil {
		t.Fatal(err)
	} else if fi, _ := os.Lstat(to); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was followed")
	}
}

func TestNoTrashMeansUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defer func(f func(a, b string) bool) { sameDisk = f }(sameDisk)
	sameDisk = func(a, b string) bool { return false } // every Trash is on another disk
	p := filepath.Join(t.TempDir(), "clip.mov")
	os.WriteFile(p, []byte("x"), 0o644)
	if _, err := MoveToTrash(p, "/"); !errors.Is(err, ErrNoTrash) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("the item was touched when it could not go to the Trash")
	}
}

func TestRemoveForGood(t *testing.T) {
	d := t.TempDir()
	keep := filepath.Join(d, "keep")
	os.MkdirAll(keep, 0o755)
	os.WriteFile(filepath.Join(keep, "f"), []byte("x"), 0o644)
	gone := filepath.Join(d, "gone")
	os.MkdirAll(gone, 0o755)
	os.Symlink(keep, filepath.Join(gone, "link"))
	if err := RemoveForGood(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(keep, "f")); err != nil {
		t.Fatal("a link inside was followed")
	}
}
