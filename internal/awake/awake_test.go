package awake

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHoldFollowsTheQueue(t *testing.T) {
	var busy atomic.Bool
	var mu sync.Mutex
	running, starts := 0, 0
	k := &Keeper{Busy: busy.Load, Every: 5 * time.Millisecond, start: func(ctx context.Context) error {
		mu.Lock()
		running++
		starts++
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		running--
		mu.Unlock()
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { k.Run(ctx); close(done) }()
	wait := func(want int) {
		t.Helper()
		for i := 0; i < 400; i++ {
			mu.Lock()
			r := running
			mu.Unlock()
			if r == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("holds running: want %d", want)
	}
	wait(0)
	busy.Store(true)
	wait(1)
	time.Sleep(30 * time.Millisecond) // still busy: one hold, not one per check
	mu.Lock()
	if starts != 1 {
		t.Fatalf("%d holds started", starts)
	}
	mu.Unlock()
	busy.Store(false)
	wait(0)
	busy.Store(true)
	wait(1)
	cancel()
	<-done
	wait(0) // Shuttle stopping lets the computer sleep
	if k.Holding() {
		t.Fatal("still holding after Run ended")
	}
}
