package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/location"
)

func TestSizerKeepsAdoptableList(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "a"), 0o755)
	os.WriteFile(filepath.Join(src, "a", "x"), make([]byte, 1000), 0o644)
	os.WriteFile(filepath.Join(src, "y"), make([]byte, 24), 0o644)
	var z sizer
	z.setDir(filepath.Join(root, "draft"))
	f := location.Folder{Dir: src, System: true}
	var c SizeCount
	for i := 0; i < 200; i++ {
		if c = z.ask(src, nil, f); c.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !c.Done || c.Files != 2 || c.Bytes != 1024 {
		t.Fatalf("count %+v", c)
	}
	d := z.take(src, nil)
	if d == nil || d.Files != 2 || d.Bytes != 1024 {
		t.Fatalf("no list to adopt: %+v", d)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "manifest.jsonl")); err != nil {
		t.Fatal(err)
	}
	if z.take(src, []string{"y"}) != nil || z.take(filepath.Join(root, "other"), nil) != nil {
		t.Fatal("a list is only for the folder and names it was made for")
	}
}
