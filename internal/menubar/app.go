package menubar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/appicon"
)

// The helper runs as a small real app, "Shuttle Server", so macOS lists it
// under that name with Shuttle's icon (System Settings → Menu Bar, Login
// Items) instead of a bare "osascript". It is made on this Mac from the same
// menubar.js with macOS's own osacompile, so it needs nothing installed and
// carries no download quarantine. If making it fails, Run falls back to
// osascript, as before.

const (
	appFolder = "helper.noindex" // the .noindex suffix keeps Spotlight out
	appName   = "Shuttle Server Menu.app"
	stampFile = "shuttle-stamp"
	// iconRev changes when the icon or the bundle's settings change, so the
	// app is made again once.
	iconRev = "1"
)

// runTool runs one of macOS's tools; tests replace it.
var runTool = func(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		out = bytes.TrimSpace(out)
		if len(out) > 300 {
			out = out[len(out)-300:]
		}
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, out)
	}
	return nil
}

// makeICNS is replaced in tests.
var makeICNS = appicon.WriteICNS

func stampOf() string {
	h := sha256.Sum256(append([]byte(iconRev+"\n"), script...))
	return hex.EncodeToString(h[:8])
}

// AppExecutable is the helper app's program inside dir.
func AppExecutable(dir string) string {
	return filepath.Join(dir, appFolder, appName, "Contents", "MacOS", "applet")
}

func upToDate(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, appFolder, appName, "Contents", "Resources", stampFile))
	return err == nil && string(b) == stampOf() && fileExists(AppExecutable(dir))
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// BuildApp makes the helper app inside dir if it is missing or out of date,
// and returns the path of its program. dir is Shuttle's own temp folder.
func BuildApp(ctx context.Context, dir string) (string, error) {
	if upToDate(dir) {
		return AppExecutable(dir), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	root := filepath.Join(dir, appFolder)
	work := filepath.Join(root, "building")
	os.RemoveAll(work) // our own leftovers from a stopped build
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	src := filepath.Join(work, "menubar.js")
	if err := os.WriteFile(src, script, 0o600); err != nil {
		return "", err
	}
	app := filepath.Join(work, appName)
	if err := runTool(ctx, "/usr/bin/osacompile", "-l", "JavaScript", "-o", app, src); err != nil {
		return "", err
	}
	res := filepath.Join(app, "Contents", "Resources")
	if err := makeICNS(ctx, filepath.Join(res, "applet.icns")); err != nil {
		return "", err
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	for _, kv := range [][]string{
		{"CFBundleName", "-string", "Shuttle Server"},
		{"CFBundleDisplayName", "-string", "Shuttle Server"},
		{"CFBundleIdentifier", "-string", "local.shuttle-server.menu"},
		{"CFBundleIconFile", "-string", "applet"},
		{"LSUIElement", "-bool", "YES"},
	} {
		if err := runTool(ctx, "/usr/bin/plutil", "-replace", kv[0], kv[1], kv[2], plist); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(res, stampFile), []byte(stampOf()), 0o644); err != nil {
		return "", err
	}
	// Changing the plist broke the signature osacompile gave it; sign again
	// (ad hoc, which needs no account) so macOS will run it.
	if err := runTool(ctx, "/usr/bin/codesign", "--force", "--sign", "-", app); err != nil {
		return "", err
	}
	final := filepath.Join(root, appName)
	os.RemoveAll(final) // our own helper app, being replaced by the new one
	if err := os.Rename(app, final); err != nil {
		return "", err
	}
	return AppExecutable(dir), nil
}
