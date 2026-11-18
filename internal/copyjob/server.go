package copyjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/fsx"
	"github.com/YC-BB-20/shuttle-server/internal/location"
	"github.com/YC-BB-20/shuttle-server/internal/remote"
)

// KindServer (v0.2.122): a copy whose source, destination or both are on a
// server added in Settings (S3 or WebDAV). Every byte passes through this
// computer; nothing is stored here on the way between two servers.
//
// Proof, per file:
//   - To a server: the source is read and hashed on its own first (S1),
//     sent under a temporary name, then downloaded from the server and
//     hashed (D1). Only when S1 equals D1 does the file get its real name.
//     Two separate reads of the source (S1 and the one sent) and one of the
//     server's copy.
//   - From a server: the download is hashed as it is written (C), flushed,
//     read back from the disk (D1) while the server is read a second time
//     (S2, unless skipped). The file gets its real name only when they agree.
const KindServer = "server"

func (s Spec) isServer() bool { return s.Kind == KindServer }

// Servers opens a server the owner added in Settings.
type Servers interface {
	Open(id string) (remote.Remote, remote.Public, error)
}

// srvEnd is one end of a server copy: a folder on a server, or on this
// computer.
type srvEnd struct {
	r    remote.Remote // nil: a folder on this computer
	name string        // the server's name, for messages
	dir  string        // server: the folder inside it; this computer: an absolute folder
	disk *diskio.Disk  // this computer only
}

func (e srvEnd) onServer() bool { return e.r != nil }

func (e srvEnd) path(rel string) string {
	if e.onServer() {
		return path.Join(e.dir, rel)
	}
	return filepath.Join(e.dir, filepath.FromSlash(rel))
}

// serverWalker lists a folder on a server for the manifest: folders one at
// a time, only a stack of folder paths held, never every file.
type serverWalker struct {
	r                   remote.Remote
	dir                 string
	only                []string
	system, appleDouble bool
}

