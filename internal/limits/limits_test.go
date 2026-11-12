package limits

import (
	"context"
	"testing"
	"time"
)

func TestBudgetBlocksWhenFullAndNeverExceeds(t *testing.T) {
	b := NewBudget(100)
	a, err := b.Acquire(context.Background(), 60)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan Lease)
	go func() { l, _ := b.Acquire(context.Background(), 60); got <- l }()
	select {
	case <-got:
		t.Fatal("second lease granted beyond the budget")
	case <-time.After(50 * time.Millisecond):
	}
	if s := b.Stats(); s.Waiting != 1 || s.Used != 60 {
		t.Fatalf("stats %+v", s)
	}
	b.Release(a)
	l := <-got
	if len(l.B) != 60 || b.Stats().Used != 60 {
		t.Fatal("waiter not served after release")
	}
}

func TestBudgetCancelAndTooLarge(t *testing.T) {
	b := NewBudget(10)
	if _, err := b.Acquire(context.Background(), 11); err != ErrTooLarge {
		t.Fatal("oversize accepted")
	}
	l, _ := b.Acquire(context.Background(), 10)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := b.Acquire(ctx, 5); err == nil {
		t.Fatal("expected timeout")
	}
	b.Release(l)
	if s := b.Stats(); s.Used != 0 || s.Waiting != 0 {
		t.Fatalf("leaked: %+v", s)
	}
}

func TestBudgetFIFOAndShrink(t *testing.T) {
	b := NewBudget(100)
	b.Shrink(40)
	if _, err := b.Acquire(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := b.Acquire(ctx, 30); err == nil {
		t.Fatal("shrunk limit ignored")
	}
	b.Restore()
	if _, err := b.Acquire(context.Background(), 70); err != nil {
		t.Fatal(err)
	}
}

func TestWatchdogStages(t *testing.T) {
	var mem uint64 = 50
	var overCalls []bool
	critical := 0
	w := &Watchdog{Ceiling: 100, Grace: time.Minute, Read: func() uint64 { return mem },
		OnOver: func(o bool) { overCalls = append(overCalls, o) }, OnCritical: func() { critical++ }}
	t0 := time.Now()
	w.Check(t0)
	mem = 150
	w.Check(t0.Add(5 * time.Second))
	if w.AcceptingNewWork() {
		t.Fatal("still accepting work over the ceiling")
	}
	mem = 80
	w.Check(t0.Add(10 * time.Second))
	if !w.AcceptingNewWork() || len(overCalls) != 2 || overCalls[1] {
		t.Fatalf("recovery not reported: %v", overCalls)
	}
	mem = 150
	w.Check(t0.Add(20 * time.Second))
	w.Check(t0.Add(50 * time.Second))
	if critical != 0 {
		t.Fatal("critical before grace")
	}
	w.Check(t0.Add(81 * time.Second))
	w.Check(t0.Add(90 * time.Second))
	if critical != 1 {
		t.Fatalf("critical called %d times", critical)
	}
	if GoMemory() == 0 {
		t.Fatal("GoMemory read nothing")
	}
}

func TestProcs(t *testing.T) {
	for _, c := range []struct{ n, want int }{{1, 1}, {2, 2}, {4, 2}, {18, 9}} {
		if got := Procs(c.n, 0.5); got != c.want {
			t.Errorf("Procs(%d)=%d want %d", c.n, got, c.want)
		}
	}
	if r := ApplyCPU(0.5, 0); r.NiceError != "" {
		t.Errorf("nice: %s", r.NiceError)
	}
}
