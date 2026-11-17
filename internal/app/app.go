// Package app wires the pieces together and runs until asked to stop.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/YC-BB-20/shuttle-server/internal/speedtest"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	shuttle "github.com/YC-BB-20/shuttle-server"
	"github.com/YC-BB-20/shuttle-server/internal/config"
	"github.com/YC-BB-20/shuttle-server/internal/copyjob"
	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/events"
	"github.com/YC-BB-20/shuttle-server/internal/fsx"
	"github.com/YC-BB-20/shuttle-server/internal/launcher"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
	"github.com/YC-BB-20/shuttle-server/internal/logx"
	"github.com/YC-BB-20/shuttle-server/internal/menubar"
	"github.com/YC-BB-20/shuttle-server/internal/safemode"
	"github.com/YC-BB-20/shuttle-server/internal/server"
	"github.com/YC-BB-20/shuttle-server/internal/sysinfo"
	"github.com/YC-BB-20/shuttle-server/internal/update"
	"github.com/YC-BB-20/shuttle-server/internal/version"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitStartup = 1
	ExitPanic   = 2
	ExitMemory  = 3
	ExitRestart = 4 // main starts the program again in place
)

const idleHeadline = "Idle — no copy running"

var (
	errMemory  = errors.New("memory stayed over the ceiling")
	errRestart = errors.New("restart requested")
)

// RestartPath is the program to start again when Run returns ExitRestart.
var RestartPath string

// Options come from the command line.
type Options struct {
	DataDir       string
	ClearSafeMode bool
	Stdout        io.Writer
	StableAfter   time.Duration // 0 = safemode.StableAfter
}

// Status is what the page shows. It never contains a secret.
type Status struct {
	Version    string             `json:"version"`
	Commit     string             `json:"commit"`
	StartedAt  time.Time          `json:"started_at"`
	Now        time.Time          `json:"now"`
	Level      string             `json:"level"`
	Headline   string             `json:"headline"`
	SafeMode   bool               `json:"safe_mode"`
	SafeReason string             `json:"safe_mode_reason,omitempty"`
	Notes      []string           `json:"notes,omitempty"`
	DataDir    string             `json:"data_dir"`
	TempDir    string             `json:"temp_dir"`
	Disk       Disk               `json:"disk"`
	Memory     Memory             `json:"memory"`
	Buffers    limits.BudgetStats `json:"buffers"`
	CPU        limits.CPUResult   `json:"cpu"`
	OpenFiles  OpenFiles          `json:"open_files"`
	Pages      Pages              `json:"pages"`
	Update     update.State       `json:"update"`
	Jobs       []copyjob.View     `json:"jobs"`
}

type Disk struct {
	Free       uint64 `json:"free"`
	Floor      uint64 `json:"floor"`
	BelowFloor bool   `json:"below_floor"`
	Error      string `json:"error,omitempty"`
}

type Memory struct {
	Used    uint64          `json:"used"`
	Ceiling uint64          `json:"ceiling"`
	State   limits.MemState `json:"state"`
}

type OpenFiles struct {
	Limit uint64 `json:"limit"`
	Cap   int    `json:"cap"`
}

type Pages struct {
	Open int `json:"open"`
	Max  int `json:"max"`
}

