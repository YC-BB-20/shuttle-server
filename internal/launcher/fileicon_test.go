package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOldLinkRemovedButARealFileIsNot(t *testing.T) {
	app := t.TempDir()
	dir := filepath.Join(app, "Contents", "MacOS")
	os.MkdirAll(dir, 0o755)
	l := filepath.Join(dir, "shuttle-server")
	os.Symlink("/bin/x", l)
	removeOldLink(app)
	if _, err := os.Lstat(l); !os.IsNotExist(err) {
		t.Fatal("old link left behind")
	}
	os.WriteFile(l, []byte("mine"), 0o755)
	removeOldLink(app)
	if _, err := os.Stat(l); err != nil {
		t.Fatal("a real file must never be removed")
	}
}
