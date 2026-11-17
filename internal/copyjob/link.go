package copyjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/fsx"
	"github.com/YC-BB-20/shuttle-server/internal/limits"
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
	// v0.2.91: Vimeo and SoundCloud.
	Site    string `json:"site,omitempty"`    // "" (a file link), "vimeo", "soundcloud"
	Quality string `json:"quality,omitempty"` // the chosen stream
	Height  int    `json:"height,omitempty"`  // a Vimeo stream's height, to find it again
	Est     int64  `json:"est,omitempty"`     // expected size, for the bar
	Title   string `json:"title,omitempty"`
}

var linkClient = linkdl.Client()

// LinkInfo looks up pasted links (one per line or separated by spaces) for
// the Add link pop-up. No link is in the answer.
type LinkInfo struct {
	N int `json:"n"`
	linkdl.Info
	Error     string           `json:"error,omitempty"`
	Site      string           `json:"site,omitempty"` // "vimeo", "soundcloud"
	Title     string           `json:"title,omitempty"`
	Duration  float64          `json:"duration,omitempty"`
	Qualities []linkdl.Quality `json:"qualities,omitempty"` // best first
}

// SplitLinks splits pasted text into links, at most linkdl.MaxLinks.
func SplitLinks(text string) []string {
	f := strings.Fields(text)
	if len(f) > linkdl.MaxLinks {
		f = f[:linkdl.MaxLinks]
	}
	return f
}

// linkWorkers is how many links are looked up at once.
const linkWorkers = 4