func (w serverWalker) Walk(ctx context.Context, file func(location.Entry) error, skip func(location.Skipped)) error {
	only := map[string]bool{}
	for _, n := range w.only {
		only[n] = true
	}
	stack := []string{""}
	for len(stack) > 0 {
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		err := w.r.List(ctx, path.Join(w.dir, rel), func(e remote.Entry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := path.Base(e.Path)
			if len(only) > 0 && rel == "" && !only[name] {
				return nil
			}
			r := path.Join(rel, name)
			switch {
			case w.system && location.IsSystemName(name, w.appleDouble):
				skip(location.Skipped{Rel: r, Why: location.WhySystem})
			case e.Dir:
				stack = append(stack, r)
			case remote.IsTemp(name) || location.IsShuttleTemp(name):
			default:
				return file(location.Entry{Rel: r, Size: e.Size, Mtime: e.Mod.UnixNano(), Mode: 0o644})
			}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if rel == "" || remote.Retryable(err) {
				return err // the server, not one folder: the job waits and lists again
			}
			skip(location.Skipped{Rel: rel, Why: "could not list this folder on the server: " + err.Error()})
		}
	}
	return nil
}

// addServer adds a copy to, from or between servers.
func (m *Manager) addServer(s Spec) (*Job, error) {
	if m.Servers == nil {
		return nil, errors.New("servers are not available")
	}
	end := func(p, which string) (place, vol, name string, err error) {
		p = strings.TrimSpace(p)
		if id, rel, ok := remote.SplitPlace(p); ok {
			_, pub, err := m.Servers.Open(id)
			if err != nil {
				return "", "", "", fmt.Errorf("the %s's server is no longer in Settings", which)
			}
			return remote.Place(id, rel), remote.PlacePrefix + id, pub.Name, nil
		}
		if remote.IsPlace(p) {
			return "", "", "", fmt.Errorf("the %s is not a place on a server", which)
		}
		a, err := filepath.Abs(p)
		if err != nil {
			return "", "", "", err
		}
		if which == "source" {
			if fi, err := os.Stat(a); err != nil || !fi.IsDir() {
				return "", "", "", fmt.Errorf("the source folder %q was not found", a)
			}
		}
		if !diskio.VolumeMounted(existingDir(filepath.Join(a, "x"))) {
			return "", "", "", fmt.Errorf("the %s's disk is not connected", which)
		}
		v, err := diskio.VolumeOf(existingDir(filepath.Join(a, "x")))
		if err != nil {
			return "", "", "", err
		}
		return a, v.ID, "", nil
	}
	src, sv, sn, err := end(s.Source, "source")
	if err != nil {
		return nil, err
	}
	dst, dv, dn, err := end(s.Dest, "destination")
	if err != nil {
		return nil, err
	}
	if src == dst || strings.HasPrefix(dst, strings.TrimSuffix(src, "/")+"/") {
		return nil, errors.New("the destination cannot be the source folder or inside it")
	}
	if len(s.Only) > MaxOnly {
		return nil, fmt.Errorf("pick at most %d files and folders one by one, or choose their folder", MaxOnly)
	}
	seen := map[string]bool{}
	var only []string
	for _, n := range s.Only {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\") {
			return nil, fmt.Errorf("%q is not a name inside the source folder", n)
		}
		if !remote.IsPlace(src) {
			if _, err := os.Lstat(filepath.Join(src, n)); err != nil {
				return nil, fmt.Errorf("%q was not found in the source folder", n)
			}
		}
		if !seen[n] {
			seen[n] = true
			only = append(only, n)
		}
	}
	cs := m.Settings()
	t := Spec{Kind: KindServer, Source: src, Dest: dst, Only: only, OnlyDirs: s.OnlyDirs, S2: "now",
		EstBytes: max(0, s.EstBytes), EstFiles: max(0, s.EstFiles), SourceServer: sn, DestServer: dn,
		SkipSystem: cs.SkipSystem, SkipAppleDouble: cs.SkipSystem && cs.SkipAppleDouble, Algo: normAlgo(cs.Hash)}
	if s.S2 == "skip" {
		t.S2 = "skip"
	}
	now := time.Now()
	id, err := m.newJobDir(now.Format("20060102-150405.000"))
	if err != nil {
		return nil, err
	}
	j := &Job{dir: filepath.Join(m.Dir, id), st: state{FormatVersion: FormatVersion, ID: id, Spec: t, Created: now,
		Choice: "run", Phase: "scan", SourceVol: sv, DestVol: dv, SameVolume: sv == dv}}
	if err := j.save(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.jobs = append(m.jobs, j)
	m.mu.Unlock()
	m.Log.Info("server copy added", "job", id, "source", src, "destination", dst, "s2", t.S2, "algo", t.Algo)
	m.poke()
	return j, nil
}

// srvEndOf opens one end of a running server copy. which is "source" or
// "destination", for the missing-disk wait.
func (m *Manager) srvEndOf(p, vol string) (srvEnd, string, error) {
	if id, rel, ok := remote.SplitPlace(p); ok {
		if m.Servers == nil {
			return srvEnd{}, "", errors.New("servers are not available")
		}
		r, pub, err := m.Servers.Open(id)
		if err != nil {
			return srvEnd{}, "", errors.New("the server is no longer in Settings; add it again with the same name to continue")
		}
		return srvEnd{r: r, name: pub.Name, dir: rel}, "", nil
	}
	at := existingDir(filepath.Join(p, "x"))
	if !diskio.VolumeMounted(at) {
		return srvEnd{}, "gone", nil
	}
	d, err := m.Disks.For(at)
	if err != nil || d.ID != vol || !diskio.Present(d) {
		return srvEnd{}, "gone", nil
	}
	return srvEnd{dir: p, disk: d}, "", nil
}

// runServer runs one server copy, like runOne does a disk copy.
func (m *Manager) runServer(ctx context.Context, j *Job) {
	src, sgone, err := m.srvEndOf(j.st.Spec.Source, j.st.SourceVol)
	if err == nil && sgone == "" {
		if _, err := os.Stat(j.st.Spec.Source); !src.onServer() && err != nil {
			sgone = "gone"
		}
	}
	dst, dgone, err2 := m.srvEndOf(j.st.Spec.Dest, j.st.DestVol)
	if err != nil || err2 != nil {
		m.wait(ctx, j, errors.Join(err, err2).Error())
		return
	}
	if which := missingWord(sgone != "", dgone != ""); which != "" {
		m.waitMissing(ctx, j, which)
		return
	}
	jctx, cancel := context.WithCancelCause(ctx)
	m.mu.Lock()
	m.running, m.cancel = j, cancel
	m.mu.Unlock()
	j.mu.Lock()
	switch j.st.Choice {
	case "stopped":
		cancel(errOwnerStop)
	case "paused":
		cancel(errOwnerPause)
	}
	j.mu.Unlock()
	j.setLive(func(l *live) { l.reason, l.missing = "", "" })
	go func() { // notice an unplugged disk within seconds
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-jctx.Done():
				return
			case <-t.C:
				if (src.disk != nil && !diskio.Present(src.disk)) || (dst.disk != nil && !diskio.Present(dst.disk)) {
					cancel(diskio.ErrGone)
					return
				}
			}
		}
	}()
	j.startWork(time.Now())
	e := env{budget: m.Budget, src: src.disk, dst: dst.disk, log: m.Log}
	if e.src == nil {
		e.src = noDisk
	}
	if e.dst == nil {
		e.dst = noDisk
	}
	err = j.runServerJob(jctx, e, src, dst, m.Accepting)
	cancel(nil)
	m.mu.Lock()
	m.running = nil
	m.mu.Unlock()
	j.mu.Lock()
	worked := j.endWorkLocked(time.Now())
	stopped := j.st.Choice == "stopped"
	j.mu.Unlock()
	if worked {
		j.save()
	}
	cause := context.Cause(jctx)
	var w waitErr
	switch {
	case stopped:
		m.finishServerStopped(j, dst)
	case err == nil:
		j.mu.Lock()
		c := j.c
		j.mu.Unlock()
		m.Log.Info("server copy finished", "job", j.st.ID, "verified", c.Verified, "already_there", c.Identical, "failed", c.Failed)
		m.askAboutErrors(j)
	case errors.Is(cause, errOwnerPause), ctx.Err() != nil:
		j.setLive(func(l *live) { l.reason, l.missing = "", "" })
	case errors.Is(err, diskio.ErrGone), errors.Is(cause, diskio.ErrGone):
		_, sg, _ := m.srvEndOf(j.st.Spec.Source, j.st.SourceVol)
		_, dg, _ := m.srvEndOf(j.st.Spec.Dest, j.st.DestVol)
		which := missingWord(sg != "", dg != "")
		if which == "" {
			which = "both"
		}
		m.waitMissing(ctx, j, which)
	case errors.Is(err, diskio.ErrStalled):
		m.wait(ctx, j, "A disk is not responding; waiting for it")
	case errors.As(err, &w):
		m.wait(ctx, j, w.reason)
	default:
		m.Log.Error("server copy hit a problem; it will try again", "job", j.st.ID, "err", err)
		m.wait(ctx, j, "Problem: "+err.Error()+" — trying again shortly")
	}
}

