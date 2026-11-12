package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptNeverUsesNohup(t *testing.T) {
	s := scriptFor("/Users/bb/Shuttle-Server/bin/shuttle-server", "/Users/bb/Shuttle-Server-Data", "8470")
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "nohup") {
			t.Fatal("nohup moves the program out of the login session; the menu bar icon then cannot show")
		}
	}
	if !strings.Contains(s, `-data "$DATA" </dev/null >/dev/null 2>&1 &`) || !strings.Contains(s, "127.0.0.1:8470") {
		t.Fatal(s)
	}
}

func TestInstallWritesBundle(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Shuttle Server.app")
	if _, err := installAt(app, "/bin/x", "/d", "8470"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "launch"))
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("launch script missing or not executable")
	}
	if b, _ := os.ReadFile(filepath.Join(app, "Contents", "Info.plist")); !strings.Contains(string(b), "LSUIElement") {
		t.Fatal("plist")
	}
}
