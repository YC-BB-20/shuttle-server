// Package diskio is every byte Shuttle reads or writes on a user's disk. It
// keeps reads out of the system cache, retries short reads, limits the work
// in flight per disk, and notices a disk that stops answering.
package diskio

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrStalled: an operation on this disk has not returned for too long.
	ErrStalled = errors.New("disk not responding")
	// ErrGone: the disk was unplugged or is no longer the same disk.
	ErrGone = errors.New("disk is not connected")
)

// Disk is one volume. All work on it goes through Do, so the limit on
// operations in flight and the stall timer cover every job type.
type Disk struct {
	Name       string
	Root       string
	ID         string // volume identifier
	stallAfter time.Duration
	slots      chan struct{}

	mu      sync.Mutex
	ops     map[uint64]time.Time
	next    uint64
	stalled bool
}

// NewDisk makes a disk with limit operations in flight.
func NewDisk(name, root, id string, limit int, stallAfter time.Duration) *Disk {
	return &Disk{Name: name, Root: root, ID: id, stallAfter: stallAfter,
		slots: make(chan struct{}, limit), ops: map[uint64]time.Time{}}
}

// Do runs op when a slot is free. While the disk is marked stalled it
// returns ErrStalled at once rather than queueing more work behind a hung
// read, which could never be cancelled.
func (d *Disk) Do(ctx context.Context, op func() error) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if d.Stalled() {
			return ErrStalled
		}
		select {
		case d.slots <- struct{}{}:
			goto run
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
run:
	d.mu.Lock()
	id := d.next
	d.next++
	d.ops[id] = time.Now()
	d.mu.Unlock()
	err := op()
	d.mu.Lock()
	delete(d.ops, id)
	d.mu.Unlock()
	<-d.slots
	return err
}

// Check marks the disk stalled if an operation has run longer than the
// limit, and clears the mark once nothing is hung. Called every few seconds.
func (d *Disk) Check(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	hung := false
	for _, t := range d.ops {
		if now.Sub(t) > d.stallAfter {
			hung = true
		}
	}
	d.stalled = hung
}

// Stalled reports whether the disk is marked not responding.
func (d *Disk) Stalled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stalled
}

// Registry finds the Disk for a path, so two jobs on one volume share its
// limit, and runs the stall checks.
type Registry struct {
	mu         sync.Mutex
	disks      map[string]*Disk
	limit      int
	stallAfter time.Duration
}

// NewRegistry makes a registry. stallAfter is the stall timer (60 s).
func NewRegistry(limit int, stallAfter time.Duration) *Registry {
	return &Registry{disks: map[string]*Disk{}, limit: limit, stallAfter: stallAfter}
}

// For returns the disk holding path.
func (r *Registry) For(path string) (*Disk, error) {
	v, err := VolumeOf(path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.disks[v.ID]; ok {
		return d, nil
	}
	d := NewDisk(v.Name, v.Mount, v.ID, r.limit, r.stallAfter)
	r.disks[v.ID] = d
	return d, nil
}

// Run checks every disk every few seconds until ctx ends.
func (r *Registry) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.mu.Lock()
			for _, d := range r.disks {
				d.Check(now)
			}
			r.mu.Unlock()
		}
	}
}