func missingWord(s, d bool) string {
	switch {
	case s && d:
		return "both"
	case s:
		return "source"
	case d:
		return "destination"
	}
	return ""
}

// noDisk stands in for the disk of an end that is on a server.
var noDisk = diskio.NewDisk("", "", "", 1, time.Minute)

// finishServerStopped ends a stopped server copy. Proven files stay; the
// unfinished upload or download of the file in progress (our own temporary
// file) is removed.
func (m *Manager) finishServerStopped(j *Job, dst srvEnd) {
	j.mu.Lock()
	if j.st.Phase == "done" {
		j.mu.Unlock()
		return
	}
	temp := j.live.temp
	if u, ok := j.loadUpload(); ok && temp == "" {
		temp = u.State.Path // after a restart the upload in progress is known from its record
	}
	j.st.Phase, j.st.Finished, j.st.Checkpoints = "done", time.Now(), nil
	j.live = live{}
	j.mu.Unlock()
	if temp != "" {
		if dst.onServer() && remote.IsTemp(temp) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			if st, ok := j.loadUpload(); ok {
				dst.r.Abort(ctx, st.State)
			}
			dst.r.RemoveTemp(ctx, temp)
			cancel()
		} else if !dst.onServer() && strings.HasSuffix(temp, ".shuttle-part") {
			os.Remove(temp)
		}
	}
	os.Remove(j.path("upload.json"))
	j.save()
}

