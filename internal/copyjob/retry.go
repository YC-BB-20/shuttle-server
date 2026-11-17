package copyjob

import (
	"errors"
	"time"
)

// Retry failed (v0.2.92, DESIGN.md section 7): a finished copy's failed
// files go back into the same job, which goes to the end of the queue and
// copies only them, with the same proof as before. A "retry" record per
// file in the journal puts the file back to waiting, so a crash keeps
// either the old failure or the retry, and the old failure stays in the
// file's history. Proven files are not touched. Nothing at the destination
// is removed or overwritten: a failed copy that was set aside keeps its
// name, and a file in the way still fails without being touched.

var errNotRetryable = errors.New("only a finished copy with failed files can try them again")

// retried says whether file i was failed once and is no longer: it was put
// back in the queue and is waiting, or copied since.
func (j *Job) retried(i int) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.st.Retries > 0 && i >= 0 && i < len(j.status) && j.status[i] != stFailed
}

// RetryFailed puts copy id's failed files (only file, when file >= 0) back
// in its queue. It returns how many.
func (m *Manager) RetryFailed(id string, file int) (int, error) {
	j := m.find(id)
	if j == nil {
		return 0, errors.New("no such job")
	}
	// A finished copy has closed its journal; the queue may still be
	// tidying up after it for a moment, which a retry does not disturb.
	j.mu.Lock()
	sp := j.st.Spec
	ok := !sp.isVerify() && !sp.isLink() && j.st.Phase == "done" && j.st.Choice != "stopped" && j.status != nil
	var idx []int
	if ok {
		for i, s := range j.status {
			if s == stFailed && (file < 0 || i == file) {
				idx = append(idx, i)
			}
		}
	}
	j.mu.Unlock()
	if !ok || len(idx) == 0 {
		return 0, errNotRetryable
	}
	jr, _, err := OpenJournal(j.path("journal.log"), func(Rec) {})
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	for _, i := range idx {
		jr.Append(Rec{I: i, St: "retry", T: now})
	}
	err = jr.Close() // flushed before the job is saved as waiting
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	var last int64
	for _, o := range m.jobs {
		last = max(last, keyOf(o))
	}
	m.mu.Unlock()
	j.mu.Lock()
	for _, i := range idx {
		j.apply(Rec{I: i, St: "retry"})
	}
	// Back to the copy phase; the check and source passes count their
	// journal again, so the new copies are included.
	j.st.Phase, j.st.Finished, j.st.Choice, j.st.Error = "copy", time.Time{}, "run", ""
	j.st.CheckEnd, j.st.S2JournalEnd, j.st.Checkpoints = 0, 0, nil
	j.st.Order = last + 1e9 // the end of the queue: the owner's order stays as it was
	j.st.Retries++
	j.st.Hidden = false
	j.mu.Unlock()
	if err := j.save(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	sortQueue(m.jobs)
	m.mu.Unlock()
	m.Log.Info("failed files put back in the queue", "job", id, "files", len(idx))
	m.poke()
	return len(idx), nil
}