// Run starts Shuttle Server and returns an exit code.
func Run(opts Options) (code int) {
	started := time.Now()
	if opts.StableAfter == 0 {
		opts.StableAfter = safemode.StableAfter
	}
	data, err := filepath.Abs(opts.DataDir)
	if err != nil {
		fmt.Fprintln(opts.Stdout, "Data folder:", err)
		return ExitStartup
	}
	stateDir := filepath.Join(data, "state")
	for _, d := range []string{data, stateDir, filepath.Join(data, "logs"), filepath.Join(data, "records"), filepath.Join(data, "temp.noindex")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fmt.Fprintln(opts.Stdout, "Could not create the data folder:", err)
			return ExitStartup
		}
	}
	log, closeLog, err := logx.Open(filepath.Join(data, "logs"), opts.Stdout, started)
	if err != nil {
		fmt.Fprintln(opts.Stdout, "Could not open the log:", err)
		return ExitStartup
	}
	defer closeLog()

	trackerPath := filepath.Join(stateDir, "startup.json")
	if opts.ClearSafeMode {
		if err := safemode.Reset(trackerPath); err != nil {
			log.Error("could not clear safe mode", "err", err)
		} else {
			log.Info("safe mode cleared by the owner")
		}
	}
	tr, safe, reason, err := safemode.Open(trackerPath, started)
	if err != nil {
		safe, reason = true, "Could not write to the state folder: "+err.Error()
	}
	var notes []string
	if tr.Note != "" {
		notes = append(notes, tr.Note)
	}
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprint(r)
			tr.RecordError(msg, time.Now())
			log.Error("crashed", "panic", msg)
			code = ExitPanic
		}
	}()

	cfgPath := filepath.Join(data, "config.json")
	cfg, created, err := config.Load(cfgPath)
	if err != nil {
		msg := "The settings file config.json has a problem: " + err.Error()
		tr.RecordError(msg, started)
		safe, reason = true, msg
		cfg = config.Defaults()
	} else if created {
		log.Info("wrote default settings", "file", cfgPath)
	}
	tempDir := cfg.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(data, "temp.noindex")
	}

	// Resource limits (DESIGN.md section 18), applied before anything else runs.
	ram := sysinfo.TotalRAM()
	cpu := limits.ApplyCPU(cfg.CPUShare, cfg.Nice)
	ceiling := cfg.MemoryCeilingBytes(ram)
	limits.ApplyMemoryLimit(ceiling)
	budget := limits.NewBudget(cfg.BufferBudgetBytes(ram))
	fileLimit := sysinfo.OpenFileLimit()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	sigCtx, stopSig := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSig()

	wd := &limits.Watchdog{Ceiling: uint64(ceiling), Interval: 5 * time.Second, Grace: time.Minute, Read: limits.GoMemory,
		OnOver: func(over bool) {
			if over {
				log.Warn("memory over the ceiling: starting nothing new, buffers halved")
				budget.Shrink(budget.Stats().Total / 2)
			} else {
				log.Info("memory back under the ceiling")
				budget.Restore()
			}
		},
		OnCritical: func() {
			log.Error("memory stayed over the ceiling for a minute: stopping cleanly")
			cancel(errMemory)
		}}
	go wd.Run(ctx)

	var jobs *copyjob.Manager
	jobsDone := make(chan struct{})
	disks := diskio.NewRegistry(1, 60*time.Second)
	if !safe {
		jobs = &copyjob.Manager{Dir: filepath.Join(stateDir, "jobs"), Budget: budget, Disks: disks,
			Accepting: wd.AcceptingNewWork, Log: log, Floor: cfg.FloorBytes(), Secrets: filepath.Join(data, "secrets", "links")}
		if v, err := diskio.VolumeOf(data); err == nil {
			jobs.FloorVol = v.ID // the free-space floor is kept on the internal disk
		}
		if err := jobs.Load(); err != nil {
			log.Error("could not read the copy jobs", "err", err)
			jobs = nil
		} else {
			go disks.Run(ctx)
			go func() { jobs.Run(ctx); close(jobsDone) }()
		}
	}

	if jobs == nil {
		close(jobsDone)
	}

	// Restart and updates work in safe mode too: a bad release is undone from there.
	restartCh, quitCh := make(chan struct{}, 1), make(chan struct{}, 1)
	quit := func() {
		select {
		case quitCh <- struct{}{}:
		default:
		}
	}
	restart := func() {
		select {
		case restartCh <- struct{}{}:
		default:
		}
	}
	binPath, _ := os.Executable()
	if p, err := filepath.EvalSymlinks(binPath); err == nil {
		binPath = p
	}
	if renamed := launcher.PrettyName(binPath); renamed != binPath {
		log.Info("named the program file \"Shuttle Server\" (\"shuttle-server\" still starts it)", "path", renamed)
		binPath = renamed
	}
	updater := &update.Updater{Repo: cfg.UpdateRepo, KeyPath: filepath.Join(data, "secrets", "github-update-key"),
		BinPath: binPath, Version: version.Version, Commit: version.Commit, Log: log, Restart: restart,
		CopyBusy: func() bool { return jobs != nil && jobs.Busy() }}
	updater.Init()
	go updater.Watch(ctx, 6*time.Hour)
	if _, port, err := net.SplitHostPort(cfg.Listen); err == nil {
		if done, err := launcher.Refresh(binPath, data, port); done {
			log.Info("updated the Shuttle Server app in Applications")
		} else if err != nil {
			log.Warn("could not update the Shuttle Server app in Applications", "err", err)
		}
	}

	if launcher.LoginSupported() {
		go func() {
			if err := launcher.SetFileIcon(ctx, binPath); err != nil {
				log.Warn("could not give the program its icon in Login Items", "err", err)
			}
		}()
	}
	var login server.Login
	if launcher.LoginSupported() {
		login = loginItem{bin: binPath, data: data}
		if done, err := launcher.RefreshLogin(binPath, data); done {
			log.Info("updated the start-at-login item")
		} else if err != nil {
			log.Warn("could not update the start-at-login item", "err", err)
		}
	}

	hub := events.NewHub(cfg.PageUpdatesPerS, cfg.MaxOpenPages)
	go hub.Run(ctx)

	snapshot := func() Status {
		st := Status{Version: version.Version, Commit: version.Commit, StartedAt: started, Now: time.Now(),
			SafeMode: safe, SafeReason: reason, Notes: notes, DataDir: data, TempDir: tempDir,
			Buffers: budget.Stats(), CPU: cpu, OpenFiles: OpenFiles{fileLimit, cfg.OpenFiles(fileLimit)},
			Pages: Pages{hub.Open(), hub.Max()}}
		st.Memory.Used, st.Memory.State = wd.Status()
		st.Memory.Ceiling = uint64(ceiling)
		st.Disk.Floor = cfg.FloorBytes()
		if free, err := fsx.FreeBytes(tempDir); err != nil {
			st.Disk.Error = err.Error()
		} else {
			st.Disk.Free, st.Disk.BelowFloor = free, free < st.Disk.Floor
		}
		if jobs != nil {
			st.Jobs = jobs.Views()
		}
		st.Update = updater.State()
		var active *copyjob.View
		for i := range st.Jobs {
			if v := &st.Jobs[i]; v.State == "running" || v.State == "waiting" {
				active = v
			}
		}
		ok := false
		switch {
		case safe:
			st.Headline = "Safe mode — nothing runs until the problem is fixed"
		case st.Memory.State != limits.MemOK:
			st.Headline = "Using too much memory — starting nothing new"
		case active != nil && active.State == "waiting":
			st.Headline = active.Reason
		case st.Disk.BelowFloor:
			st.Headline = "The internal disk is below the free-space floor"
		case st.Buffers.Waiting > 0:
			st.Headline = "Waiting for memory buffers"
		case active != nil && active.Phase == "scan":
			st.Headline, ok = fmt.Sprintf("Scanning the source — %d files so far", active.Scanned), true
		case active != nil:
			st.Headline, ok = progressHeadline(active), true
		default:
			st.Headline, ok = idleLine(st.Jobs), true
		}
		st.Level = "warn"
		switch {
		case safe, active != nil && active.Missing != "":
			st.Level = "bad"
		case ok:
			st.Level = "ok"
		}
		return st
	}
	status := func() any { return snapshot() }
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			if b, err := json.Marshal(status()); err == nil {
				hub.Publish(b)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	var actions server.Actions
	if jobs != nil {
		actions = jobs
	}
	var speed *speedtest.Tester
	if jobs != nil {
		speed = &speedtest.Tester{Budget: budget, Disks: disks, Accepting: wd.AcceptingNewWork, Log: log,
			StateFile: filepath.Join(stateDir, "speedtests.json"),
			Busy: func(mount string) string { // a job working on that disk now
				on := func(p string) bool {
					if mount == "/" { // this computer's own disk: anything not on an attached one
						return p != "" && !strings.HasPrefix(p, "/Volumes/") && !strings.HasPrefix(p, "/media/") && !strings.HasPrefix(p, "/mnt/")
					}
					return p == mount || strings.HasPrefix(p, mount+"/")
				}
				for _, v := range jobs.Views() {
					if (v.State == "running" || v.State == "waiting") && (on(v.Source) || on(v.Dest)) {
						return "a job is working on it"
					}
				}
				return ""
			}}
	}
	srv := (&server.Server{Page: shuttle.IndexHTML, Speed: speed, PageDir: cfg.PageDir, Hub: hub, Status: status, Jobs: actions, Listen: cfg.Listen,
		Updates: updater, Restart: restart, Quit: quit, Login: login, LogsDir: filepath.Join(data, "logs"),
		About: func() any {
			return map[string]string{"version": version.Version, "commit": version.Commit, "changelog": shuttle.Changelog}
		}}).HTTP()
	srv.BaseContext = func(net.Listener) context.Context { return ctx } // open pages end when Shuttle stops
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		tr.Forget() // not a crash: most likely already running
		log.Error("could not open the page address — is Shuttle Server already running?", "listen", cfg.Listen, "err", err)
		return ExitStartup
	}
	go srv.Serve(ln)

	_, port, _ := net.SplitHostPort(cfg.Listen)
	pageURL := "http://localhost:" + port
	menuDone := make(chan struct{})
	if cfg.MenuBarIcon && menubar.Supported() {
		go func() {
			defer close(menuDone)
			menubar.Run(ctx, menubar.Options{Dir: filepath.Join(data, "temp.noindex"), URL: pageURL, Log: log,
				Get: func() menubar.Line {
					s := snapshot()
					l := menubar.Line{Level: s.Level, Headline: s.Headline, Version: s.Version, Update: "none", Login: launcher.LoginEnabled()}
					switch u := s.Update; {
					case !u.KeySet:
						l.Update = "nokey"
					case u.Busy != "":
						l.Update = u.Busy // checking, downloading, installing
					case u.Waiting:
						l.Update = "waiting"
					case u.Available != nil:
						l.Update, l.UpdateVersion = "available", u.Available.Version
					case u.Error != "":
						l.Update = "failed"
					}
					return l
				}})
		}()
	} else {
		close(menuDone)
	}
	log.Info("Shuttle Server running", "version", version.String(), "page", pageURL,
		"data", data, "safe_mode", safe, "cores", cpu.Procs, "nice", cpu.Nice,
		"buffer_budget_mb", budget.Stats().Total>>20, "memory_ceiling_mb", ceiling>>20)
	if safe {
		log.Warn("safe mode", "reason", reason, "to_leave", "fix the problem, then start with -clear-safe-mode")
	}
	if cpu.NiceError != "" {
		log.Warn("could not lower priority", "err", cpu.NiceError)
	}

	stable := time.NewTimer(opts.StableAfter)
	defer stable.Stop()
	for running := true; running; {
		select {
		case <-stable.C:
			if !safe {
				tr.Stable()
			}
		case <-sigCtx.Done():
			running = false
		case <-quitCh:
			log.Info("quit from the page")
			running = false
		case <-restartCh:
			log.Info("restarting")
			cancel(errRestart)
			running = false
		}
	}

	// Stop in order: copies save their place, then the page, then the icon.
	cancel(context.Cause(ctx))
	select {
	case <-jobsDone:
	case <-time.After(20 * time.Second):
		log.Warn("a copy did not stop within 20 seconds; it will be re-checked on the next start")
	}
	shut, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	srv.Shutdown(shut)
	<-menuDone
	if errors.Is(context.Cause(ctx), errRestart) {
		if !safe {
			tr.Stable()
		}
		log.Info("stopped for restart", "after", time.Since(started).Round(time.Second).String())
		RestartPath = binPath
		return ExitRestart
	}
	if errors.Is(context.Cause(ctx), errMemory) {
		return ExitMemory
	}
	if !safe {
		tr.Stable() // a clean quit is not a crash
	}
	log.Info("stopped", "after", time.Since(started).Round(time.Second).String())
	return ExitOK
}