// runServerJob lists the source, then copies and proves each file.
func (j *Job) runServerJob(ctx context.Context, e env, src, dst srvEnd, accepting func() bool) error {
	if j.st.Phase == "scan" {
		var w walker = serverWalker{r: src.r, dir: src.dir, only: j.st.Spec.Only, system: j.st.Spec.SkipSystem, appleDouble: j.st.Spec.SkipAppleDouble}
		if !src.onServer() {
			w = j.source()
		}
		j.setLive(func(l *live) { l.stage = "Listing the source"; l.scanned, l.scannedBytes = 0, 0 })
		l, err := listManifest(ctx, w, j.path("manifest.jsonl"), j.path("skipped.jsonl"), func(n int, total int64) {
			j.setLive(func(l *live) { l.scanned, l.scannedBytes = n, total })
		})
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			if src.disk != nil && !diskio.Present(src.disk) {
				return diskio.ErrGone
			}
			return serverWait(err, src.name)
		}
		j.mu.Lock()
		j.st.Files, j.st.Bytes, j.st.Skipped, j.st.SystemSkipped, j.st.Phase = l.n, l.total, l.skipped, l.system, "copy"
		j.live.scanned, j.live.scannedBytes = l.n, l.total
		j.mu.Unlock()
		if err := j.save(); err != nil {
			return err
		}
	}
	j.mu.Lock()
	j.status = make([]byte, j.st.Files)
	j.already = nil
	j.c = counts{}
	j.mu.Unlock()
	jr, _, err := OpenJournal(j.path("journal.log"), func(r Rec) { j.mu.Lock(); j.apply(r); j.mu.Unlock() })
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	defer jr.Close()
	if j.st.Phase == "copy" {
		it, err := openManifest(j.path("manifest.jsonl"))
		if err != nil {
			return err
		}
		defer it.Close()
		for it.Next() {
			j.mu.Lock()
			pending := it.E.I < len(j.status) && j.status[it.E.I] == stPending
			j.mu.Unlock()
			if !pending {
				continue
			}
			if !accepting() {
				return waitErr{"Waiting for memory to free up"}
			}
			en := it.E
			j.setLive(func(l *live) { l.file, l.done, l.size = en.P, 0, en.S })
			began := time.Now()
			var r Rec
			if dst.onServer() {
				r, err = j.toServer(ctx, e, src, dst, &en)
			} else {
				r, err = j.fromServer(ctx, e, src, dst, &en)
			}
			if err != nil {
				if ctx.Err() != nil { // paused, stopped or a disk gone: never this file's fault
					return context.Cause(ctx)
				}
				if !isFileErr(err) {
					return err
				}
				r = Rec{I: en.I, St: "failed", N: en.S, P: en.P, Err: err.Error()}
			}
			r.T, r.Ms = time.Now().Unix(), time.Since(began).Milliseconds()
			jr.Append(r)
			j.mu.Lock()
			j.apply(r)
			j.mu.Unlock()
			j.dropCheckpoints(en.I)
			j.setLive(func(l *live) { l.temp = "" })
		}
		jr.Flush()
	}
	j.mu.Lock()
	j.st.Phase, j.st.Finished, j.st.Checkpoints = "done", time.Now(), nil
	j.st.CopyRate = copyAverage(j.copyClock(j.st.Finished))
	j.endWorkLocked(j.st.Finished)
	j.live = live{}
	j.mu.Unlock()
	return j.save()
}

// serverWait turns a server error that is not about one file into a wait:
// the line or the server is down, the login was refused, the server is full.
// Anything else is a problem with this file.
func serverWait(err error, name string) error {
	if name == "" {
		name = "the server"
	}
	var re *remote.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, diskio.ErrGone), errors.Is(err, diskio.ErrStalled):
		return err
	case remote.Retryable(err):
		return waitErr{"Waiting for " + name + ": " + err.Error()}
	case errors.As(err, &re) && (re.Code == 401 || re.Code == 407 || re.Code == 507):
		return waitErr{"Waiting: " + name + " says " + err.Error()}
	case isFileErr(err):
		return err
	}
	return perFile("%s", err.Error())
}

// sourceHash reads a source file in full and hashes it (S1). It returns the
// file's size, which may differ from the list's when the file changed.
func (j *Job) sourceHash(ctx context.Context, e env, src srvEnd, en *entry, stage string) (sum, int64, int64, error) {
	p := src.path(en.P)
	if src.onServer() {
		st, err := src.r.Stat(ctx, p)
		if errors.Is(err, remote.ErrNotFound) {
			return sum{}, 0, 0, perFile("no longer on the server")
		}
		if err != nil {
			return sum{}, 0, 0, serverWait(err, src.name)
		}
		h, err := j.hashRemote(ctx, e, src.r, src.name, p, st.Size, st.Mod.UnixNano(), en.I, stage, true, true)
		return h, st.Size, st.Mod.UnixNano(), err
	}
	h, err := j.hashFile(ctx, e, src.disk, p, en.I, stage, true)
	if err != nil {
		return sum{}, 0, 0, err
	}
	size, mtime, err := statSM(p)
	if err != nil {
		return sum{}, 0, 0, perFile("could not read: %v", err)
	}
	return h, size, mtime, nil
}

