// Package menubar shows Shuttle Server's status as an icon in the macOS menu
// bar. The icon is a small helper run by macOS's own osascript (no install,
// no cgo). Shuttle writes a status file every two seconds; the helper reads
// it, and quits by itself if the file goes stale. Shuttle starts the helper
// in its own process group and stops all of it when Shuttle stops.
package menubar

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed menubar.js
var script []byte

// Line is what the icon shows.
type Line struct {
	Level    string // ok, warn or bad
	Headline string
	Version  string
	// Update is none, nokey, checking, downloading, installing, waiting, available or failed.
	Update        string
	UpdateVersion string
}

// Options for Run. Command is the helper; empty means osascript.
type Options struct {
	Dir     string // where the script and status file live (temp.noindex)
	URL     string
	Get     func() Line
	Log     *slog.Logger
	Command []string
	Every   time.Duration
}

// Supported is true where the icon can be shown.
func Supported() bool { return runtime.GOOS == "darwin" }

// StatusFile is the file the helper reads.
func StatusFile(dir string) string { return filepath.Join(dir, "menubar-status.txt") }

// Run writes the status file and keeps the helper running until ctx ends.
// If the helper keeps failing it gives up after three tries in ten minutes,
// says so in the log, and Shuttle carries on without the icon.
func Run(ctx context.Context, o Options) {
	if o.Every == 0 {
		o.Every = 2 * time.Second
	}
	status := StatusFile(o.Dir)
	write := func() {
		l := o.Get()
		body := fmt.Sprintf("%d\n%s\n%s\n%s\n%s\n%s\n%s\n", time.Now().Unix(), l.Level, oneLine(l.Headline), l.Version, o.URL, l.Update, l.UpdateVersion)
		// A display file, not state: rename keeps it whole, no flush needed.
		tmp := status + ".tmp"
		if os.WriteFile(tmp, []byte(body), 0o600) == nil {
			os.Rename(tmp, status)
		}
	}
	write()

	cmd := o.Command
	if cmd == nil {
		path := filepath.Join(o.Dir, "menubar.js")
		if err := os.WriteFile(path, script, 0o600); err != nil {
			o.Log.Warn("menu bar icon not shown: could not write its script", "err", err)
			return
		}
		cmd = []string{"/usr/bin/osascript", "-l", "JavaScript", path, status, strconv.Itoa(os.Getpid())}
	}

	exited := make(chan string, 1)
	var cur *exec.Cmd
	var mu sync.Mutex
	var starts []time.Time
	start := func() {
		now := time.Now()
		var recent []time.Time
		for _, t := range starts {
			if now.Sub(t) < 10*time.Minute {
				recent = append(recent, t)
			}
		}
		if len(recent) >= 3 {
			o.Log.Warn("menu bar icon stopped after failing three times; Shuttle carries on without it")
			starts = append(recent, now) // stays over the limit: no more tries
			return
		}
		starts = append(recent, now)
		c := exec.Command(cmd[0], cmd[1:]...)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // Ctrl-C reaches Shuttle only; Shuttle stops the helper
		tail := &tailBuffer{max: 2048}
		c.Stdout, c.Stderr = tail, tail
		if err := c.Start(); err != nil {
			o.Log.Warn("menu bar icon could not start", "err", err)
			return
		}
		mu.Lock()
		cur = c
		mu.Unlock()
		go func() {
			err := c.Wait()
			exited <- fmt.Sprintf("%v %s", err, strings.TrimSpace(tail.String()))
		}()
	}
	start()

	t := time.NewTicker(o.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			c := cur
			mu.Unlock()
			stop(c, exited)
			os.Remove(status) // our own display file
			return
		case <-t.C:
			write()
		case why := <-exited:
			mu.Lock()
			cur = nil
			mu.Unlock()
			o.Log.Warn("menu bar icon exited; restarting it", "detail", why)
			start()
		}
	}
}

// stop ends the helper's whole process group: politely, then firmly.
func stop(c *exec.Cmd, exited <-chan string) {
	if c == nil || c.Process == nil {
		return
	}
	syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		<-exited
	}
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }

// tailBuffer keeps only the last max bytes of the helper's output, so a noisy
// helper can never make memory grow.
type tailBuffer struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append([]byte(nil), t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