// progressHeadline is the status line for a running job, as in the menu bar:
// for a copy, how many files are copied and how many are verified. It says
// "Verifying" while the file being worked on is read back or compared, and
// files already at the destination count as verified, never as copied
// (v0.2.44).
// idleLine is the headline with nothing running: after a job has finished
// it says how long the last one took, in m:ss (v0.2.56).
func idleLine(jobs []copyjob.View) string {
	var last *copyjob.View
	for i := range jobs {
		if v := &jobs[i]; v.State == "finished" && v.WorkMs > 0 && (last == nil || v.Finished.After(last.Finished)) {
			last = v
		}
	}
	if last == nil {
		return idleHeadline
	}
	what := "copy"
	if last.Kind == "verify" {
		what = "verify"
	}
	return idleHeadline + " · last " + what + " took " + Clock(time.Duration(last.WorkMs)*time.Millisecond)
}

// Clock writes a duration as m:ss, or h:mm:ss from an hour.
func Clock(d time.Duration) string {
	t := int64(d.Round(time.Second) / time.Second)
	if t < 0 {
		t = 0
	}
	if h := t / 3600; h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

func progressHeadline(v *copyjob.View) string {
	if v.Kind == "verify" {
		return fmt.Sprintf("Verifying — %d of %d files checked", v.Verified+v.Failed+v.Skipped, v.Files)
	}
	verb := "Copying"
	if copyjob.IsCheckStage(v.Stage) {
		verb = "Verifying"
	}
	toCopy := max(0, v.Files-v.Identical)
	copied := min(toCopy, v.Verified+v.Failed+v.Copied+v.Hashed)
	s := fmt.Sprintf("%s — %d of %d files copied", verb, copied, toCopy)
	if v.Check == "none" {
		if copied > 0 {
			s += " (not read back)"
		}
	} else {
		s += fmt.Sprintf(", %d verified", v.Verified)
	}
	if v.Identical > 0 {
		s += fmt.Sprintf(" · %d already there, verified", v.Identical)
	}
	return s
}