// toServer copies one file to a server and proves it (S1, send, D1).
func (j *Job) toServer(ctx context.Context, e env, src, dst srvEnd, en *entry) (Rec, error) {
	final := dst.path(en.P)
	tmp := final + remote.TempSuffix
	if have, err := dst.r.Stat(ctx, final); err == nil {
		// Already there: compare, never replace.
		j.setLive(func(l *live) { l.stage = "Already on the server: reading both in full to compare" })
		s1, size, _, err := j.sourceHash(ctx, e, src, en, "s1")
		if err != nil {
			return Rec{}, err
		}
		if have.Size == size {
			d1, err := j.hashRemote(ctx, e, dst.r, dst.name, final, size, have.Mod.UnixNano(), en.I, "d1", true, false)
			if err != nil {
				return Rec{}, err
			}
			if d1 == s1 {
				return Rec{I: en.I, St: "identical", N: size, S1: s1.String(), D1: d1.String()}, nil
			}
		}
		return Rec{}, perFile("a different file with this name is already on the server; it was left as it is")
	} else if !errors.Is(err, remote.ErrNotFound) {
		return Rec{}, serverWait(err, dst.name)
	}
	j.setLive(func(l *live) { l.stage = "Reading the source before sending it" })
	s1, size, mtime, err := j.sourceHash(ctx, e, src, en, "s1")
	if err != nil {
		return Rec{}, err
	}
	chg := size != en.S || mtime != en.M
	if err := dst.r.Mkdir(ctx, path.Dir(final)); err != nil {
		return Rec{}, serverWait(err, dst.name)
	}
	var d1 sum
	for try := 1; ; try++ {
		if err := j.send(ctx, e, src, dst, en, tmp, size, mtime); err != nil {
			return Rec{}, err
		}
		j.setLive(func(l *live) { l.stage = "Reading back from " + dst.name })
		d1, err = j.hashRemote(ctx, e, dst.r, dst.name, tmp, size, 0, en.I, "d1", true, false)
		if err != nil {
			return Rec{}, err
		}
		if d1 == s1 {
			break
		}
		// Read the server's copy once more before believing a difference.
		j.dropCheckpoint(en.I, "d1")
		j.setLive(func(l *live) { l.stage = "Reading back from " + dst.name + " again" })
		again, err := j.hashRemote(ctx, e, dst.r, dst.name, tmp, size, 0, en.I, "d1", true, false)
		if err != nil {
			return Rec{}, err
		}
		if again == s1 {
			d1 = again
			break
		}
		if try == 2 {
			return Rec{}, perFile("the copy on the server read back differently from the source twice; it was left there as %s", path.Base(tmp))
		}
		// Send it again once: our own unproven upload is replaced.
		j.dropCheckpoints(en.I)
		if err := dst.r.RemoveTemp(ctx, tmp); err != nil {
			return Rec{}, serverWait(err, dst.name)
		}
	}
	// The source must still be what S1 read.
	if !src.onServer() {
		if s, m, err := statSM(src.path(en.P)); err != nil || s != size || m != mtime {
			return Rec{}, perFile("the source changed while it was being sent; the copy on the server was left as %s", path.Base(tmp))
		}
	}
	j.setLive(func(l *live) { l.stage = "Giving the copy its name on " + dst.name })
	if err := dst.r.Rename(ctx, tmp, final); err != nil {
		if errors.Is(err, remote.ErrExists) {
			return Rec{}, perFile("a file with this name appeared on the server during the copy; the copy was left as %s", path.Base(tmp))
		}
		return Rec{}, serverWait(err, dst.name)
	}
	r := Rec{I: en.I, St: "verified", N: size, S1: s1.String(), D1: d1.String(), A: 1}
	if chg {
		r.Chg, r.Err = true, "the source differed from the list when its turn came; it was copied as found"
	}
	return r, nil
}

// uploadRec is the unfinished upload of one file, kept so a cut-off upload
// continues where the server says it stopped (S3) after a pause or restart.
type uploadRec struct {
	I     int                `json:"i"`
	Size  int64              `json:"size"`
	Mtime int64              `json:"mtime"`
	State remote.UploadState `json:"state"`
}

func (j *Job) loadUpload() (uploadRec, bool) {
	b, err := os.ReadFile(j.path("upload.json"))
	if err != nil {
		return uploadRec{}, false
	}
	var u uploadRec
	return u, json.Unmarshal(b, &u) == nil
}

