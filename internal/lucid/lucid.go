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
	"sync"
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
	// Stream runs a command that keeps printing (LucidLink 3's "perf" prints
	// a row every 2 seconds and never stops) until done is true of the
	// complete lines so far, then stops it. nil: Run is used (v0.2.103).
	Stream func(ctx context.Context, done func(string) bool, cmd string, args ...string) (string, error)
}

// New finds the command (set may name it) and returns a client.
func New(set string) *Client { return &Client{Set: set, Cmd: Find(set), Run: run, Stream: runUntil} }

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

// runUntil is run for a command that keeps printing: once done is true of
// the complete lines printed so far, the command's whole process group is
// stopped and those lines are returned.
func runUntil(ctx context.Context, done func(string) bool, cmd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	stop, stopped := context.WithCancel(ctx)
	defer stopped()
	c := exec.CommandContext(stop, cmd, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 2 * time.Second
	out := &capped{done: done, stop: stopped}
	c.Stdout, c.Stderr = out, out
	err := c.Run()
	if lines, ok := out.answered(); ok {
		return lines, nil
	}
	if ctx.Err() != nil {
		return out.String(), errors.New("LucidLink's command did not answer in time")
	}
	return out.String(), err
}

// capped keeps the first maxOut bytes written to it. With done set, it
// calls stop once done is true of the complete lines so far.
type capped struct {
	mu   sync.Mutex
	b    bytes.Buffer
	done func(string) bool
	stop func()
	hit  string
	ok   bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := maxOut - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
		} else {
			c.b.Write(p)
		}
	}
	if c.done != nil && !c.ok {
		s := c.b.String()
		if lines := s[:strings.LastIndexByte(s, '\n')+1]; lines != "" && c.done(lines) {
			c.hit, c.ok = lines, true
			c.stop()
		}
	}
	return len(p), nil
}

func (c *capped) answered() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hit, c.ok
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

// Waiting returns the bytes written to the filespace on this computer that
// LucidLink has not uploaded yet.
func (c *Client) Waiting(ctx context.Context) (int64, error) {
	if c == nil || c.cmd() == "" {
		return 0, ErrNoCommand
	}
	var out string
	var err error
	if c.Stream != nil {
		out, err = c.Stream(ctx, func(s string) bool { _, ok := parseDirty(s); return ok }, c.Cmd, "perf", "--cache", "dirtyBytes")
	} else {
		out, err = c.Run(ctx, c.Cmd, "perf", "--cache", "dirtyBytes")
	}
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
	// "dirtyBytes: 1234", "dirtyBytes 1.5 GiB", "dirtyBytes = 0", on one line
	afterName = regexp.MustCompile(`(?i)dirty[ \t]*bytes[ \t]*[:=]?[ \t]*([0-9][0-9,.]*)[ \t]*([kmgtp]i?b|b|bytes)?\b`)
	// one table cell: "0B", "1.5GiB", "512 KiB"
	cell = regexp.MustCompile(`(?i)^([0-9][0-9,.]*)([kmgtp]i?b|b|bytes)?$`)
	// a line holding only a number, with or without a unit
	alone = regexp.MustCompile(`(?im)^\s*([0-9][0-9,.]*)\s*([kmgtp]i?b|b|bytes)?\s*$`)
)

// parseDirty reads the waiting bytes from the command's output. It accepts
// a number after the word dirtyBytes, or the output's only number standing
// on a line of its own; anything else is not understood.
func parseDirty(out string) (int64, bool) {
	if n, ok, table := parseTable(out); table {
		return n, ok
	}
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

// parseTable reads LucidLink 3's "perf" table (v0.2.103):
//
//	-- time --             -- cache --
//	currentDateAndTimeUTC  dirtyBytes
//	2026-11-18T00:42:41Z   0B
//
// table is true when a heading row names dirtyBytes as a column of its own;
// then only the first row under it counts, and it must read cleanly.
func parseTable(out string) (n int64, ok, table bool) {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		f := strings.Fields(l)
		col, numbers := -1, false
		for k, x := range f {
			if strings.EqualFold(x, "dirtyBytes") {
				col = k
			}
			numbers = numbers || cell.MatchString(x) || x == "=" || x == ":"
		}
		if col < 0 || len(f) < 2 || numbers {
			continue // not a heading row: one name, or a name with its value
		}
		for _, row := range lines[i+1:] {
			r := strings.Fields(row)
			if len(r) == 0 {
				continue
			}
			if len(r) != len(f) {
				return 0, false, true
			}
			m := cell.FindStringSubmatch(r[col])
			if m == nil {
				return 0, false, true
			}
			n, ok = toBytes(m[1], m[2])
			return n, ok, true
		}
		return 0, false, true // the heading, no row yet
	}
	return 0, false, false
}
