package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	for _, s := range []string{"one", "two"} {
		if err := WriteFileAtomic(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temporary files left behind: %v", ents)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestFreeBytes(t *testing.T) {
	if n, err := FreeBytes(t.TempDir()); err != nil || n == 0 {
		t.Fatalf("free=%d err=%v", n, err)
	}
}
