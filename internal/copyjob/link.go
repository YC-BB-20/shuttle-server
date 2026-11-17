package copyjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/fsx"
	"github.com/YC-BB-20/shuttle-server/internal/linkdl"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// Link downloads (v0.2.90): "Add link" puts a download in the same queue as
// the copies, with the same order, pause, stop, waiting and details. The link
// is a secret (it may carry a signature): it lives only in
// secrets/links/<job>.json (0600) and is removed when the job ends. The job
// file keeps only the site's name. The file grows as <name>.shuttle-dl,
// continues from where it stopped, is checked for size, read back and hashed,
// and only then gets its name, never over an existing file.

// KindLink is a link download job.
const KindLink = "link"

func (s Spec) isLink() bool { return s.Kind == KindLink }

// LinkSpec is what a link job knows about its file, without the link.
type LinkSpec struct {
	Name      string `json:"name"`
	Sub       string `json:"sub,omitempty"`   // folder inside the destination, from the link's #rq-path
	Size      int64  `json:"size"`            // -1 when the site does not say
	Resumable bool   `json:"resumable"`       // the site answers a request for part of the file
	Share     string `json:"share,omitempty"` // the share it came from (#rq-share)
	Host      string `json:"host"`
}

var linkClient = linkdl.Client()

// LinkInfo looks up pasted links (one per line or separated by spaces) for
// the Add link pop-up. No link is in the answer.
type LinkInfo struct {
	N int `json:"n"`
	linkdl.Info
	Error string `json:"error,omitempty"`
}

// SplitLinks splits pasted text into links, at most linkdl.MaxLinks.
func SplitLinks(text string) []string {
	f := strings.Fields(text)
	if len(f) > linkdl.MaxLinks {
		f = f[:linkdl.MaxLinks]
	}
	return f
}

// LookUpLinks probes each link, one after another.
func (m *Manager) LookUpLinks(ctx context.Context, raws []string) []LinkInfo {
	out := make([]LinkInfo, 0, len(raws))
	for i, raw := range raws {
		r := LinkInfo{N: i + 1}
		link, why := linkdl.Parse(raw)
		if link == "" {
			r.Error = linkdl.Why(why)
		} else if in, err := linkdl.Probe(ctx, linkClient, raw, link); err != nil {
			r.Error, r.Host = err.Error(), linkdl.Host(link)
		} else {
			r.Info = in
		}
		out = append(out, r)
	}
	return out
}

// AddLink checks a link again and queues its download into folder dest.
func (m *Manager) AddLink(ctx context.Context, raw, dest string) (*Job, error) {
	link, why := linkdl.Parse(raw)
	if link == "" {
		return nil, errors.New(linkdl.Why(why))
	}
	dest, err := filepath.Abs(strings.TrimSpace(dest))
	if err != nil || dest == "" {
		return nil, errors.New("choose a folder to save to")
	}
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		return nil, errors.New("choose a folder that exists to save to")
	}
	if !diskio.VolumeMounted(dest) {
		return nil, errors.New("the disk of that folder is not connected")
	}
	in, err := linkdl.Probe(ctx, linkClient, raw, link)
	if err != nil {
		return nil, err
	}
	dv, err := diskio.VolumeOf(dest)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	id, err := m.newJobDir(now.Format("20060102-150405.000"))
	if err != nil {
		return nil, err
	}
	ls := &LinkSpec{Name: in.Name, Sub: in.Sub, Size: in.Size, Resumable: in.Resumable, Share: in.Share, Host: in.Host}
	j := &Job{dir: filepath.Join(m.Dir, id), st: state{FormatVersion: FormatVersion, ID: id, Created: now, Choice: "run", Phase: "copy",
		Spec:  Spec{Kind: KindLink, Source: "https://" + in.Host, Dest: dest, Link: ls, Algo: "xxh128"},
		Files: 1, Bytes: max(0, in.Size), DestVol: dv.ID}}
	if err := m.saveLink(id, link); err != nil {
		os.RemoveAll(j.dir) // our own empty job folder
		return nil, errors.New("the link could not be kept on this computer")
	}
	if err := j.save(); err != nil {
		m.dropLink(id)
		return nil, err
	}
	m.mu.Lock()
	m.jobs = append(m.jobs, j)
	m.mu.Unlock()
	m.Log.Info("link download added", "job", id, "site", in.Host, "name", in.Name, "size", in.Size, "destination", dest)
	m.poke()
	return j, nil
}

