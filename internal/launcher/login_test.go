package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginOnOffAndRefresh(t *testing.T) {
	p := filepath.Join(t.TempDir(), "LaunchAgents", agentName)
	if ok, _ := refreshLoginAt(p, "/b", "/d"); ok {
		t.Fatal("refresh must never create the file")
	}
	if err := setLoginAt(p, true, "/Users/a b/bin/shuttle-server", "/d&x"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "<key>RunAtLoad</key><true/>") || strings.Contains(s, "KeepAlive") || strings.Contains(s, "Background") {
		t.Fatal("wrong keys:", s)
	}
	if !strings.Contains(s, "<string>local.shuttle-server.launcher</string>") || !strings.Contains(plist, "local.shuttle-server.launcher") {
		t.Fatal("the login item must be tied to the Shuttle Server app, by the app's own identifier")
	}
	if !strings.Contains(s, "/Users/a b/bin/shuttle-server") || !strings.Contains(s, "/d&amp;x") {
		t.Fatal("paths:", s)
	}
	if ok, err := refreshLoginAt(p, "/Users/a b/bin/shuttle-server", "/d&x"); ok || err != nil {
		t.Fatal("unchanged file must not be rewritten")
	}
	if ok, err := refreshLoginAt(p, "/new/bin", "/d"); !ok || err != nil {
		t.Fatal("moved program must be rewritten")
	}
	if err := setLoginAt(p, false, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
	if err := setLoginAt(p, false, "", ""); err != nil {
		t.Fatal("turning off twice must be fine")
	}
}
