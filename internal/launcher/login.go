package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// The login item is a launchd agent, tied to the "Shuttle Server" app in
// Applications (AssociatedBundleIdentifiers) so System Settings → Login Items
// shows that name and icon instead of "shuttle-server". It is: macOS starts it when the owner logs in.
// It only starts Shuttle (RunAtLoad, no KeepAlive), so "Stop Shuttle" really
// stops it until the next login. It is not "Background" type, which would
// throttle disk access (DESIGN.md section 18).

const agentName = "local.shuttle-server.plist"

const agentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>local.shuttle-server</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-data</string>
    <string>%s</string>
  </array>
  <key>AssociatedBundleIdentifiers</key>
  <array><string>local.shuttle-server.launcher</string></array>
  <key>RunAtLoad</key><true/>
  <key>WorkingDirectory</key><string>%s</string>
  <key>StandardOutPath</key><string>/dev/null</string>
  <key>StandardErrorPath</key><string>/dev/null</string>
</dict>
</plist>
`

// LoginSupported is true where the login item can be made.
func LoginSupported() bool { return runtime.GOOS == "darwin" }

// LoginPath is where the agent file lives.
func LoginPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentName)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func agentFor(bin, data string) string {
	home, _ := os.UserHomeDir()
	return fmt.Sprintf(agentPlist, xmlEscape(bin), xmlEscape(data), xmlEscape(home))
}

// LoginEnabled is true when the agent file exists.
func LoginEnabled() bool {
	_, err := os.Stat(LoginPath())
	return err == nil
}

// SetLogin turns starting at login on or off. Turning on writes the agent
// file; it does not start a second copy now, and macOS reads it at the next
// login. Turning off removes only that file, which is Shuttle's own.
func SetLogin(on bool, bin, data string) error {
	return setLoginAt(LoginPath(), on, bin, data)
}

func setLoginAt(path string, on bool, bin, data string) error {
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, []byte(agentFor(bin, data)), 0o644)
}

// RefreshLogin rewrites an existing agent file when the program or data
// folder moved, so the login item never points at a missing program.
func RefreshLogin(bin, data string) (bool, error) {
	return refreshLoginAt(LoginPath(), bin, data)
}

func refreshLoginAt(path, bin, data string) (bool, error) {
	cur, err := os.ReadFile(path)
	if err != nil {
		return false, nil // not turned on: never create it here
	}
	if string(cur) == agentFor(bin, data) {
		return false, nil
	}
	return true, fsx.WriteFileAtomic(path, []byte(agentFor(bin, data)), 0o644)
}
