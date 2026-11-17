package copyjob

import (
	"errors"
	"strings"
)

// Pause and ask after errors (v0.2.99, owner 17 November 2026, DESIGN.md
// section 7): a copy that ends with failed files does not slip quietly into
// the history. It waits for the owner's answer, try the failed files again
// or leave them failed. Only that job waits: the rest of the queue carries
// on, overnight too, and the copy's own Verify item waits with it.
// Answering changes nothing on disk; proven files are never touched.

// ErrorsAsk is the question: how many failed, and the most common reason.
type ErrorsAsk struct {
	Failed int    `json:"failed"`
	Reason string `json:"reason,omitempty"` // the most common error, as the files' rows show it
	Same   int    `json:"same,omitempty"`   // how many of the failed files had that error
}

var errNoQuestion = errors.New("this copy is not waiting for an answer")

// askAboutErrors holds a copy that has just finished with failed files.
func (m *Manager) askAboutErrors(j *Job) {
	j.mu.Lock()
	sp, failed, stopped := j.st.Spec, j.c.Failed, j.st.Choice == "stopped"
	j.mu.Unlock()
	if failed == 0 || stopped || sp.isVerify() || sp.isLink() {
		return
	}
	reason, same := m.commonFailure(j)
	j.mu.Lock()
	j.st.ErrorsAsk = &ErrorsAsk{Failed: failed, Reason: reason, Same: min(same, failed)}
	j.mu.Unlock()
	if err := j.save(); err != nil {
		m.Log.Error("could not save the question about failed files", "job", j.st.ID, "err", err)
	}
	m.Log.Warn("copy finished with failed files; it waits for an answer, the rest of the queue carries on",
		"job", j.st.ID, "failed", failed, "most_common", reason, "of_them", same)
	m.poke()
}

// KeepFailed answers the question with "leave them failed": the copy goes
// to the history as it is, and its Verify item may start. The failed files
// can still be tried again later from the copy's Problems tab.
func (m *Manager) KeepFailed(id string) error {
	j := m.find(id)
	if j == nil {
		return errors.New("no such job")
	}
	j.mu.Lock()
	if j.st.ErrorsAsk == nil {
		j.mu.Unlock()
		return errNoQuestion
	}
	n := j.st.ErrorsAsk.Failed
	j.st.ErrorsAsk = nil
	j.mu.Unlock()
	if err := j.save(); err != nil {
		return err
	}
	m.Log.Info("owner left the failed files as they are", "job", id, "failed", n)
	m.poke()
	return nil
}

// maxReasons bounds the table of distinct errors while counting, so a job
// whose every error text differs cannot make it grow with the files.
const maxReasons = 64

// commonFailure reads the journal for the error most of the still-failed
// files share. A file that failed more than once may count more than once.
func (m *Manager) commonFailure(j *Job) (string, int) {
	count := map[string]int{}
	ReadJournal(j.path("journal.log"), 0, func(r Rec) bool {
		if r.St != "failed" || r.Err == "" {
			return true
		}
		j.mu.Lock()
		still := r.I >= 0 && r.I < len(j.status) && j.status[r.I] == stFailed
		j.mu.Unlock()
		if !still {
			return true
		}
		k := reasonKey(r.Err)
		if _, ok := count[k]; ok || len(count) < maxReasons {
			count[k]++
		}
		return true
	})
	best, n := "", 0
	for k, c := range count {
		if c > n || c == n && k < best {
			best, n = k, c
		}
	}
	return best, n
}

// reasonKey is an error without the file's own path, so files that failed
// the same way group together: "open /Volumes/A/x.mov: permission denied"
// becomes "permission denied".
func reasonKey(e string) string {
	if i := strings.LastIndex(e, ": "); i > 0 && strings.Contains(e[:i], "/") {
		e = e[i+2:]
	}
	e = strings.TrimSpace(e)
	if r := []rune(e); len(r) > 160 {
		e = string(r[:160]) + "…"
	}
	return e
}
