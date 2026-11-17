package copyjob

import (
	"errors"
	"slices"
	"time"
)

// Queue order (v0.2.85). The queue runs in the order of m.jobs. A job's
// place is its creation time until the owner moves it; then its own Order
// is saved in job.json. Only the moved job's file is written, so a move is
// one atomic save, and a crash keeps either the old place or the new one.

// orderKey is a job's place in the queue: lower runs first.
func orderKey(st state) int64 {
	if st.Order != 0 {
		return st.Order
	}
	return st.Created.UnixNano()
}

func keyOf(j *Job) int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return orderKey(j.st)
}

// sortQueue puts jobs in queue order; equal places keep creation order.
func sortQueue(jobs []*Job) {
	keys := make(map[*Job]int64, len(jobs))
	for _, j := range jobs {
		keys[j] = keyOf(j)
	}
	slices.SortStableFunc(jobs, func(a, b *Job) int {
		switch ka, kb := keys[a], keys[b]; {
		case ka < kb:
			return -1
		case ka > kb:
			return 1
		}
		return a.st.Created.Compare(b.st.Created)
	})
}

var errNotMovable = errors.New("only a job that has not started can be moved: running and finished jobs keep their place")

// Move puts job id right before the job before, or at the end of the queue
// when before is empty. The job running now, and finished jobs, cannot be
// moved; a job may be put before any job that has not finished. Moving
// never interrupts the running job: the new order counts from the next pick.
func (m *Manager) Move(id, before string) error {
	if id == before {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.findLocked(id)
	if j == nil {
		return errors.New("no such job")
	}
	if j == m.running || m.doneLocked(j) {
		return errNotMovable
	}
	rest := slices.DeleteFunc(slices.Clone(m.jobs), func(x *Job) bool { return x == j })
	var key int64
	if before == "" {
		key = time.Now().UnixNano()
		if n := len(rest); n > 0 {
			key = max(key, keyOf(rest[n-1])+1)
		}
	} else {
		b := m.findLocked(before)
		if b == nil {
			return errors.New("no such job to move before")
		}
		if m.doneLocked(b) {
			return errors.New("a job can only be moved before one that has not finished")
		}
		i := slices.Index(rest, b)
		next := keyOf(b)
		prev := next - 2e9 // nothing before: two seconds ahead of it
		if i > 0 {
			prev = keyOf(rest[i-1])
		}
		if next-prev < 2 {
			// No room between the two (moved there many times): spread the
			// unfinished jobs out again, one save each, then place it.
			if err := m.respaceLocked(rest); err != nil {
				return err
			}
			next = keyOf(b)
			prev = next - 2e9
			if i > 0 {
				prev = keyOf(rest[i-1])
			}
		}
		key = prev + (next-prev)/2
	}
	j.mu.Lock()
	old := j.st.Order
	j.st.Order = key
	j.mu.Unlock()
	if err := j.save(); err != nil {
		j.mu.Lock()
		j.st.Order = old
		j.mu.Unlock()
		return err
	}
	sortQueue(m.jobs)
	m.Log.Info("owner moved a job in the queue", "job", id, "before", before)
	m.poke()
	return nil
}

// respaceLocked gives every job in jobs, in their present order, a place a
// second apart. m.mu is held.
func (m *Manager) respaceLocked(jobs []*Job) error {
	base := time.Now().UnixNano() - int64(len(jobs))*1e9
	for i, x := range jobs {
		x.mu.Lock()
		x.st.Order = base + int64(i)*1e9
		x.mu.Unlock()
		if err := x.save(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) findLocked(id string) *Job {
	for _, j := range m.jobs {
		if j.st.ID == id {
			return j
		}
	}
	return nil
}

func (m *Manager) doneLocked(j *Job) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.st.Phase == "done"
}
