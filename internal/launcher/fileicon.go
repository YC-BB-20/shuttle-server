package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// System Settings → Login Items shows a background item by its program file's
// own name and icon (it is not tied to an app unless an Apple developer has
// signed both). The name cannot change, but the file can carry a custom icon,
// the way a folder can in Finder. It is kept in the file's extended
// attributes, so a replaced program (an update) loses it: Shuttle sets it
// again at every start.

const setIconScript = `ObjC.import("Cocoa");
function run(a) {
  const img = $.NSImage.alloc.initWithContentsOfFile(a[0]);
  if (img.isNil()) throw new Error("could not read the icon");
  if (!$.NSWorkspace.sharedWorkspace.setIconForFileOptions(img, a[1], 0)) throw new Error("macOS did not set the icon");
}`

// SetFileIcon gives the program file Shuttle's logo, from the launcher app's icon.
func SetFileIcon(ctx context.Context, bin string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	icns := iconPath(AppPath())
	if _, err := os.Stat(icns); err != nil {
		return errors.New("the Shuttle Server app has no icon yet")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", setIconScript, icns, bin).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
