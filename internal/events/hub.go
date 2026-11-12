// Package events pushes status to open pages. Updates are coalesced: however
// often status changes, each page gets at most a few sends a second, and only
// the latest. A page that stops reading (a sleeping phone) is dropped, so it
// can never make memory grow; it catches up when it reconnects.
package events

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrTooMany means the cap on open pages is reached.
var ErrTooMany = errors.New("too many pages open")

// Hub fans the latest snapshot out to subscribers.
type Hub struct {
	mu        sync.Mutex
	clients   map[*Client]struct{}
	max       int
	latest    []byte
	seq       uint64
	interval  time.Duration
	dropAfter int
}

// Client is one open page.
type Client struct {
	C       chan []byte   // at most one pending message
	Done    chan struct{} // closed when the hub drops this page
	sent    uint64
	misses  int
	dropped bool
}

// NewHub sends at most perSecond updates a second to at most maxPages pages.
// A page that has not taken a message for ten seconds is dropped.
func NewHub(perSecond, maxPages int) *Hub {
	return &Hub{clients: map[*Client]struct{}{}, max: maxPages,
		interval: time.Second / time.Duration(perSecond), dropAfter: 10 * perSecond}
}

// Publish replaces the latest snapshot. It never blocks.
func (h *Hub) Publish(b []byte) {
	h.mu.Lock()
	h.latest = b
	h.seq++
	h.mu.Unlock()
}

// Subscribe adds a page; the latest snapshot goes out on the next tick.
func (h *Hub) Subscribe() (*Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.max {
		return nil, ErrTooMany
	}
	c := &Client{C: make(chan []byte, 1), Done: make(chan struct{})}
	h.clients[c] = struct{}{}
	return c, nil
}

// Unsubscribe removes a page that went away.
func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Open is the number of pages connected.
func (h *Hub) Open() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Max is the cap on open pages.
func (h *Hub) Max() int { return h.max }

// Run ticks until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Tick()
		}
	}
}

// Tick sends the latest snapshot to every page that has not seen it.
// Exported for tests.
func (h *Hub) Tick() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.latest == nil {
		return
	}
	for c := range h.clients {
		if c.sent == h.seq {
			continue
		}
		select {
		case c.C <- h.latest:
			c.sent, c.misses = h.seq, 0
		default:
			if c.misses++; c.misses >= h.dropAfter {
				delete(h.clients, c)
				c.dropped = true
				close(c.Done)
			}
		}
	}
}