// eachLink runs fn for i in [0, n) on a fixed pool of linkWorkers.
func eachLink(n int, fn func(i int)) {
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(linkWorkers, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// LookUpLinks probes the links, a few at a time; the answer keeps their order.
func (m *Manager) LookUpLinks(ctx context.Context, raws []string, password string) []LinkInfo {
	out := make([]LinkInfo, len(raws))
	eachLink(len(raws), func(i int) {
		raw := raws[i]
		r := LinkInfo{N: i + 1}
		if site := siteOf(raw); site != "" {
			r.Site = site
			if in, err := m.lookUpSite(ctx, site, raw, password); err != nil {
				r.Error, r.Host = err.Error(), site+".com"
			} else {
				r.Info, r.Title, r.Duration, r.Qualities = in.Info, in.Title, in.Duration, in.Qualities
			}
			out[i] = r
			return
		}
		link, why := linkdl.Parse(raw)
		if link == "" {
			r.Error = linkdl.Why(why)
		} else if in, err := linkdl.Probe(ctx, linkClient, raw, link); err != nil {
			r.Error, r.Host = err.Error(), linkdl.Host(link)
		} else {
			r.Info = in
		}
		out[i] = r
	})
	return out
}

// AddLink checks a link again and queues its download into folder dest.
func (m *Manager) AddLink(ctx context.Context, raw, dest string, o LinkOpts) (*Job, error) {
	j, errs, err := m.AddLinks(ctx, []string{raw}, dest, o)
	if err == nil && j == nil && len(errs) > 0 {
		err = errors.New(errs[0])
	}
	return j, err
}

// linkSpecOf checks one link again and says what its download is.
func (m *Manager) linkSpecOf(ctx context.Context, raw string, o LinkOpts) (link string, ls *LinkSpec, err error) {
	site := siteOf(raw)
	link, why := strings.TrimSpace(raw), ""
	if site == "" {
		link, why = linkdl.Parse(raw)
	}
	if link == "" {
		return "", nil, errors.New(linkdl.Why(why))
	}
	if site != "" {
		ls, err = m.siteSpec(ctx, site, raw, o)
		return link, ls, err
	}
	in, err := linkdl.Probe(ctx, linkClient, raw, link)
	if err != nil {
		return "", nil, err
	}
	return link, &LinkSpec{Name: in.Name, Sub: in.Sub, Size: in.Size, Resumable: in.Resumable, Share: in.Share, Host: in.Host}, nil
}

// linkRel is where an item goes inside the destination, with "/".
func linkRel(ls LinkSpec) string {
	if ls.Sub == "" {
		return ls.Name
	}
	return ls.Sub + "/" + ls.Name
}

// cmpLinkPath orders items as the Files tab lists them: inside each folder,
// its folders first, then its own files, names A–Z.
func cmpLinkPath(a, b LinkSpec) int {
	pa, pb := strings.Split(linkRel(a), "/"), strings.Split(linkRel(b), "/")
	for k := 0; k < len(pa) && k < len(pb); k++ {
		la, lb := k == len(pa)-1, k == len(pb)-1 // the name itself, not a folder
		if la != lb {
			if la {
				return 1
			}
			return -1
		}
		if c := strings.Compare(strings.ToLower(pa[k]), strings.ToLower(pb[k])); c != 0 {
			return c
		}
		if c := strings.Compare(pa[k], pb[k]); c != 0 {
			return c
		}
	}
	return len(pa) - len(pb)
}

// groupName names a job of several links: their share, else the one folder
// they all sit in, else how many files.
func groupName(items []LinkSpec) string {
	share, top := items[0].Share, strings.SplitN(items[0].Sub, "/", 2)[0]
	for _, it := range items[1:] {
		if it.Share != share {
			share = ""
		}
		if strings.SplitN(it.Sub, "/", 2)[0] != top {
			top = ""
		}
	}
	switch {
	case share != "":
		return share
	case top != "":
		return top
	}
	return fmt.Sprintf("%d files", len(items))
}

// AddLinks checks pasted links again and queues them into folder dest as one
// job (v0.2.92): one link as before; several as one job whose files keep the
// folders their links name (#rq-path), downloaded one after another in the
// order of a folder listing. A link that fails is reported in errs and left
// out; err is for the destination, or when no link could be added.
func (m *Manager) AddLinks(ctx context.Context, raws []string, dest string, o LinkOpts) (*Job, []string, error) {
	if len(raws) == 0 {
		return nil, nil, errors.New("paste one or more download links")
	}
	if len(raws) > linkdl.MaxLinks {
		raws = raws[:linkdl.MaxLinks]
	}
	dest, err := filepath.Abs(strings.TrimSpace(dest))
	if err != nil || dest == "" {
		return nil, nil, errors.New("choose a folder to save to")
	}
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		return nil, nil, errors.New("choose a folder that exists to save to")
	}
	if !diskio.VolumeMounted(dest) {
		return nil, nil, errors.New("the disk of that folder is not connected")
	}
	type found struct {
		link string
		ls   *LinkSpec
		err  error
	}
	got := make([]found, len(raws))
	eachLink(len(raws), func(i int) {
		link, ls, err := m.linkSpecOf(ctx, raws[i], o)
		got[i] = found{link, ls, err}
	})
	var errs []string
	type item struct {
		link string
		ls   LinkSpec
	}
	var ok []item
	for _, f := range got {
		if f.err != nil {
			errs = append(errs, f.err.Error())
			continue
		}
		ok = append(ok, item{f.link, *f.ls})
	}
	if len(ok) == 0 {
		return nil, errs, nil
	}
	slices.SortStableFunc(ok, func(a, b item) int { return cmpLinkPath(a.ls, b.ls) })
	dv, err := diskio.VolumeOf(dest)
	if err != nil {
		return nil, errs, err
	}
	now := time.Now()
	id, err := m.newJobDir(now.Format("20060102-150405.000"))
	if err != nil {
		return nil, errs, err
	}
	first := ok[0].ls
	spec := Spec{Kind: KindLink, Source: "https://" + first.Host, Dest: dest, Link: &first, Algo: "xxh128"}
	var bytes int64
	links := make([]string, len(ok))
	for i, it := range ok {
		links[i] = it.link
		bytes += max(0, it.ls.Size, it.ls.Est)
	}
	if len(ok) > 1 {
		spec.Items = make([]LinkSpec, len(ok))
		for i, it := range ok {
			spec.Items[i] = it.ls
		}
		spec.LinkName = groupName(spec.Items)
	}
	j := &Job{dir: filepath.Join(m.Dir, id), st: state{FormatVersion: FormatVersion, ID: id, Created: now, Choice: "run", Phase: "copy",
		Spec: spec, Files: len(ok), Bytes: bytes, DestVol: dv.ID}}
	if err := m.saveLink(id, links, o.Password); err != nil {
		os.RemoveAll(j.dir) // our own empty job folder
		return nil, errs, errors.New("the links could not be kept on this computer")
	}
	if err := j.save(); err != nil {
		m.dropLink(id)
		return nil, errs, err
	}
	m.mu.Lock()
	m.jobs = append(m.jobs, j)
	m.mu.Unlock()
	if len(ok) == 1 {
		m.Log.Info("link download added", "job", id, "site", first.Host, "name", first.Name, "size", max(first.Size, first.Est), "destination", dest)
	} else {
		m.Log.Info("link downloads added as one job", "job", id, "name", spec.LinkName, "files", len(ok), "size", bytes, "destination", dest)
	}
	m.poke()
	return j, errs, nil
}

func (m *Manager) linkPath(id string) string { return filepath.Join(m.Secrets, id+".json") }

func (m *Manager) saveLink(id string, links []string, password string) error {
	if m.Secrets == "" {
		return errors.New("no secrets folder")
	}
	if err := os.MkdirAll(m.Secrets, 0o700); err != nil {
		return err
	}
	sec := map[string]any{"url": links[0]}
	if len(links) > 1 {
		sec["urls"] = links // a job of several links, in the order of its items
	}
	if password != "" {
		sec["password"] = password // a Vimeo video's password, kept like the link
	}
	b, _ := json.Marshal(sec)
	return fsx.WriteFileAtomic(m.linkPath(id), b, 0o600)
}

// readLink is the link of item at (0 for a job of one link).
func (m *Manager) readLink(id string, at int) (link, password string) {
	b, err := os.ReadFile(m.linkPath(id))
	if err != nil {
		return "", ""
	}
	var x struct {
		URL, Password string
		URLs          []string
	}
	json.Unmarshal(b, &x)
	if len(x.URLs) > 0 {
		if at < 0 || at >= len(x.URLs) {
			return "", x.Password
		}
		return x.URLs[at], x.Password
	}
	if at != 0 {
		return "", x.Password
	}
	return x.URL, x.Password
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
	link, password := m.readLink(st.ID, st.LinkAt)
	if link == "" {
		m.linkFailed(j, "The link is no longer kept on this computer; add the download again")
		return
	}
	final, part := linkTarget(st)
	if need := linkNeed(st); need > 0 && !st.SpaceOK {
		have := int64(0)
		if fi, err := os.Stat(part); err == nil {
			have = fi.Size()
		}
		if short, err := m.spaceFor(filepath.Dir(final), max(0, need-have), j); err == nil && short != nil {
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
		*l = live{file: linkRel(*st.Spec.Link), stage: "Downloading", size: max(0, st.Spec.Link.Size, st.Spec.Link.Est)}
	})
	j.startWork(time.Now())
	var got int64
	var hash string
	switch st.Spec.Link.Site {
	case "vimeo":
		if got, err = m.fetchVimeo(jctx, j, dd, link, password, part); err == nil {
			hash, err = m.readBack(jctx, j, dd, part, got, "")
		}
	case "soundcloud":
		if got, hash, err = m.fetchSC(jctx, j, dd, link, part); err == nil && hash == "" {
			hash, err = m.readBack(jctx, j, dd, part, got, "")
		}
	default:
		got, hash, err = m.download(jctx, j, dd, link, part, st.Spec.Link)
	}
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
		m.dropSiteState(j)
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
		m.Log.Info("link download finished", "job", st.ID, "site", st.Spec.Link.Host, "name", filepath.Base(dst), "bytes", got, "hash", hash)
		if m.itemDone(j, LinkResult{File: dst, Hash: hash, Bytes: got}) {
			return
		}
		j.mu.Lock()
		j.st.Phase, j.st.Finished, j.st.Bytes, j.st.LinkHash, j.st.LinkFile = "done", time.Now(), got, hash, dst
		j.c.Verified, j.c.BytesProven = 1, got
		j.live = live{}
		took := time.Duration(j.st.WorkNs).Round(time.Second)
		j.mu.Unlock()
		m.dropLink(st.ID) // the file has its name: the link and the progress are no longer needed
		m.dropSiteState(j)
		j.save()
		m.Log.Info("link download job finished", "job", st.ID, "took", took.String())
	case errors.Is(cause, errOwnerPause), ctx.Err() != nil:
		j.setLive(func(l *live) { l.reason, l.missing, l.stage = "", "", "" })
	case errors.As(err, &gone):
		os.Remove(part)
		m.dropSiteState(j)
		if m.itemDone(j, LinkResult{Err: gone.Msg}) {
			m.Log.Warn("one file of a link download job failed; going on with the next", "job", st.ID, "why", gone.Msg)
			return
		}
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

// LinkResult is what became of one file of a job of several links.
type LinkResult struct {
	File  string `json:"file,omitempty"` // where it went
	Hash  string `json:"hash,omitempty"` // its XXH128, read back
	Bytes int64  `json:"bytes,omitempty"`
	Err   string `json:"error,omitempty"` // why it could not be downloaded
}

// linkNeed is the room a link job still needs: the files not yet
// downloaded, as far as the sites said.
func linkNeed(st state) int64 {
	if len(st.Spec.Items) == 0 {
		return max(st.Spec.Link.Size, st.Spec.Link.Est)
	}
	var n int64
	for _, it := range st.Spec.Items[min(st.LinkAt, len(st.Spec.Items)):] {
		n += max(0, it.Size, it.Est)
	}
	return n
}

// itemDone records what became of the file just finished in a job of
// several links and moves on to the next one. It says false for a job of
// one link; for the last file it ends the job and says true.
func (m *Manager) itemDone(j *Job, r LinkResult) bool {
	j.mu.Lock()
	if len(j.st.Spec.Items) == 0 {
		j.mu.Unlock()
		return false
	}
	j.st.LinkDone = append(j.st.LinkDone, r)
	j.st.LinkAt++
	more := j.st.LinkAt < len(j.st.Spec.Items)
	if more {
		next := j.st.Spec.Items[j.st.LinkAt]
		j.st.Spec.Link = &next
		j.st.SpaceOK = false // the room is checked again for what is left
	}
	j.live = live{}
	id, n := j.st.ID, len(j.st.Spec.Items)
	j.mu.Unlock()
	m.dropSiteState(j)
	if more {
		j.save()
		return true
	}
	// The last file: the job is done, failed when any file could not be had.
	j.mu.Lock()
	bad, why := 0, ""
	var bytes, est int64
	for i, x := range j.st.LinkDone {
		if x.Err == "" {
			bytes += x.Bytes
			continue
		}
		bad++
		est += max(0, j.st.Spec.Items[i].Size, j.st.Spec.Items[i].Est)
		if why == "" {
			why = linkRel(j.st.Spec.Items[i]) + ": " + x.Err
		}
	}
	j.st.Phase, j.st.Finished, j.st.Bytes = "done", time.Now(), bytes+est
	if bad > 0 {
		j.st.Error = fmt.Sprintf("%d of %d files could not be downloaded. %s", bad, n, why)
	}
	took := time.Duration(j.st.WorkNs).Round(time.Second)
	j.mu.Unlock()
	m.dropLink(id)
	j.save()
	m.Log.Info("link download job finished", "job", id, "files", n, "failed", bad, "bytes", bytes, "took", took.String())
	return true
}

// linkFailed ends a link job that cannot go on.
func (m *Manager) linkFailed(j *Job, msg string) {
	j.mu.Lock()
	j.st.Phase, j.st.Finished, j.st.Error = "done", time.Now(), msg
	j.live = live{}
	id := j.st.ID
	j.mu.Unlock()
	m.dropLink(id)
	m.dropSiteState(j)
	j.save()
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
	defer func() { m.Budget.Release(lease) }()
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
	var want string
	if from == 0 {
		want = stream.Sum128().String()
	}
	m.Budget.Release(lease) // the read-back takes its own buffer
	lease = limits.Lease{}
	sum, err := m.readBack(ctx, j, d, part, got, want)
	return got, sum, err
}

// readBack reads a finished download back from the disk and hashes it: what
// is on the disk is what arrived. want, when known, is the hash of the bytes
// as they arrived.
func (m *Manager) readBack(ctx context.Context, j *Job, d *diskio.Disk, part string, got int64, want string) (string, error) {
	j.setLive(func(l *live) { l.stage, l.done, l.size = "Reading back", 0, got })
	rf, err := diskio.OpenRead(part)
	if err != nil {
		return "", err
	}
	defer rf.Close()
	lease, err := m.Budget.Acquire(ctx, linkChunk)
	if err != nil {
		return "", err
	}
	defer m.Budget.Release(lease)
	buf := lease.B
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
			return "", err
		}
	}
	sum := h.Sum128().String()
	if want != "" && want != sum {
		os.Remove(part)
		return "", linkdl.Retry{Msg: "The file read back differently from what arrived; downloading it again"}
	}
	return sum, nil
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
	if len(st.Spec.Items) > 0 {
		groupView(v, st, l)
		return
	}
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
	n := ls.Size
	if n <= 0 {
		n = ls.Est // a Vimeo or SoundCloud download: the size is an estimate
	}
	if l.stage == "Reading back" && l.size > 0 {
		n = l.size
	}
	if n <= 0 {
		v.Progress, v.CopiedPart, v.ProvenPart = 0, 0, 0
		return
	}
	size := float64(n)
	switch l.stage {
	case "Joining":
		v.CopiedPart, v.ProvenPart = 1, 0
	case "Reading back":
		v.CopiedPart, v.ProvenPart = 1, min(1, float64(l.done)/size)
	default:
		v.CopiedPart, v.ProvenPart = min(1, float64(l.done)/size), 0
	}
	v.Progress = (v.CopiedPart + v.ProvenPart) / 2
}

