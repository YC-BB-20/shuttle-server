package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkInsideAppAndFollowsTheProgram(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Shuttle Server.app")
	if _, err := installAt(app, "/bin/one", "/d", "8470"); err != nil {
		t.Fatal(err)
	}
	if !linkOK(app, "/bin/one") {
		t.Fatal("link missing after install")
	}
	if err := ensureLink(app, "/bin/two"); err != nil || !linkOK(app, "/bin/two") {
		t.Fatal("link must follow a moved program", err)
	}
	if got, _ := os.Readlink(linkPath(app)); got != "/bin/two" {
		t.Fatal(got)
	}
	if err := ensureLink(app, "/bin/two"); err != nil {
		t.Fatal("unchanged link must be fine")
	}
}
