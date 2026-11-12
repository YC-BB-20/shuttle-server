// Package limits keeps the app inside fixed limits so a fault can never
// freeze the computer (DESIGN.md section 18).
package limits

import (
	"container/list"
	"context"
	"errors"
	"sync"
)

// ErrTooLarge means a request can never fit in the budget.
var ErrTooLarge = errors.New("request larger than the whole buffer budget")

// Budget is the one shared pool every byte of file data in memory comes from.
// A caller that finds it empty waits; it never allocates more. Waiters are
// served first come, first served, so a large request is not starved.
type Budget struct {
	mu      sync.Mutex
	total   int64
	limit   int64
	used    int64
	waiters list.List // of *waiter
}

type waiter struct {
	n     int64
	ready chan struct{}
}

// Lease is memory taken from the budget. Release it exactly once.
type Lease struct {
	B []byte
	n int64
}

// NewBudget makes a budget of total bytes.
func NewBudget(total int64) *Budget { return &Budget{total: total, limit: total} }

// Acquire waits until n bytes fit, or ctx ends.
func (b *Budget) Acquire(ctx context.Context, n int64) (Lease, error) {
	if n <= 0 || n > b.total {
		return Lease{}, ErrTooLarge
	}
	b.mu.Lock()
	if b.waiters.Len() == 0 && b.used+n <= b.limit {
		b.used += n
		b.mu.Unlock()
		return Lease{B: make([]byte, n), n: n}, nil
	}
	w := &waiter{n: n, ready: make(chan struct{})}
	el := b.waiters.PushBack(w)
	b.mu.Unlock()

	select {
	case <-w.ready:
		return Lease{B: make([]byte, n), n: n}, nil
	case <-ctx.Done():
		b.mu.Lock()
		select {
		case <-w.ready: // granted while cancelling: give it back
			b.used -= n
		default:
			b.waiters.Remove(el)
		}
		b.notifyLocked()
		b.mu.Unlock()
		return Lease{}, ctx.Err()
	}
}

// Release returns a lease's bytes to the budget.
func (b *Budget) Release(l Lease) {
	if l.n == 0 {
		return
	}
	b.mu.Lock()
	b.used -= l.n
	b.notifyLocked()
	b.mu.Unlock()
}

// Shrink lowers the usable limit (the memory watchdog does this). Leases
// already out are not taken back; new ones wait until usage is under it.
func (b *Budget) Shrink(limit int64) {
	b.mu.Lock()
	if limit < b.total {
		b.limit = limit
	}
	b.mu.Unlock()
}

// Restore lifts the limit back to the full budget.
func (b *Budget) Restore() {
	b.mu.Lock()
	b.limit = b.total
	b.notifyLocked()
	b.mu.Unlock()
}

func (b *Budget) notifyLocked() {
	for e := b.waiters.Front(); e != nil; e = b.waiters.Front() {
		w := e.Value.(*waiter)
		if b.used+w.n > b.limit {
			return
		}
		b.used += w.n
		b.waiters.Remove(e)
		close(w.ready)
	}
}

// BudgetStats is what the page shows.
type BudgetStats struct {
	Total   int64 `json:"total"`
	Limit   int64 `json:"limit"`
	Used    int64 `json:"used"`
	Waiting int   `json:"waiting"`
}

// Stats reports the budget's state. Waiting > 0 is shown as "waiting for
// memory buffers".
func (b *Budget) Stats() BudgetStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetStats{b.total, b.limit, b.used, b.waiters.Len()}
}
