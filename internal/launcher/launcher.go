// Package launcher makes "Shuttle Server.app" in ~/Applications: double-click
// it (or find it with Spotlight) to start Shuttle in the background and open
// its page. It is made on this Mac, so it carries no download quarantine. It
// changes no system setting and does not start at login.
package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Shuttle Server</string>
  <key>CFBundleIdentifier</key><string>local.shuttle-server.launcher</string>
  <key>CFBundleExecutable</key><string>launch</string>
  <key>CFBundlePackageType</key><string>APPL</string>
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
cd "$HOME" && /usr/bin/nohup "$BIN" -data "$DATA" >/dev/null 2>&1 &
for i in 1 2 3 4 5 6 7 8 9 10; do
  sleep 0.5
  /usr/bin/curl -s -m 1 -o /dev/null "http://127.0.0.1:%s/api/status" && break
done
/usr/bin/open "$PAGE"
`

// Install writes the app and returns its path.
func Install(bin, data, port string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("the launcher is for macOS")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	app := filepath.Join(home, "Applications", "Shuttle Server.app")
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		return "", err
	}
	page := "http://localhost:" + port
	s := fmt.Sprintf(script, bin, data, page, port, port)
	if err := os.WriteFile(filepath.Join(macos, "launch"), []byte(s), 0o755); err != nil {
		return "", err
	}
	return app, nil
}
