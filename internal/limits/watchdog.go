package limits

import (
	"context"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"time"
)

// MemState is the watchdog's verdict.
type MemState string

const (
	MemOK       MemState = "ok"
	MemOver     MemState = "over"     // start nothing new, pools shrunk
	MemCritical MemState = "critical" // stopping cleanly
)

// Watchdog checks the process's memory every few seconds. Over the ceiling it
// calls OnOver(true) (start nothing new, shrink pools); still over after
// Grace it calls OnCritical once (save state and stop). It never lets the
// process grow until the computer swaps.
type Watchdog struct {
	Ceiling    uint64
	Interval   time.Duration
	Grace      time.Duration
	Read       func() uint64
	OnOver     func(over bool)
	OnCritical func()

	mu        sync.Mutex
	state     MemState
	overSince time.Time
	last      uint64
}

// ApplyMemoryLimit sets the Go runtime's soft limit, so garbage collection
// works harder before the process grows.
func ApplyMemoryLimit(ceiling int64) { debug.SetMemoryLimit(ceiling) }

// GoMemory returns the memory the Go runtime holds from the system. With no
// cgo this is nearly all of the process.
func GoMemory() uint64 {
	s := []metrics.Sample{{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/released:bytes"}}
	metrics.Read(s)
	return s[0].Value.Uint64() - s[1].Value.Uint64()
}

// Run checks until ctx ends.
func (w *Watchdog) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.Check(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			w.Check(now)
		}
	}
}

// Check takes one reading. Exported for tests.
func (w *Watchdog) Check(now time.Time) {
	m := w.Read()
	w.mu.Lock()
	w.last = m
	prev := w.state
	switch {
	case m <= w.Ceiling && prev != MemCritical:
		w.state = MemOK
	case m > w.Ceiling && (prev == MemOK || prev == ""):
		w.state, w.overSince = MemOver, now
	case m > w.Ceiling && prev == MemOver && now.Sub(w.overSince) >= w.Grace:
		w.state = MemCritical
	}
	cur := w.state
	w.mu.Unlock()

	if cur == MemOver && prev != MemOver && w.OnOver != nil {
		w.OnOver(true)
	}
	if cur == MemOK && prev == MemOver && w.OnOver != nil {
		w.OnOver(false)
	}
	if cur == MemCritical && prev != MemCritical && w.OnCritical != nil {
		w.OnCritical()
	}
}

// Status returns the last reading and state.
func (w *Watchdog) Status() (uint64, MemState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state == "" {
		return w.last, MemOK
	}
	return w.last, w.state
}

// AcceptingNewWork is false while memory is over the ceiling. Every job
// scheduler asks this before starting anything.
func (w *Watchdog) AcceptingNewWork() bool {
	_, s := w.Status()
	return s == MemOK
}
