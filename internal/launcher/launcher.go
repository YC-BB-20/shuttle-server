// Package launcher makes "Shuttle Server.app" in ~/Applications: double-click
// it (or find it with Spotlight) to start Shuttle in the background and open
// its page. It is made on this Mac, so it carries no download quarantine. It
// changes no system setting and does not start at login.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/YC-BB-20/shuttle-server/internal/appicon"
)

const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Shuttle Server</string>
  <key>CFBundleIdentifier</key><string>local.shuttle-server.launcher</string>
  <key>CFBundleExecutable</key><string>launch</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleIconFile</key><string>Shuttle</string>
  <key>CFBundleShortVersionString</key><string>1</string>
  <key>LSUIElement</key><true/>
</dict>
</plist>
`

const script = `#!/bin/sh
# Starts Shuttle Server if it is not running, then opens its page.
BIN=%q
DATA=%q
PAGE=%q
if /usr/bin/curl -s -m 2 -o /dev/null "http://127.0.0.1:%s/api/status"; then
  /usr/bin/open "$PAGE"
  exit 0
fi
if [ ! -x "$BIN" ]; then
  /usr/bin/osascript -e 'display alert "Shuttle Server" message "The program is missing from '"$BIN"'."'
  exit 1
fi
# Not nohup: on macOS nohup moves the program out of the login session, and
# then its menu bar icon cannot be shown. Started from this app there is no
# terminal to hang up, so a plain background start is enough.
cd "$HOME" && "$BIN" -data "$DATA" </dev/null >/dev/null 2>&1 &
for i in 1 2 3 4 5 6 7 8 9 10; do
  sleep 0.5
  /usr/bin/curl -s -m 1 -o /dev/null "http://127.0.0.1:%s/api/status" && break
done
/usr/bin/open "$PAGE"
`

// AppPath is where the launcher lives.
func AppPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Applications", "Shuttle Server.app")
}

// Refresh rewrites an existing launcher if its script is out of date, so a
// fix to the launcher arrives with an update. It never creates one.
func Refresh(bin, data, port string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, nil
	}
	cur, err := os.ReadFile(filepath.Join(AppPath(), "Contents", "MacOS", "launch"))
	if err != nil {
		return false, nil
	}
	if _, noIcon := os.Stat(iconPath(AppPath())); string(cur) == scriptFor(bin, data, port) && noIcon == nil && linkOK(AppPath(), bin) {
		return false, nil
	}
	_, err = installAt(AppPath(), bin, data, port)
	return err == nil, err
}

func scriptFor(bin, data, port string) string {
	return fmt.Sprintf(script, bin, data, "http://localhost:"+port, port, port)
}

// Install writes the app and returns its path.
func Install(bin, data, port string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("the launcher is for macOS")
	}
	return installAt(AppPath(), bin, data, port)
}

func installAt(app, bin, data, port string) (string, error) {
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(macos, "launch"), []byte(scriptFor(bin, data, port)), 0o755); err != nil {
		return "", err
	}
	if err := ensureLink(app, bin); err != nil {
		return "", err
	}
	// The icon is a nicety: without it the app still works.
	if runtime.GOOS == "darwin" {
		res := filepath.Join(app, "Contents", "Resources")
		if os.MkdirAll(res, 0o755) == nil {
			appicon.WriteICNS(context.Background(), iconPath(app))
		}
	}
	return app, nil
}

// LinkPath is the program's link inside the app. The start-at-login item runs
// it from there, so macOS sees the program as part of "Shuttle Server" and can
// show that name and icon (an item outside any app is listed as its file name).
func LinkPath() string { return linkPath(AppPath()) }

func linkPath(app string) string { return filepath.Join(app, "Contents", "MacOS", "shuttle-server") }

func linkOK(app, bin string) bool {
	cur, err := os.Readlink(linkPath(app))
	return err == nil && cur == bin
}

// ensureLink points the link at bin. The link is our own file, so replacing it is fine.
func ensureLink(app, bin string) error {
	if linkOK(app, bin) {
		return nil
	}
	os.Remove(linkPath(app))
	return os.Symlink(bin, linkPath(app))
}

func iconPath(app string) string { return filepath.Join(app, "Contents", "Resources", "Shuttle.icns") }
