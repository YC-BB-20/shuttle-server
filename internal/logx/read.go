package logx

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Line is one log line split into its parts, for the page (v0.2.87).
type Line struct {
	Time  string `json:"t"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	More  string `json:"more,omitempty"` // the other key=value pairs, as written
}

// tailBytes is how much of each month's log is read from its end: enough for
// any one job's lines, small enough that a large log never slows the page.
const tailBytes = 32 << 20

// Find returns the last max lines containing key=value exactly (not as the
// start of a longer value), from the months since `since` (at most four),
// oldest first. Nothing is written; each file is read only from its last
// 32 MB.
func Find(logsDir string, since, now time.Time, key, value string, max int) []Line {
	want := key + "=" + value
	var out []Line
	m := time.Date(since.Year(), since.Month(), 1, 0, 0, 0, 0, time.Local)
	if now.Sub(m) > 120*24*time.Hour {
		m = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -3, 0)
	}
	for ; !m.After(now); m = m.AddDate(0, 1, 0) {
		f, err := os.Open(filepath.Join(logsDir, m.Format("2006-01"), "shuttle.log"))
		if err != nil {
			continue
		}
		if st, err := f.Stat(); err == nil && st.Size() > tailBytes {
			f.Seek(st.Size()-tailBytes, io.SeekStart)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			if key != "" { // an empty key matches every line: the whole log's tail
				i := strings.Index(l, want)
				if i < 0 || i > 0 && l[i-1] != ' ' {
					continue
				}
				if e := i + len(want); e < len(l) && l[e] != ' ' {
					continue
				}
			}
			out = append(out, Parse(l))
			if len(out) > max {
				out = out[1:]
			}
		}
		f.Close()
	}
	return out
}

// Parse splits a line written by slog's text handler.
func Parse(l string) Line {
	var x Line
	var more []string
	for _, kv := range splitPairs(l) {
		k, v, _ := strings.Cut(kv, "=")
		uq := strings.Trim(v, `"`)
		uq = strings.ReplaceAll(uq, `\"`, `"`)
		switch k {
		case "time":
			if t, err := time.Parse(time.RFC3339Nano, uq); err == nil {
				x.Time = t.Format(time.RFC3339)
			}
		case "level":
			x.Level = uq
		case "msg":
			x.Msg = uq
		case "job":
		default:
			more = append(more, k+"="+uq)
		}
	}
	x.More = strings.Join(more, " · ")
	return x
}

// splitPairs splits on spaces outside double quotes.
func splitPairs(l string) []string {
	var out []string
	var b strings.Builder
	q, esc := false, false
	for _, r := range l {
		switch {
		case esc:
			esc = false
		case r == '\\':
			esc = true
		case r == '"':
			q = !q
		case r == ' ' && !q:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
