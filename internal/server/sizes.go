package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
)

// Folder sizes for the file browser (v0.2.101). Working out a folder's size
// means listing everything inside it, so it is bounded: one at a time (a new
// request cancels the one before), a cap on entries and on time, never
// crossing onto another disk, never following links. A size cut short says
// so ("at least"). "Auto" sizes only where it costs little: not on a network
// share or the cloud, not on a spinning disk, and not while a job runs on
// that disk. "Calculate sizes" asks for them anyway.

type folderSize struct {
	Bytes   int64 `json:"bytes"`
	Files   int64 `json:"files"`
	Partial bool  `json:"partial,omitempty"`
}

type sizeLimits struct {
	entries int
	time    time.Duration
}

var (
	autoSizeLimits = sizeLimits{100_000, 4 * time.Second}
	askSizeLimits  = sizeLimits{2_000_000, 12 * time.Second} // inside the 15 s request limit
	sizeMu         sync.Mutex                                // one sizing at a time
	sizeCancel     struct {
		sync.Mutex
		f context.CancelFunc
	}
	// sizeSpinning (tests) says whether the disk at mount spins; nil when unknown.
	sizeSpinning = func(ctx context.Context, mount string) *bool {
		in := cachedDetails(ctx, mount)
		if in.Solid == nil {
			return nil
		}
		spin := !*in.Solid
		return &spin
	}
)

func (s *Server) folderSizes(w http.ResponseWriter, r *http.Request) {
	p := filepath.Clean(r.URL.Query().Get("path"))
	auto := r.URL.Query().Get("auto") == "1"
	if !filepath.IsAbs(p) || !browsable(p) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this folder cannot be measured"})
		return
	}
	if auto {
		if why := s.sizeTaxing(r.Context(), p); why != "" {
			writeJSON(w, http.StatusOK, map[string]any{"skipped": why})
			return
		}
	}
	// A new request replaces the one still running.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sizeCancel.Lock()
	if sizeCancel.f != nil {
		sizeCancel.f()
	}
	sizeCancel.f = cancel
	sizeCancel.Unlock()
	sizeMu.Lock()
	defer sizeMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	lim := askSizeLimits
	if auto {
		lim = autoSizeLimits
	}
	ctx, stop := context.WithTimeout(ctx, lim.time)
	defer stop()
	out, err := measureChildren(ctx, p, lim.entries)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "could not read this folder"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sizes": out})
}

// sizeTaxing says why automatic sizing is skipped here, or "".
func (s *Server) sizeTaxing(ctx context.Context, p string) string {
	vol, err := diskio.VolumeOf(p)
	if err != nil {
		return "not found"
	}
	if vol.Network {
		return "network"
	}
	if v, ok := s.Jobs.(viewer); ok && vol.Mount != "" {
		for _, j := range v.Views() {
			if j.State == "running" && (under(j.Source, vol.Mount) || under(j.Dest, vol.Mount)) {
				return "busy"
			}
		}
	}
	if spin := sizeSpinning(ctx, vol.Mount); spin != nil && *spin {
		return "spinning"
	}
	return ""
}

// measureChildren adds up every folder directly inside dir, sharing one
// entry budget; what is left over when it runs out is marked partial.
func measureChildren(ctx context.Context, dir string, budget int) (map[string]folderSize, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	ents, err := f.ReadDir(10_000)
	f.Close()
	if err != nil && len(ents) == 0 {
		return map[string]folderSize{}, nil
	}
	dev := devOf(dir)
	out := map[string]folderSize{}
	for _, e := range ents {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		fs := measure(ctx, filepath.Join(dir, e.Name()), dev, &budget)
		out[e.Name()] = fs
	}
	return out, nil
}

// measure walks one folder without recursion, reading entries in batches.
func measure(ctx context.Context, root string, dev uint64, budget *int) folderSize {
	var fs folderSize
	stack := []string{root}
	for len(stack) > 0 {
		if ctx.Err() != nil || *budget <= 0 {
			fs.Partial = true
			return fs
		}
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		f, err := os.Open(d)
		if err != nil {
			continue
		}
		for {
			ents, err := f.ReadDir(256)
			for _, e := range ents {
				*budget--
				if e.Type()&os.ModeSymlink != 0 {
					continue
				}
				if e.IsDir() {
					p := filepath.Join(d, e.Name())
					if dev != 0 && devOf(p) != dev {
						continue // another disk mounted inside
					}
					stack = append(stack, p)
					continue
				}
				if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
					fs.Bytes += info.Size()
					fs.Files++
				}
			}
			if err != nil || len(ents) == 0 || ctx.Err() != nil || *budget <= 0 {
				break
			}
		}
		f.Close()
	}
	if ctx.Err() != nil || *budget <= 0 {
		fs.Partial = fs.Partial || len(stack) > 0 || *budget <= 0
	}
	return fs
}

func devOf(p string) uint64 {
	fi, err := os.Lstat(p)
	if err != nil {
		return 0
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}

// browsable: the same places the file browser shows (home, connected
// disks and shares), never the system's own folders.
func browsable(p string) bool {
	home, _ := os.UserHomeDir()
	if p == home || strings.HasPrefix(p, home+"/") {
		return true
	}
	for _, x := range places() {
		if (x.Kind == "disk" || x.Kind == "server" || x.Kind == "share") && (p == x.Path || strings.HasPrefix(p, x.Path+"/")) {
			return true
		}
	}
	return false
}