func (m *Manager) linkPath(id string) string { return filepath.Join(m.Secrets, id+".json") }

func (m *Manager) saveLink(id, link string) error {
	if m.Secrets == "" {
		return errors.New("no secrets folder")
	}
	if err := os.MkdirAll(m.Secrets, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"url": link})
	return fsx.WriteFileAtomic(m.linkPath(id), b, 0o600)
}

func (m *Manager) readLink(id string) string {
	b, err := os.ReadFile(m.linkPath(id))
	if err != nil {
		return ""
	}
	var x struct{ URL string }
	json.Unmarshal(b, &x)
	return x.URL
}

func (m *Manager) dropLink(id string) {
	if m.Secrets != "" {
		os.Remove(m.linkPath(id)) // our own secret file
	}
}

// ID is the job's id.
func (j *Job) ID() string { return j.st.ID }

// linkTarget is where a link job's file goes, and its partial file.
func linkTarget(st state) (final, part string) {
	dir := st.Spec.Dest
	if st.Spec.Link.Sub != "" {
		dir = filepath.Join(dir, filepath.FromSlash(st.Spec.Link.Sub))
	}
	final = filepath.Join(dir, st.Spec.Link.Name)
	return final, final + ".shuttle-dl"
}

// freeName is name, or "name (2).ext", "name (3).ext"… when it is taken.
func freeName(p string) string {
	if _, err := os.Lstat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	b := strings.TrimSuffix(p, ext)
	for i := 2; i < 1000; i++ {
		q := fmt.Sprintf("%s (%d)%s", b, i, ext)
		if _, err := os.Lstat(q); err != nil {
			return q
		}
	}
	return p
}