// send uploads one source file to tmp on the destination server.
func (j *Job) send(ctx context.Context, e env, src, dst srvEnd, en *entry, tmp string, size, mtime int64) error {
	var st remote.UploadState
	if u, ok := j.loadUpload(); ok && u.I == en.I && u.Size == size && u.Mtime == mtime && u.State.Path == tmp && dst.r.Resumable() {
		st = u.State
	} else {
		os.Remove(j.path("upload.json"))
		if _, err := dst.r.Stat(ctx, tmp); err == nil {
			// Our own unfinished upload from an earlier try: sent again from the start.
			if err := dst.r.RemoveTemp(ctx, tmp); err != nil {
				return serverWait(err, dst.name)
			}
		}
	}
	save := func(s remote.UploadState) error {
		b, _ := json.Marshal(uploadRec{I: en.I, Size: size, Mtime: mtime, State: s})
		return fsx.WriteFileAtomic(j.path("upload.json"), b, 0o600)
	}
	var ra io.ReaderAt
	if src.onServer() {
		rr := &remoteReaderAt{ctx: ctx, r: src.r, p: src.path(en.P)}
		defer rr.Close()
		ra = rr
	} else {
		f, err := diskio.OpenRead(src.path(en.P))
		if err != nil {
			if !diskio.Present(src.disk) {
				return diskio.ErrGone
			}
			return perFile("could not open: %v", err)
		}
		defer f.Close()
		ra = diskReaderAt{ctx: ctx, d: src.disk, f: f}
	}
	start := time.Now()
	j.copySince.Store(start.UnixNano())
	defer func() { j.copySince.Store(0); j.copyNs.Add(int64(time.Since(start))) }()
	var last int64
	var mu sync.Mutex
	done := func(n int64) {
		mu.Lock()
		if n > last {
			j.copyBytes.Add(n - last)
			j.srcBytes.Add(n - last)
			last = n
		}
		mu.Unlock()
		j.setLive(func(l *live) { l.done = n })
	}
	done(st.Done())
	j.setLive(func(l *live) { l.stage, l.temp, l.size = "Sending to "+dst.name, tmp, size })
	err := dst.r.Upload(ctx, tmp, size, ra, &st, save, done)
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if errors.Is(err, remote.ErrExists) {
			return perFile("could not send: a file named %s is in the way on the server", path.Base(tmp))
		}
		return serverWait(err, dst.name)
	}
	os.Remove(j.path("upload.json"))
	return nil
}

// fromServer copies one file from a server to this computer and proves it
// (C while downloading, then D1 from the disk and S2 from the server).
func (j *Job) fromServer(ctx context.Context, e env, src, dst srvEnd, en *entry) (Rec, error) {
	sp, final := src.path(en.P), dst.path(en.P)
	info, err := src.r.Stat(ctx, sp)
	if errors.Is(err, remote.ErrNotFound) {
		return Rec{I: en.I, St: "gone", N: en.S, P: en.P, Err: "no longer on the server when its turn came"}, nil
	}
	if err != nil {
		return Rec{}, serverWait(err, src.name)
	}
	size, mod := info.Size, info.Mod.UnixNano()
	chg := size != en.S
	if !diskio.Present(dst.disk) || !diskio.VolumeMounted(existingDir(final)) {
		return Rec{}, diskio.ErrGone
	}
	if _, err := os.Lstat(final); err == nil {
		j.setLive(func(l *live) { l.stage = "Already at the destination: reading both in full to compare" })
		d1, err := j.hashFile(ctx, e, dst.disk, final, en.I, "d1", false)
		if err != nil {
			return Rec{}, err
		}
		s1, err := j.hashRemote(ctx, e, src.r, src.name, sp, size, mod, en.I, "s1", true, true)
		if err != nil {
			return Rec{}, err
		}
		if s1 == d1 {
			return Rec{I: en.I, St: "identical", N: size, S1: s1.String(), D1: d1.String()}, nil
		}
		return Rec{}, perFile("a different file with this name is already at the destination; it was left as it is")
	}
	tmp := tempName(final)
	var c, d1, s2 sum
	for try := 1; ; try++ {
		hs := newHasher(j.algo())
		if err := j.download(ctx, e, src, dst, sp, size, mod, tmp, en.I, hs); err != nil {
			return Rec{}, err
		}
		c = hs.Sum()
		var errD, errS error
		var wg sync.WaitGroup
		if j.st.Spec.S2 != "skip" {
			j.setLive(func(l *live) { l.stage = "Reading back the destination and " + src.name })
			wg.Add(1)
			go func() {
				defer wg.Done()
				s2, errS = j.hashRemote(ctx, e, src.r, src.name, sp, size, mod, en.I, "s2", false, true)
			}()
		} else {
			j.setLive(func(l *live) { l.stage = "Reading back the destination" })
		}
		d1, errD = j.hashFile(ctx, e, dst.disk, tmp, en.I, "d1", false)
		wg.Wait()
		if err := errors.Join(errD, errS); err != nil {
			if errD != nil {
				return Rec{}, errD
			}
			return Rec{}, errS
		}
		if d1 == c && (j.st.Spec.S2 == "skip" || s2 == c) {
			break
		}
		if d1 == c { // the server gave other bytes the second time
			if now, err := src.r.Stat(ctx, sp); err == nil && (now.Size != size || !now.Mod.Equal(info.Mod)) {
				return Rec{}, perFile("the file changed on the server during the copy; the copy was kept as %s", filepath.Base(tmp))
			}
		}
		if try == 2 {
			return Rec{}, perFile("the copy did not read back the same as the server's file twice; it was kept as %s", filepath.Base(tmp))
		}
		j.dropCheckpoints(en.I)
		os.Remove(tmp) // our own unproven copy, made again
	}
	en.M, en.O = mod, 0o644
	if err := j.placeProven(tmp, final, en); err != nil {
		return Rec{}, err
	}
	r := Rec{I: en.I, St: "verified", N: size, C: c.String(), D1: d1.String(), A: 1}
	if j.st.Spec.S2 != "skip" {
		r.St, r.S2 = "strong", s2.String()
	}
	if chg {
		r.Chg, r.Err = true, "the file on the server differed from the list when its turn came; it was copied as found"
	}
	return r, nil
}

