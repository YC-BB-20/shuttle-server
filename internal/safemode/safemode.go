// Package safemode stops crash loops. Every start is recorded before anything
// risky runs; a start that survives a minute, or a clean quit, clears the
// record. Three starts within five minutes that never got that far mean
// safe mode: the page opens and says what is wrong, and nothing runs.
package safemode

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

const (
	MaxCrashes  = 3
	Window      = 5 * time.Minute
	StableAfter = time.Minute
)

// Record is startup.json in the state folder.
type Record struct {
	FormatVersion int         `json:"format_version"`
	Attempts      []time.Time `json:"attempts"`
	LastError     string      `json:"last_error,omitempty"`
	LastErrorAt   time.Time   `json:"last_error_at,omitzero"`
}

// Tracker holds the record for this run.
type Tracker struct {
	path string
	rec  Record
	Note string // set when a damaged record was set aside
}

// Open loads the record, decides on safe mode, and, if not in safe mode,
// records this start. A damaged record is renamed aside, never deleted.
func Open(path string, now time.Time) (t *Tracker, safe bool, reason string, err error) {
	t = &Tracker{path: path, rec: Record{FormatVersion: 1}}
	if b, rerr := os.ReadFile(path); rerr == nil {
		if json.Unmarshal(b, &t.rec) != nil {
			aside := path + ".damaged-" + strconv.FormatInt(now.Unix(), 10)
			os.Rename(path, aside)
			t.rec = Record{FormatVersion: 1}
			t.Note = "startup.json was damaged and was set aside as " + aside
		}
	}
	var recent []time.Time
	for _, a := range t.rec.Attempts {
		if now.Sub(a) < Window && !a.After(now) {
			recent = append(recent, a)
		}
	}
	t.rec.Attempts = recent
	if len(recent) >= MaxCrashes {
		reason = fmt.Sprintf("Shuttle Server stopped %d times while starting, within %v.", len(recent), Window)
		if t.rec.LastError != "" {
			reason += " Last error: " + t.rec.LastError
		}
		return t, true, reason, nil
	}
	t.rec.Attempts = append(t.rec.Attempts, now)
	return t, false, "", t.save()
}

// Stable clears the start attempts: this run got through start-up.
func (t *Tracker) Stable() error { t.rec.Attempts = nil; return t.save() }

// Forget removes this run's attempt, for a start that failed for a reason
// that is not a crash (the port already in use).
func (t *Tracker) Forget() error {
	if n := len(t.rec.Attempts); n > 0 {
		t.rec.Attempts = t.rec.Attempts[:n-1]
	}
	return t.save()
}

// RecordError keeps the last start-up error for the safe-mode message.
func (t *Tracker) RecordError(msg string, now time.Time) error {
	t.rec.LastError, t.rec.LastErrorAt = msg, now
	return t.save()
}

// Reset leaves safe mode: the owner asked for it with -clear-safe-mode.
func Reset(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	var r Record
	if err != nil || json.Unmarshal(b, &r) != nil {
		r = Record{FormatVersion: 1}
	}
	r.Attempts = nil
	out, _ := json.MarshalIndent(r, "", "  ")
	return fsx.WriteFileAtomic(path, out, 0o600)
}

func (t *Tracker) save() error {
	out, _ := json.MarshalIndent(t.rec, "", "  ")
	return fsx.WriteFileAtomic(t.path, out, 0o600)
}
