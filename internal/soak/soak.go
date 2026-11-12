// Package soak runs copy jobs for a long time over many small files and some
// large ones, recording memory, goroutines, threads and open files every
// interval. All four must stay flat (DESIGN.md section 18).
package soak

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
)

// Options for a soak run.
type Options struct {
	Dir      string // an empty test folder; everything is written inside it
	Files    int
	BigFiles int
	BigSize  int64
	Duration time.Duration
	Every    time.Duration
	Out      io.Writer
}

// Sample is one reading.
type Sample struct {
	At         time.Duration
	Memory     uint64
	Goroutines int
	Threads    int
	OpenFiles  int
	Proven     int
}

func openFiles() int {
	for _, d := range []string{"/proc/self/fd", "/dev/fd"} {
		if e, err := os.ReadDir(d); err == nil {
			return len(e)
		}
	}
	return -1
}

// Run makes the test data, copies it again and again until the time is up,
// and returns the samples.
func Run(o Options) ([]Sample, error) {
	src := filepath.Join(o.Dir, "src")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		fmt.Fprintf(o.Out, "Making %d small files and %d large ones in %s …\n", o.Files, o.BigFiles, src)
		r := rand.New(rand.NewSource(1))
		buf := make([]byte, 1<<20)
		for i := 0; i < o.Files; i++ {
			p := filepath.Join(src, fmt.Sprintf("d%03d", i/1000), fmt.Sprintf("f%06d.bin", i))
			os.MkdirAll(filepath.Dir(p), 0o755)
			r.Read(buf[:1+r.Intn(64<<10)])
			if err := os.WriteFile(p, buf[:1+r.Intn(64<<10)], 0o644); err != nil {
				return nil, err
			}
		}
		for i := 0; i < o.BigFiles; i++ {
			f, err := os.Create(filepath.Join(src, fmt.Sprintf("big%02d.mov", i)))
			if err != nil {
				return nil, err
			}
			for n := int64(0); n < o.BigSize; n += int64(len(buf)) {
				r.Read(buf)
				f.Write(buf)
			}
			f.Close()
		}
	}
	m := &copyjob.Manager{Dir: filepath.Join(o.Dir, "state"), Budget: limits.NewBudget(64 << 20),
		Disks: diskio.NewRegistry(1, time.Minute), Accepting: func() bool { return true },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := m.Load(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.Duration)
	defer cancel()
	go m.Run(ctx)

	csvf, err := os.Create(filepath.Join(o.Dir, "soak.csv"))
	if err != nil {
		return nil, err
	}
	defer csvf.Close()
	w := csv.NewWriter(csvf)
	w.Write([]string{"seconds", "memory_mb", "goroutines", "threads", "open_files", "files_proven"})
	start := time.Now()
	round := 0
	var samples []Sample
	tick := time.NewTicker(o.Every)
	defer tick.Stop()
	for {
		busy := false
		proven := 0
		for _, v := range m.Views() {
			proven += v.Verified + v.Identical
			if v.State != "finished" && v.State != "stopped" {
				busy = true
			}
		}
		if !busy {
			round++
			if _, err := m.Add(copyjob.Spec{Source: src, Dest: filepath.Join(o.Dir, fmt.Sprintf("dst-%03d", round)), S2: "now"}); err != nil {
				return samples, err
			}
		}
		select {
		case <-ctx.Done():
			return samples, nil
		case <-tick.C:
		}
		s := Sample{At: time.Since(start), Memory: limits.GoMemory(), Goroutines: runtime.NumGoroutine(),
			Threads: pprof.Lookup("threadcreate").Count(), OpenFiles: openFiles(), Proven: proven}
		samples = append(samples, s)
		w.Write([]string{strconv.Itoa(int(s.At.Seconds())), strconv.FormatFloat(float64(s.Memory)/(1<<20), 'f', 1, 64),
			strconv.Itoa(s.Goroutines), strconv.Itoa(s.Threads), strconv.Itoa(s.OpenFiles), strconv.Itoa(proven)})
		w.Flush()
		fmt.Fprintf(o.Out, "%6.0fs  memory %6.1f MB  goroutines %3d  threads %3d  open files %3d  proven %d\n",
			s.At.Seconds(), float64(s.Memory)/(1<<20), s.Goroutines, s.Threads, s.OpenFiles, proven)
	}
}

// Flat reports whether the second half of a run stayed within the first
// half's peaks plus a margin.
func Flat(s []Sample) (bool, string) {
	if len(s) < 4 {
		return false, "too few samples"
	}
	half := len(s) / 2
	peak := func(xs []Sample, f func(Sample) float64) float64 {
		m := 0.0
		for _, x := range xs {
			if v := f(x); v > m {
				m = v
			}
		}
		return m
	}
	checks := []struct {
		name   string
		f      func(Sample) float64
		margin float64
	}{
		{"memory", func(x Sample) float64 { return float64(x.Memory) }, 1.5},
		{"goroutines", func(x Sample) float64 { return float64(x.Goroutines) }, 1.2},
		{"threads", func(x Sample) float64 { return float64(x.Threads) }, 1.2},
		{"open files", func(x Sample) float64 { return float64(x.OpenFiles) }, 1.2},
	}
	for _, c := range checks {
		a, b := peak(s[:half], c.f), peak(s[half:], c.f)
		if b > a*c.margin+4 {
			return false, fmt.Sprintf("%s grew from %.0f to %.0f", c.name, a, b)
		}
	}
	return true, "memory, goroutines, threads and open files stayed flat"
}