// download writes a file from a server to tmp, hashing it as it is written,
// and continues a cut-off download from its last flushed checkpoint.
func (j *Job) download(ctx context.Context, e env, src, dst srvEnd, sp string, size, mod int64, tmp string, idx int, hs *hasher) error {
	start := time.Now()
	j.copySince.Store(start.UnixNano())
	defer func() { j.copySince.Store(0); j.copyNs.Add(int64(time.Since(start))) }()
	var off int64
	if c, ok := j.checkpoint(idx, "copy"); ok && c.Size == size && c.Mtime == mod {
		if fi, err := os.Stat(tmp); err == nil && fi.Size() >= c.Offset && hs.UnmarshalBinary(c.State) == nil && int64(hs.Size()) == c.Offset {
			off = c.Offset
		}
	}
	if off == 0 {
		hs.Reset()
	}
	if v, err := diskio.VolumeOf(existingDir(tmp)); err == nil && v.Free < uint64(size-off)+(64<<20) {
		return fullAt(tmp, size-off+(64<<20))
	}
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		if !diskio.Present(dst.disk) {
			return diskio.ErrGone
		}
		return perFile("could not create folder at destination: %v", err)
	}
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		if !diskio.Present(dst.disk) {
			return diskio.ErrGone
		}
		return perFile("could not write at destination: %v", err)
	}
	defer out.Close()
	if err := out.Truncate(off); err != nil {
		return perFile("could not prepare the copy: %v", err)
	}
	diskio.NoCache(out)
	j.setLive(func(l *live) {
		l.stage, l.temp, l.done, l.size = "Downloading from "+src.name+" (hashed as it is written)", tmp, off, size
	})
	save := func() error {
		if err := diskio.Flush(ctx, dst.disk, out); err != nil {
			return err
		}
		b, _ := hs.MarshalBinary()
		return j.saveCheckpoint(Checkpoint{Index: idx, Stage: "copy", Offset: off, State: b, Size: size, Mtime: mod})
	}
	if size > off {
		body, err := src.r.Open(ctx, sp, off)
		if err != nil {
			return serverWait(err, src.name)
		}
		defer body.Close()
		n := min(ChunkSize, max(e.budget.Total()/4, 1<<20), size-off)
		lease, err := e.budget.Acquire(ctx, n)
		if err != nil {
			return err
		}
		defer e.budget.Release(lease)
		buf, last := lease.B[:n], off
		for off < size {
			k, rerr := io.ReadFull(body, buf[:min(int64(len(buf)), size-off)])
			if k > 0 {
				if err := diskio.WriteChunk(ctx, dst.disk, out, buf[:k], off); err != nil {
					if ctx.Err() != nil {
						save()
						return context.Cause(ctx)
					}
					if errors.Is(err, diskio.ErrGone) {
						return err
					}
					return perFile("could not write at destination: %v", err)
				}
				hs.Write(buf[:k])
				off += int64(k)
				j.copyBytes.Add(int64(k))
				j.dstBytes.Add(int64(k))
				j.setLive(func(l *live) { l.done = off })
				if off-last >= CheckpointEvery && off < size {
					if err := save(); err != nil {
						return err
					}
					last = off
				}
			}
			if rerr != nil && off < size {
				if off > last {
					save()
				}
				if ctx.Err() != nil {
					return context.Cause(ctx)
				}
				if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
					return perFile("the server sent %d of %d bytes", off, size)
				}
				return serverWait(rerr, src.name)
			}
		}
		// A server that sends more than it said it holds has changed the file.
		var one [1]byte
		if k, _ := body.Read(one[:]); k > 0 {
			return perFile("the file on the server is longer than when it was listed")
		}
	}
	if err := diskio.Flush(ctx, dst.disk, out); err != nil {
		if errors.Is(err, diskio.ErrGone) {
			return err
		}
		return perFile("could not flush the copy to the disk: %v", err)
	}
	return nil
}