// groupView fills the view of a job of several links: its name, the bar
// over all its files, and how many are downloaded and checked or failed.
func groupView(v *View, st state, l live) {
	v.LinkName = st.Spec.LinkName
	v.FromDisk, v.FromPath = st.Spec.Link.Host, st.Spec.LinkName
	v.Algo = "xxh128"
	var total, done int64
	ok, bad := 0, 0
	for i, it := range st.Spec.Items {
		est := max(0, it.Size, it.Est)
		if i < len(st.LinkDone) {
			if r := st.LinkDone[i]; r.Err == "" {
				ok++
				done += r.Bytes
				total += r.Bytes
				continue
			}
			bad++
		}
		total += est
	}
	v.Verified, v.Failed, v.Proven, v.Bytes = ok, bad, done, total
	if total <= 0 {
		total = 1
	}
	if st.Phase == "done" {
		v.CopiedPart, v.ProvenPart = 1, float64(done)/float64(total)
		v.Progress = v.ProvenPart
		v.Checks = &Checks{Of: len(st.Spec.Items), Dest: ok, DestAt: st.Finished}
		return
	}
	var w, r int64 // the file now: written and read back
	switch l.stage {
	case "Joining":
		w = max(0, st.Spec.Link.Size, st.Spec.Link.Est)
	case "Reading back":
		w, r = l.size, l.done
	case "Downloading":
		w = l.done
	}
	v.CopiedPart = min(1, float64(done+w)/float64(total))
	v.ProvenPart = min(1, float64(done+r)/float64(total))
	v.Progress = (v.CopiedPart + v.ProvenPart) / 2
}

// linkRows lists a link job's files for the Files tab, in download order:
// done (downloaded and checked), failed, the one downloading now, waiting.
func linkRows(st state, l live) []FileRow {
	items := st.Spec.Items
	if len(items) == 0 && st.Spec.Link != nil {
		items = []LinkSpec{*st.Spec.Link}
	}
	rows := make([]FileRow, len(items))
	for i, it := range items {
		r := FileRow{I: i, Path: linkRel(it), Size: max(0, it.Size, it.Est), Status: "waiting"}
		switch {
		case len(st.Spec.Items) == 0 && st.Phase == "done":
			if st.Error != "" {
				r.Status, r.Why = "failed", st.Error
			} else if st.LinkHash != "" {
				r.Status, r.Size = "proven", st.Bytes
			}
		case i < len(st.LinkDone):
			if x := st.LinkDone[i]; x.Err != "" {
				r.Status, r.Why = "failed", x.Err
			} else {
				r.Status, r.Size = "proven", x.Bytes
			}
		case i == st.LinkAt && st.Phase != "done" && l.stage != "":
			r.Status, r.Stage, r.Done = "working", l.stage, l.done
			if l.size > 0 {
				r.Size = l.size
			}
		}
		rows[i] = r
	}
	return rows
}
