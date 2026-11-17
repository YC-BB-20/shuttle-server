// Package lucid asks the LucidLink desktop client, through its own command,
// how much written data is still waiting to upload, and drops the client's
// cache so a read-back comes from LucidLink's storage rather than this
// computer (DESIGN.md section 19).
//
// What the command prints has not been seen on the owner's Mac yet, so the
// answer is read cautiously: anything Shuttle cannot read is an error, and
// an error never counts as "nothing waiting".
package lucid

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Timeout bounds every call to the client's command.
var Timeout = 20 * time.Second

func setTimeout(d time.Duration) { Timeout = d }

// maxOut caps what is kept of the command's output.
const maxOut = 64 << 10

// ErrNoCommand means the client's command was not found.
var ErrNoCommand = errors.New("Shuttle cannot find LucidLink's command on this computer")

// names are the client's command names: lucid3 for LucidLink 3, lucid before.
var names = []string{"lucid3", "lucid"}

// dirs are places the command is looked for besides the PATH.
var dirs = []string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin"}

// appGlobs are where the Mac app may keep it.
var appGlobs = []string{"/Applications/Lucid*.app/Contents/Resources", "/Applications/Lucid*.app/Contents/MacOS"}

// Find returns the client's command: set, if it is not empty and exists,
// otherwise the first one found. "" when there is none.
func Find(set string) string {
	if set != "" {
		if isExec(set) {
			return set
		}
		return ""
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	var places []string
	places = append(places, dirs...)
	for _, g := range appGlobs {
		m, _ := filepath.Glob(g)
		places = append(places, m...)
	}
	for _, d := range places {
		for _, n := range names {
			if p := filepath.Join(d, n); isExec(p) {
				return p
			}
		}
	}
	return ""
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// Client runs the command. Run is replaceable for tests.
type Client struct {
	Set string // the command named in the config; "" finds it
	Cmd string // the command found; looked for again while empty, so LucidLink installed later is found
	Run func(ctx context.Context, cmd string, args ...string) (string, error)
}

// New finds the command (set may name it) and returns a client.
func New(set string) *Client { return &Client{Set: set, Cmd: Find(set), Run: run} }

func (c *Client) cmd() string {
	if c.Cmd == "" {
		c.Cmd = Find(c.Set)
	}
	return c.Cmd
}

func run(ctx context.Context, cmd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmd, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // its own group, stopped completely
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 2 * time.Second
	var out capped
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	if ctx.Err() != nil {
		return out.String(), errors.New("LucidLink's command did not answer in time")
	}
	return out.String(), err
}

// capped keeps the first maxOut bytes written to it.
type capped struct{ b bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxOut - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
		} else {
			c.b.Write(p)
		}
	}
	return len(p), nil
}

func (c *capped) String() string { return c.b.String() }

// Waiting returns the bytes written to the filespace on this computer that
// LucidLink has not uploaded yet.
func (c *Client) Waiting(ctx context.Context) (int64, error) {
	if c == nil || c.cmd() == "" {
		return 0, ErrNoCommand
	}
	out, err := c.Run(ctx, c.Cmd, "perf", "--cache", "dirtyBytes")
	if err != nil {
		return 0, fmt.Errorf("LucidLink's command could not say what is left to upload (%v)", short(err, out))
	}
	n, ok := parseDirty(out)
	if !ok {
		return 0, fmt.Errorf("Shuttle could not read LucidLink's answer about what is left to upload: %q", firstLine(out))
	}
	return n, nil
}

// Drop clears the client's cache of data already uploaded. LucidLink never
// drops data still waiting to upload.
func (c *Client) Drop(ctx context.Context) error {
	if c == nil || c.cmd() == "" {
		return ErrNoCommand
	}
	if out, err := c.Run(ctx, c.Cmd, "cache", "--drop"); err != nil {
		return fmt.Errorf("LucidLink's command could not clear its cache (%v)", short(err, out))
	}
	return nil
}

func short(err error, out string) string {
	if l := firstLine(out); l != "" {
		return l
	}
	return err.Error()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

var (
	// "dirtyBytes: 1234", "dirtyBytes 1.5 GiB", "dirtyBytes = 0"
	afterName = regexp.MustCompile(`(?i)dirty\s*bytes\s*[:=]?\s*([0-9][0-9,.]*)\s*([kmgtp]i?b|b|bytes)?\b`)
	// a line holding only a number, with or without a unit
	alone = regexp.MustCompile(`(?im)^\s*([0-9][0-9,.]*)\s*([kmgtp]i?b|b|bytes)?\s*$`)
)

// parseDirty reads the waiting bytes from the command's output. It accepts
// a number after the word dirtyBytes, or the output's only number standing
// on a line of its own; anything else is not understood.
func parseDirty(out string) (int64, bool) {
	if m := afterName.FindStringSubmatch(out); m != nil {
		return toBytes(m[1], m[2])
	}
	if ms := alone.FindAllStringSubmatch(out, -1); len(ms) == 1 {
		return toBytes(ms[0][1], ms[0][2])
	}
	return 0, false
}

func toBytes(num, unit string) (int64, bool) {
	num = strings.ReplaceAll(num, ",", "")
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 {
		return 0, false
	}
	mult := map[string]float64{"": 1, "b": 1, "bytes": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "pib": 1 << 50}[strings.ToLower(unit)]
	if mult == 0 {
		return 0, false
	}
	if unit == "" && strings.Contains(num, ".") {
		return 0, false // a fraction of a byte is not a byte count
	}
	return int64(f * mult), true
}