// hashRemote downloads a file and hashes it, writing nothing to disk. It
// continues from a checkpoint and saves one every CheckpointEvery bytes and
// when interrupted. mod 0 means the file's time is not checked (our own
// upload, which a server stamps when it receives it).
func (j *Job) hashRemote(ctx context.Context, e env, r remote.Remote, name, p string, size, mod int64, idx int, stage string, show, isSrc bool) (sum, error) {
	h := newHasher(j.algo())
	var off int64
	if c, ok := j.checkpoint(idx, stage); ok && c.Size == size && c.Mtime == mod && h.UnmarshalBinary(c.State) == nil && int64(h.Size()) == c.Offset {
		off = c.Offset
	} else {
		h.Reset()
	}
	save := func() error {
		b, _ := h.MarshalBinary()
		return j.saveCheckpoint(Checkpoint{Index: idx, Stage: stage, Offset: off, State: b, Size: size, Mtime: mod})
	}
	if show {
		j.setLive(func(l *live) { l.done, l.size = off, size })
	}
	if size > off {
		body, err := r.Open(ctx, p, off)
		if errors.Is(err, remote.ErrNotFound) {
			return sum{}, perFile("not found on %s: %s", name, p)
		}
		if err != nil {
			return sum{}, serverWait(err, name)
		}
		defer body.Close()
		n := min(ChunkSize, max(e.budget.Total()/8, 1<<20), size-off)
		lease, err := e.budget.Acquire(ctx, n)
		if err != nil {
			return sum{}, err
		}
		defer e.budget.Release(lease)
		buf, last := lease.B[:n], off
		for off < size {
			k, rerr := io.ReadFull(body, buf[:min(int64(len(buf)), size-off)])
			if k > 0 {
				h.Write(buf[:k])
				off += int64(k)
				if isSrc {
					j.srcBytes.Add(int64(k))
				} else {
					j.dstBytes.Add(int64(k))
				}
				if show {
					j.setLive(func(l *live) { l.done = off })
				}
				if off-last >= CheckpointEvery && off < size {
					save()
					last = off
				}
			}
			if rerr != nil && off < size {
				if off > last {
					save()
				}
				if ctx.Err() != nil {
					return sum{}, context.Cause(ctx)
				}
				if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
					return sum{}, perFile("%s sent %d of %d bytes", name, off, size)
				}
				return sum{}, serverWait(rerr, name)
			}
		}
		var one [1]byte
		if k, _ := body.Read(one[:]); k > 0 {
			return sum{}, perFile("the file on %s is longer than expected; it may have changed", name)
		}
	}
	if ctx.Err() != nil {
		return sum{}, context.Cause(ctx)
	}
	return h.Sum(), nil
}

// diskReaderAt reads a local source for an upload through the disk's limit
// and stall timer.
type diskReaderAt struct {
	ctx context.Context
	d   *diskio.Disk
	f   *os.File
}

func (x diskReaderAt) ReadAt(b []byte, off int64) (int, error) {
	n, err := diskio.ReadChunk(x.ctx, x.d, x.f, b, off)
	if err == nil && n < len(b) {
		err = io.EOF
	}
	return n, err
}

// remoteReaderAt reads a file on a server for an upload to another server.
// Reads in order share one download; a read elsewhere starts a new one.
type remoteReaderAt struct {
	ctx  context.Context
	r    remote.Remote
	p    string
	mu   sync.Mutex
	body io.ReadCloser
	pos  int64
}

func (x *remoteReaderAt) ReadAt(b []byte, off int64) (int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.body == nil || x.pos != off {
		if x.body != nil {
			x.body.Close()
			x.body = nil
		}
		body, err := x.r.Open(x.ctx, x.p, off)
		if err != nil {
			return 0, err
		}
		x.body, x.pos = body, off
	}
	n, err := io.ReadFull(x.body, b)
	x.pos += int64(n)
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	if err != nil {
		x.body.Close()
		x.body = nil
	}
	return n, err
}

func (x *remoteReaderAt) Close() {
	x.mu.Lock()
	if x.body != nil {
		x.body.Close()
		x.body = nil
	}
	x.mu.Unlock()
}