// runLink runs one link download, in the queue's place of a copy.
func (m *Manager) runLink(ctx context.Context, j *Job) {
	j.mu.Lock()
	st := j.st
	j.mu.Unlock()
	if m.missing(j) != "" {
		m.waitMissing(ctx, j, "destination")
		return
	}
	dd, err := m.Disks.For(st.Spec.Dest)
	if err != nil {
		m.waitMissing(ctx, j, "destination")
		return
	}
	if dd.Stalled() {
		m.wait(ctx, j, "A disk is not responding; waiting for it")
		return
	}
	link := m.readLink(st.ID)
	if link == "" {
		m.linkFailed(j, "The link is no longer kept on this computer; add the download again")
		return
	}
	final, part := linkTarget(st)
	if st.Spec.Link.Size > 0 && !st.SpaceOK {
		have := int64(0)
		if fi, err := os.Stat(part); err == nil {
			have = fi.Size()
		}
		if short, err := m.spaceFor(filepath.Dir(final), max(0, st.Spec.Link.Size-have), j); err == nil && short != nil {
			m.holdForSpace(j, *short)
			return
		}
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
	j.setLive(func(l *live) {
		*l = live{file: st.Spec.Link.Name, stage: "Downloading", size: max(0, st.Spec.Link.Size)}
	})
	j.startWork(time.Now())
	got, hash, err := m.download(jctx, j, dd, link, part, st.Spec.Link)
	cause := context.Cause(jctx)
	cancel(nil)
	m.mu.Lock()
	m.running = nil
	m.mu.Unlock()
	j.mu.Lock()
	j.endWorkLocked(time.Now())
	stopped := j.st.Choice == "stopped"
	j.mu.Unlock()
	var gone linkdl.Gone
	switch {
	case stopped:
		os.Remove(part) // our own partial file
		m.dropLink(st.ID)
		m.finishStopped(j)
		m.Log.Info("link download stopped by the owner", "job", st.ID)
	case err == nil:
		dst := freeName(final)
		if err := os.Link(part, dst); err != nil {
			if _, e := os.Lstat(dst); e == nil {
				m.wait(ctx, j, "A file with this name appeared at the destination; trying again shortly")
				return
			}
			if err := os.Rename(part, dst); err != nil { // formats without links (exFAT): checked just above
				m.wait(ctx, j, "Problem: the file could not be given its name ("+err.Error()+"); trying again shortly")
				return
			}
		} else {
			os.Remove(part)
		}
		j.mu.Lock()
		j.st.Phase, j.st.Finished, j.st.Bytes, j.st.LinkHash, j.st.LinkFile = "done", time.Now(), got, hash, dst
		j.c.Verified, j.c.BytesProven = 1, got
		j.live = live{}
		took := time.Duration(j.st.WorkNs).Round(time.Second)
		j.mu.Unlock()
		j.save()
		m.dropLink(st.ID)
		m.Log.Info("link download finished", "job", st.ID, "site", st.Spec.Link.Host, "name", filepath.Base(dst), "bytes", got, "hash", hash, "took", took.String())
	case errors.Is(cause, errOwnerPause), ctx.Err() != nil:
		j.setLive(func(l *live) { l.reason, l.missing, l.stage = "", "", "" })
	case errors.As(err, &gone):
		os.Remove(part)
		m.linkFailed(j, gone.Msg)
	case errors.Is(err, diskio.ErrGone), errors.Is(cause, diskio.ErrGone):
		m.waitMissing(ctx, j, "destination")
	case errors.Is(err, diskio.ErrStalled):
		m.wait(ctx, j, "A disk is not responding; waiting for it")
	default:
		m.Log.Warn("link download stopped; it will continue from where it was", "job", st.ID, "site", st.Spec.Link.Host, "err", err)
		m.wait(ctx, j, err.Error()+" — continuing from where it stopped shortly")
	}
}

// linkFailed ends a link job that cannot go on.
func (m *Manager) linkFailed(j *Job, msg string) {
	j.mu.Lock()
	j.st.Phase, j.st.Finished, j.st.Error = "done", time.Now(), msg
	j.live = live{}
	id := j.st.ID
	j.mu.Unlock()
	j.save()
	m.dropLink(id)
	m.Log.Warn("link download failed", "job", id, "why", msg)
}

const linkChunk = 1 << 20

// download fetches the link into part (continuing it), checks its size,
// flushes it, reads it back and returns its size and XXH128.
func (m *Manager) download(ctx context.Context, j *Job, d *diskio.Disk, link, part string, ls *LinkSpec) (int64, string, error) {
	if !diskio.VolumeMounted(j.st.Spec.Dest) { // never write under an unmounted disk's path
		return 0, "", diskio.ErrGone
	}
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return 0, "", err
	}
	from := int64(0)
	if fi, err := os.Stat(part); err == nil {
		from = fi.Size()
	}
	if ls.Size >= 0 && from > ls.Size || from > 0 && !ls.Resumable {
		os.Remove(part)
		from = 0
	}
	body, err := linkdl.Open(ctx, linkClient, link, from)
	if errors.Is(err, linkdl.ErrNoResume) {
		os.Remove(part)
		from = 0
		body, err = linkdl.Open(ctx, linkClient, link, 0)
	}
	if err != nil {
		return 0, "", err
	}
	defer body.Close()
	if from > 0 {
		m.Log.Info("link download continues", "job", j.st.ID, "from_bytes", from)
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	lease, err := m.Budget.Acquire(ctx, linkChunk)
	if err != nil {
		return 0, "", err
	}
	defer m.Budget.Release(lease)
	buf := lease.B
	// A connection that sends nothing for a minute is closed; the queue then
	// waits and continues from the last byte written.
	stall := time.AfterFunc(time.Minute, func() { body.Close() })
	defer stall.Stop()
	got, sinceFlush := from, int64(0)
	stream := xxh3.New() // the bytes as received, when the download starts at 0
	for {
		n, rerr := io.ReadFull(body, buf)
		if n > 0 {
			stall.Reset(time.Minute)
			if err := diskio.WriteChunk(ctx, d, f, buf[:n], got); err != nil {
				return got, "", err
			}
			if from == 0 {
				stream.Write(buf[:n])
			}
			got += int64(n)
			sinceFlush += int64(n)
			if sinceFlush >= 256<<20 {
				diskio.Flush(ctx, d, f)
				sinceFlush = 0
			}
			j.setLive(func(l *live) { l.done = got })
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF && ls.Size >= 0 && got == ls.Size {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return got, "", ctx.Err()
			}
			return got, "", linkdl.Retry{Msg: fmt.Sprintf("The download from %s stopped after %s", ls.Host, humanBytes(got))}
		}
	}
	if ls.Size >= 0 && got != ls.Size {
		if got > ls.Size {
			os.Remove(part)
		}
		return got, "", linkdl.Retry{Msg: fmt.Sprintf("Size check failed: %s received, %s expected", humanBytes(got), humanBytes(ls.Size))}
	}
	if got == 0 {
		os.Remove(part)
		return 0, "", linkdl.Retry{Msg: "The site sent an empty file"}
	}
	if err := diskio.Flush(ctx, d, f); err != nil {
		return got, "", err
	}
	f.Close()
	// Read the file back from the disk and hash it: what is on the disk is
	// what arrived.
	j.setLive(func(l *live) { l.stage, l.done, l.size = "Reading back", 0, got })
	rf, err := diskio.OpenRead(part)
	if err != nil {
		return got, "", err
	}
	defer rf.Close()
	h := xxh3.New()
	for off := int64(0); off < got; {
		n, err := diskio.ReadChunk(ctx, d, rf, buf, off)
		if n > 0 {
			h.Write(buf[:n])
			off += int64(n)
			j.setLive(func(l *live) { l.done = off })
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return got, "", err
		}
	}
	sum := h.Sum128().String()
	if from == 0 && stream.Sum128().String() != sum {
		os.Remove(part)
		return got, "", linkdl.Retry{Msg: "The file read back differently from what arrived; downloading it again"}
	}
	return got, sum, nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%d bytes", n)
}

// linkView fills a link job's view: its name, site, bar and proof.
func linkView(v *View, st state, l live) {
	ls := st.Spec.Link
	v.LinkName, v.LinkHash, v.LinkFile = ls.Name, st.LinkHash, st.LinkFile
	v.FromDisk, v.FromPath = ls.Host, ls.Name
	v.Algo = "xxh128"
	if st.Phase == "done" {
		v.Progress, v.CopiedPart, v.ProvenPart = 1, 1, 0
		if st.LinkHash != "" {
			v.ProvenPart, v.Verified, v.Proven = 1, 1, st.Bytes
			v.Checks = &Checks{Of: 1, Dest: 1, DestAt: st.Finished}
		}
		return
	}
	size := float64(max(1, ls.Size))
	if ls.Size <= 0 {
		v.Progress, v.CopiedPart, v.ProvenPart = 0, 0, 0
		return
	}
	switch l.stage {
	case "Reading back":
		v.CopiedPart, v.ProvenPart = 1, min(1, float64(l.done)/size)
	default:
		v.CopiedPart, v.ProvenPart = min(1, float64(l.done)/size), 0
	}
	v.Progress = (v.CopiedPart + v.ProvenPart) / 2
}
