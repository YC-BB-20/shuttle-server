// Package awake keeps the computer from sleeping while the queue has work
// (DESIGN.md section 7, "Do not let the machine idle-sleep while a job runs";
// built v0.2.111). On macOS it runs the system's own caffeinate -i -s -w PID
// while needed: no idle sleep, no system sleep on mains power, and it ends
// by itself if Shuttle ends. Closing a laptop's lid still sleeps it.
// Elsewhere it does nothing (a dedicated Linux machine is set not to sleep).
package awake

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Keeper starts and stops the hold. Busy is asked every Every.
type Keeper struct {
	Busy  func() bool
	Log   *slog.Logger
	Every time.Duration
	// start runs the hold until ctx ends; replaced in tests.
	start func(ctx context.Context) error

	mu   sync.Mutex
	on   bool
	stop context.CancelFunc
}

// Holding says whether the computer is being kept awake now.
func (k *Keeper) Holding() bool { k.mu.Lock(); defer k.mu.Unlock(); return k.on }

// Run checks Busy until ctx ends, then lets the computer sleep again.
func (k *Keeper) Run(ctx context.Context) {
	if k.start == nil {
		if runtime.GOOS != "darwin" {
			return
		}
		k.start = caffeinate
	}
	every := k.Every
	if every <= 0 {
		every = 10 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	defer k.set(ctx, false)
	for {
		k.set(ctx, k.Busy())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (k *Keeper) set(ctx context.Context, want bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if want == k.on {
		return
	}
	if !want {
		k.stop()
		k.on, k.stop = false, nil
		if k.Log != nil {
			k.Log.Info("the queue is idle: the computer may sleep again")
		}
		return
	}
	hctx, cancel := context.WithCancel(context.Background())
	k.on, k.stop = true, cancel
	if k.Log != nil {
		k.Log.Info("keeping the computer awake while the queue has work")
	}
	go func() {
		err := k.start(hctx)
		if hctx.Err() == nil && err != nil && k.Log != nil {
			k.Log.Warn("could not keep the computer awake", "err", err)
		}
		k.mu.Lock()
		if k.stop != nil && hctx.Err() == nil { // it ended by itself: try again at the next check
			k.stop()
			k.on, k.stop = false, nil
		}
		k.mu.Unlock()
	}()
}

func caffeinate(ctx context.Context) error {
	c := exec.CommandContext(ctx, "/usr/bin/caffeinate", "-i", "-s", "-w", strconv.Itoa(os.Getpid()))
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGTERM) }
	c.WaitDelay = 2 * time.Second
	return c.Run()
}
