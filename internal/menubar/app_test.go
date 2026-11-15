package menubar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Stand-ins for osacompile, plutil, codesign and iconutil: they record what
// was asked and make the files the real tools would.
func fakeTools(t *testing.T, calls *[]string, failOn string) {
	oldRun, oldIcns := runTool, makeICNS
	t.Cleanup(func() { runTool, makeICNS = oldRun, oldIcns })
	runTool = func(ctx context.Context, name string, args ...string) error {
		*call(calls) = append(*calls, filepath.Base(name)+" "+strings.Join(args, " "))
		if filepath.Base(name) == failOn {
			return errors.New("refused")
		}
		if filepath.Base(name) == "osacompile" {
			app := args[3]
			os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
			os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0o755)
			os.WriteFile(filepath.Join(app, "Contents", "MacOS", "applet"), []byte("x"), 0o755)
		}
		return nil
	}
	makeICNS = func(ctx context.Context, path string) error {
		*call(calls) = append(*calls, "icns")
		return os.WriteFile(path, []byte("icns"), 0o644)
	}
}

func call(c *[]string) *[]string { return c }

func TestBuildAppNamesIconSignsAndIsReused(t *testing.T) {
	var calls []string
	fakeTools(t, &calls, "")
	dir := t.TempDir()
	exe, err := BuildApp(context.Background(), dir)
	if err != nil || exe != AppExecutable(dir) || !fileExists(exe) {
		t.Fatal(exe, err)
	}
	all := strings.Join(calls, "\n")
	for _, want := range []string{
		"osacompile -l JavaScript -o ", "icns",
		"-replace CFBundleName -string Shuttle Server", "-replace CFBundleIdentifier -string local.shuttle-server.menu",
		"-replace LSUIElement -bool YES", "-replace CFBundleIconFile -string applet", "codesign --force --sign - ",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Index(all, "plutil") > strings.Index(all, "codesign") {
		t.Error("must sign after the plist changes, or macOS refuses to run it")
	}
	if _, err := os.Stat(filepath.Join(dir, appFolder, "building")); !os.IsNotExist(err) {
		t.Error("scratch folder left behind")
	}
	n := len(calls)
	if _, err := BuildApp(context.Background(), dir); err != nil || len(calls) != n {
		t.Fatal("an up-to-date app must not be made again")
	}
	os.WriteFile(filepath.Join(dir, appFolder, appName, "Contents", "Resources", stampFile), []byte("old"), 0o644)
	if _, err := BuildApp(context.Background(), dir); err != nil || len(calls) == n {
		t.Fatal("an out-of-date app must be made again")
	}
}

func TestBuildAppFailureLeavesNothingHalfMade(t *testing.T) {
	var calls []string
	fakeTools(t, &calls, "codesign")
	dir := t.TempDir()
	if _, err := BuildApp(context.Background(), dir); err == nil {
		t.Fatal("a failed signing must be an error, so Run falls back to osascript")
	}
	if fileExists(AppExecutable(dir)) {
		t.Fatal("an unsigned app must never be put in place")
	}
}

func TestHelperReadsPathsFromEnvironment(t *testing.T) {
	for _, want := range []string{"envVar('SHUTTLE_STATUS')", "envVar('SHUTTLE_PID')", "argv[0] ||"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("menubar.js lacks %q", want)
		}
	}
}
