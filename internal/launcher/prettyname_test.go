package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrettyNameRenamesAndKeepsTheOldCommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "shuttle-server")
	os.WriteFile(bin, []byte("program"), 0o755)
	got := prettyAt(bin)
	if got != filepath.Join(dir, "Shuttle Server") {
		t.Fatal(got)
	}
	if b, _ := os.ReadFile(got); string(b) != "program" {
		t.Fatal("program lost")
	}
	if b, err := os.ReadFile(bin); err != nil || string(b) != "program" { // through the shortcut
		t.Fatal("the old start command no longer reaches the program", err)
	}
	if again := prettyAt(got); again != got { // already named: nothing to do
		t.Fatal(again)
	}
}

func TestPrettyNameLeavesAnExistingFileAlone(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "shuttle-server")
	os.WriteFile(bin, []byte("new"), 0o755)
	os.WriteFile(filepath.Join(dir, "Shuttle Server"), []byte("other"), 0o755)
	if got := prettyAt(bin); got != bin {
		t.Fatal("must not overwrite", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Shuttle Server")); string(b) != "other" {
		t.Fatal("overwritten")
	}
}
