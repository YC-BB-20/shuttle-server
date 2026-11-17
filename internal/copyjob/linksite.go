package copyjob

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/fsx"
	"github.com/YC-BB-20/shuttle-server/internal/linkdl"
	"github.com/YC-BB-20/shuttle-server/internal/mp4"
)

// Vimeo and SoundCloud links (v0.2.91), rebuilt from what Shuttle Router
// does. A Vimeo video arrives as many small pieces of picture and sound; they
// are joined into one ordinary MP4 as they arrive (no ffmpeg). A SoundCloud
// track is the original upload when the artist allows it, otherwise the best
// stream, with its title, artist, date and cover written into the file.
//
// The file grows as <name>.shuttle-dl like any link download. What has
// arrived so far is kept in the job folder (site.json, written atomically;
// site.init, the joined header; site.moofs, each piece's table), so a stopped
// download continues from the last piece kept, not from the start.

// siteOf says which site a pasted link is for: "vimeo", "soundcloud" or "".
func siteOf(raw string) string {
	if id, _ := linkdl.VimeoID(raw); id != "" {
		return "vimeo"
	}
	if l, why := linkdl.SCLink(raw); l != "" || why == "set" {
		return "soundcloud"
	}
	return ""
}

// LinkOpts are the choices made in the Add link pop-up for a site link.
type LinkOpts struct {
	Password string // a Vimeo video's password
	Quality  string // a quality id from the look-up; "" means the best
}

type siteInfo struct {
	Info      linkdl.Info
	Title     string
	Duration  float64
	Qualities []linkdl.Quality
}

const siteTries = 5

// siteWait is the pause before the next try of a piece, times the try.
var siteWait = time.Second

// lookUpSite opens a Vimeo video or a SoundCloud track and lists its qualities.
func (m *Manager) lookUpSite(ctx context.Context, site, raw, password string) (siteInfo, error) {
	switch site {
	case "vimeo":
		v, err := linkdl.OpenVimeo(ctx, linkdl.VimeoClient(), raw, password)
		if err != nil {
			return siteInfo{}, err
		}
		qs := v.Qualities()
		return siteInfo{Info: linkdl.Info{Name: linkdl.VimeoFileName(v.Title, v.ID), Size: qs[0].Bytes, Type: "video/mp4",
			Resumable: true, Host: "vimeo.com", Sub: linkdl.SubFolder(raw)}, Title: v.Title, Duration: v.Duration, Qualities: qs}, nil
	case "soundcloud":
		s, err := openSC(ctx, raw)
		if err != nil {
			return siteInfo{}, err
		}
		var qs []linkdl.Quality
		for _, q := range s.Qualities() {
			qs = append(qs, q.Quality)
		}
		if len(qs) == 0 {
			return siteInfo{}, linkdl.Gone{Msg: "SoundCloud offers no way to download this track"}
		}
		return siteInfo{Info: linkdl.Info{Name: linkdl.SCFileName(s.Artist(), s.T.Title, qs[0].Ext), Size: qs[0].Bytes,
			Resumable: true, Host: "soundcloud.com", Sub: linkdl.SubFolder(raw)}, Title: s.T.Title, Duration: s.T.Duration / 1000, Qualities: qs}, nil
	}
	return siteInfo{}, errors.New("an unknown site")
}

func openSC(ctx context.Context, raw string) (*linkdl.SC, error) {
	link, why := linkdl.SCLink(raw)
	if why == "set" {
		return nil, linkdl.Gone{Msg: "This is a playlist or album. Paste the link of one track."}
	}
	if link == "" {
		return nil, linkdl.Gone{Msg: "This is not a SoundCloud track link"}
	}
	return linkdl.OpenSC(ctx, linkClient, link)
}

// siteSpec looks a site link up again and fixes the chosen quality and name.
func (m *Manager) siteSpec(ctx context.Context, site, raw string, o LinkOpts) (*LinkSpec, error) {
	in, err := m.lookUpSite(ctx, site, raw, o.Password)
	if err != nil {
		return nil, err
	}
	q := in.Qualities[0]
	if o.Quality != "" {
		found := false
		for _, x := range in.Qualities {
			if x.ID == o.Quality {
				q, found = x, true
				break
			}
		}
		if !found {
			return nil, errors.New("that quality is no longer offered; look the link up again")
		}
	}
	ls := &LinkSpec{Name: in.Info.Name, Sub: in.Info.Sub, Size: -1, Resumable: true, Host: in.Info.Host, Site: site,
		Quality: q.ID, Height: q.Height, Est: q.Bytes, Title: in.Title}
	if site == "soundcloud" {
		ext := q.Ext
		if q.ID == "original" && ext == "" { // the artist's file: its name says what it is
			if s, err := openSC(ctx, raw); err == nil {
				for _, sq := range s.Qualities() {
					if sq.ID == "original" {
						if u, err := s.StreamURL(ctx, sq); err == nil {
							if pi, err := linkdl.Probe(ctx, linkClient, u, u); err == nil {
								ext = strings.TrimPrefix(filepath.Ext(pi.Name), ".")
								ls.Est = max(ls.Est, pi.Size)
							}
						}
					}
				}
			}
		}
		base := strings.TrimSuffix(in.Info.Name, filepath.Ext(in.Info.Name))
		if ext == "" {
			ext = "mp3"
		}
		ls.Name = base + "." + strings.ToLower(ext)
	}
	return ls, nil
}

// siteState is how far a site download has come. Key names the streams, so a
// download continues only into the same streams.
type siteState struct {
	Key  string `json:"key"`
	Room int    `json:"room,omitempty"` // space kept for the table of contents
	Head int64  `json:"head"`           // bytes before the samples (MP4) or the ID3 tag (MP3)
	Next int    `json:"next"`           // the next piece
	Data uint64 `json:"data"`           // sample bytes kept, after Head
	Recs int64  `json:"recs,omitempty"` // bytes kept in site.moofs
}

func (m *Manager) dropSiteState(j *Job) {
	for _, n := range []string{"site.json", "site.init", "site.moofs"} {
		os.Remove(j.path(n)) // our own files
	}
}

// siteFile writes a site download: pieces one after another into part, with
// what has arrived kept in the job folder.
type siteFile struct {
	m       *Manager
	j       *Job
	d       *diskio.Disk
	part    string
	f, recs *os.File
	st      siteState
	init    []byte
	buf     []byte
	since   int
	sinceB  uint64
	what    string // "Vimeo", "SoundCloud", for messages
	release func()
	done    bool
}

// openSite continues the download in part when it was for the same streams,
// or starts it with the bytes head() gives (and, for MP4, the joined header).
func (m *Manager) openSite(ctx context.Context, j *Job, d *diskio.Disk, part, key, what string, start func() (head, init []byte, room int, err error)) (*siteFile, error) {
	if !diskio.VolumeMounted(j.st.Spec.Dest) {
		return nil, diskio.ErrGone
	}
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return nil, err
	}
	s := &siteFile{m: m, j: j, d: d, part: part, what: what}
	var old siteState
	b, err := os.ReadFile(j.path("site.json"))
	resume := err == nil && json.Unmarshal(b, &old) == nil && old.Key == key
	if resume {
		fi, e1 := os.Stat(part)
		ri, e2 := os.Stat(j.path("site.moofs"))
		init, e3 := os.ReadFile(j.path("site.init"))
		resume = e1 == nil && fi.Size() >= old.Head+int64(old.Data) &&
			(old.Room == 0 || e2 == nil && ri.Size() >= old.Recs && e3 == nil && len(init) > 8)
		s.init = init
	}
	if resume {
		s.st = old
		if s.f, err = os.OpenFile(part, os.O_WRONLY, 0o644); err != nil {
			return nil, err
		}
		if err := s.f.Truncate(old.Head + int64(old.Data)); err != nil {
			s.f.Close()
			return nil, err
		}
		if old.Room > 0 {
			if s.recs, err = os.OpenFile(j.path("site.moofs"), os.O_WRONLY, 0o600); err == nil {
				err = s.recs.Truncate(old.Recs)
			}
			if err != nil {
				s.close()
				return nil, err
			}
		}
		m.Log.Info("link download continues", "job", j.st.ID, "site", what, "piece", old.Next, "from_bytes", old.Head+int64(old.Data))
	} else {
		head, init, room, err := start()
		if err != nil {
			return nil, err
		}
		s.st, s.init = siteState{Key: key, Room: room, Head: int64(len(head))}, init
		if room > 0 {
			if err := fsx.WriteFileAtomic(j.path("site.init"), init, 0o600); err != nil {
				return nil, err
			}
			if s.recs, err = os.OpenFile(j.path("site.moofs"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err != nil {
				return nil, err
			}
		}
		if s.f, err = os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err != nil {
			s.close()
			return nil, err
		}
		if err := diskio.WriteChunk(ctx, d, s.f, head, 0); err != nil {
			s.close()
			return nil, err
		}
		if err := s.keep(ctx); err != nil {
			s.close()
			return nil, err
		}
	}
	lease, err := m.Budget.Acquire(ctx, linkChunk)
	if err != nil {
		s.close()
		return nil, err
	}
	s.buf = lease.B
	s.release = func() { m.Budget.Release(lease) }
	return s, nil
}

// end keeps what arrived (also after a pause or a failure), then closes.
func (s *siteFile) end() {
	if s.since > 0 && !s.done {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := s.keep(ctx); err != nil {
			s.m.Log.Warn("could not keep how far the download came; it will start this piece again", "job", s.j.st.ID, "err", err)
		}
		cancel()
	}
	s.close()
}

func (s *siteFile) close() {
	if s.f != nil {
		s.f.Close()
	}
	if s.recs != nil {
		s.recs.Close()
	}
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

// keep makes what has arrived safe on the disks, then records how far it is.
func (s *siteFile) keep(ctx context.Context) error {
	if err := diskio.Flush(ctx, s.d, s.f); err != nil {
		return err
	}
	if s.recs != nil {
		if err := s.recs.Sync(); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(s.st)
	s.since, s.sinceB = 0, 0
	return fsx.WriteFileAtomic(s.j.path("site.json"), b, 0o600)
}

func (s *siteFile) at() int64 { return s.st.Head + int64(s.st.Data) }

// piece fetches piece s.st.Next with up to siteTries tries. open gives the
// piece for a try; join says whether it is an MP4 piece to join (track) or
// bytes to add as they are.
func (s *siteFile) piece(ctx context.Context, open func(try int) (io.ReadCloser, error), join bool, track uint32) error {
	var last error
	for try := 0; try < siteTries; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(try) * siteWait):
			}
		}
		rc, err := open(try) // an expired address is tried again: the next try asks the site afresh
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			last = err
			continue
		}
		var werr error
		off := s.at()
		write := func(b []byte) error {
			if werr = diskio.WriteChunk(ctx, s.d, s.f, b, off); werr == nil {
				off += int64(len(b))
				s.j.setLive(func(l *live) { l.done = off })
			}
			return werr
		}
		var wrote uint64
		var recs []byte
		if join {
			var moofs int
			wrote, _, moofs, recs, err = mp4.CopyPayload(rc, write, s.st.Data, track, s.buf)
			if err == nil && moofs == 0 {
				err = errors.New("a piece without pictures or sound")
			}
		} else {
			for {
				n, rerr := io.ReadFull(rc, s.buf)
				if n > 0 {
					if write(s.buf[:n]) != nil {
						err = werr
						break
					}
					wrote += uint64(n)
				}
				if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
					break
				}
				if rerr != nil {
					err = rerr
					break
				}
			}
			if err == nil && wrote == 0 {
				err = errors.New("an empty piece")
			}
		}
		rc.Close()
		if werr != nil {
			return werr // the disk, not the site
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			last = err
			continue
		}
		if len(recs) > 0 {
			if _, err := s.recs.WriteAt(recs, s.st.Recs); err != nil {
				return err
			}
			s.st.Recs += int64(len(recs))
		}
		s.st.Data += wrote
		s.st.Next++
		s.since++
		s.sinceB += wrote
		if s.since >= 32 || s.sinceB >= 128<<20 {
			return s.keep(ctx)
		}
		return nil
	}
	return linkdl.Retry{Msg: fmt.Sprintf("%s stopped sending piece %d (%v)", s.what, s.st.Next+1, last)}
}

// finish writes the table of contents (MP4) and returns the file's size.
func (s *siteFile) finish(ctx context.Context) (int64, error) {
	s.done = true // the state is dropped once the file has its name
	if err := s.f.Truncate(s.at()); err != nil {
		return 0, err
	}
	if s.st.Room > 0 {
		s.j.setLive(func(l *live) { l.stage = "Joining" })
		if err := s.recs.Sync(); err != nil {
			return 0, err
		}
		rf, err := os.Open(s.j.path("site.moofs"))
		if err != nil {
			return 0, err
		}
		defer rf.Close()
		msg, err := mp4.Finish(s.init, bufio.NewReader(io.LimitReader(rf, s.st.Recs)), s.st.Room, s.st.Data, s.at(), func(b []byte, off int64) error {
			return diskio.WriteChunk(ctx, s.d, s.f, b, off)
		})
		if err != nil {
			s.m.dropSiteState(s.j) // start again: what arrived cannot be joined
			return 0, linkdl.Retry{Msg: "The pieces could not be joined (" + err.Error() + "); downloading again"}
		}
		s.m.Log.Info("link download joined", "job", s.j.st.ID, "file", msg)
	}
	if err := diskio.Flush(ctx, s.d, s.f); err != nil {
		return 0, err
	}
	fi, err := s.f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// vimeoPiece is one piece in download order: picture or sound, by start time.
type vimeoPiece struct {
	audio bool
	i     int
	start float64
}

// fetchVimeo downloads the chosen picture stream and the sound into part as
// one MP4 and returns its size.
func (m *Manager) fetchVimeo(ctx context.Context, j *Job, d *diskio.Disk, link, password, part string) (int64, error) {
	ls := j.st.Spec.Link
	c := linkdl.VimeoClient()
	v, err := linkdl.OpenVimeo(ctx, c, link, password)
	if err != nil {
		return 0, err
	}
	vs := v.Pick(ls.Quality, ls.Height)
	if vs == nil {
		return 0, linkdl.Gone{Msg: "Vimeo no longer offers the chosen quality; add the link again"}
	}
	as := v.Audio
	key := vs.SID()
	if as != nil {
		key += "+" + as.SID()
	}
	var vid, aid uint32
	s, err := m.openSite(ctx, j, d, part, key, "Vimeo", func() ([]byte, []byte, int, error) {
		vi, err := v.Init(ctx, vs)
		if err != nil {
			return nil, nil, 0, err
		}
		var ai []byte
		rate := 0.0
		if as != nil {
			if ai, err = v.Init(ctx, as); err != nil {
				return nil, nil, 0, err
			}
			rate = as.SampleRate
		}
		moov, _, _, err := mp4.MergeInit(vi, ai, v.Duration)
		if err != nil {
			return nil, nil, 0, linkdl.Gone{Msg: "Vimeo sent stream headers that cannot be joined (" + err.Error() + ")"}
		}
		room := mp4.Room(v.Duration, vs.Framerate, rate)
		head := append(append(append([]byte(nil), mp4.FTYP...), mp4.Reserve(room)...), mp4.MdatHead(0)...)
		return head, moov, room, nil
	})
	if err != nil {
		return 0, err
	}
	defer s.end()
	if vid, aid, err = trackIDs(s.init, as != nil); err != nil {
		m.dropSiteState(j)
		return 0, linkdl.Retry{Msg: "The kept stream headers are damaged; starting again"}
	}
	order := vimeoOrder(vs, as)
	j.setLive(func(l *live) { l.stage, l.size, l.done = "Downloading", max(ls.Est, 1), s.at() })
	for s.st.Next < len(order) {
		p := order[s.st.Next]
		open := func(try int) (io.ReadCloser, error) {
			if try == 3 { // the addresses may have expired: ask Vimeo again
				if v2, err := linkdl.OpenVimeo(ctx, c, link, password); err == nil {
					if nv := v2.Pick(vs.SID(), 0); nv != nil && len(nv.Segments) == len(vs.Segments) &&
						(as == nil || v2.Audio != nil && v2.Audio.SID() == as.SID() && len(v2.Audio.Segments) == len(as.Segments)) {
						v, vs = v2, nv
						if as != nil {
							as = v2.Audio
						}
					}
				}
			}
			st := vs
			if p.audio {
				st = as
			}
			return v.Piece(ctx, st, p.i, try%2 == 1)
		}
		tr := vid
		if p.audio {
			tr = aid
		}
		if err := s.piece(ctx, open, true, tr); err != nil {
			return 0, err
		}
	}
	return s.finish(ctx)
}

// trackIDs reads the picture and sound track ids from a joined header.
func trackIDs(moov []byte, sound bool) (vid, aid uint32, err error) {
	var ids []uint32
	for _, b := range mp4.Boxes(moov) {
		if b.Type != "moov" {
			continue
		}
		for _, k := range mp4.Boxes(b.B[b.H:]) {
			if k.Type == "mvex" {
				for _, t := range mp4.Boxes(k.B[k.H:]) {
					if t.Type == "trex" && len(t.B) >= 16 {
						ids = append(ids, uint32(t.B[12])<<24|uint32(t.B[13])<<16|uint32(t.B[14])<<8|uint32(t.B[15]))
					}
				}
			}
		}
	}
	if len(ids) == 0 || sound && len(ids) < 2 {
		return 0, 0, errors.New("no tracks")
	}
	if sound {
		return ids[0], ids[1], nil
	}
	return ids[0], 0, nil
}

func vimeoOrder(vs, as *linkdl.VimeoStream) []vimeoPiece {
	var o []vimeoPiece
	for i, sg := range vs.Segments {
		o = append(o, vimeoPiece{false, i, sg.Start})
	}
	if as != nil {
		for i, sg := range as.Segments {
			o = append(o, vimeoPiece{true, i, sg.Start})
		}
	}
	sort.SliceStable(o, func(a, b int) bool {
		if o[a].start != o[b].start {
			return o[a].start < o[b].start
		}
		return !o[a].audio && o[b].audio
	})
	return o
}

// fetchSC downloads a SoundCloud track into part. The original upload comes
// back with its hash already read back; the streams get their tags and are
// read back by the caller.
func (m *Manager) fetchSC(ctx context.Context, j *Job, d *diskio.Disk, link, part string) (int64, string, error) {
	ls := j.st.Spec.Link
	sc, err := openSC(ctx, link)
	if err != nil {
		return 0, "", err
	}
	var q *linkdl.SCQuality
	qs := sc.Qualities()
	for i := range qs {
		if qs[i].ID == ls.Quality {
			q = &qs[i]
			break
		}
	}
	if q == nil {
		return 0, "", linkdl.Gone{Msg: "SoundCloud no longer offers the chosen quality; add the link again"}
	}
	u, err := sc.StreamURL(ctx, *q)
	if err != nil {
		return 0, "", err
	}
	if q.Proto == "original" { // the artist's own file, as it was uploaded
		return m.download(ctx, j, d, u, part, &LinkSpec{Name: ls.Name, Size: -1, Resumable: true, Host: "soundcloud.com"})
	}
	tags := linkdl.Tags{Title: sc.T.Title, Artist: sc.Artist(), Genre: sc.T.Genre, Date: sc.Date(), URL: sc.T.PermalinkURL, Desc: sc.T.Description}
	cover := func() []byte {
		if a := sc.Artwork(); a != "" {
			if b, err := sc.Get(ctx, a, 4<<20); err == nil && linkdl.ImageMime(b) != "" {
				return b
			}
		}
		return nil
	}
	segs := []string{u}
	init := ""
	if q.Proto == "hls" {
		if init, segs, err = scList(ctx, sc, u); err != nil {
			return 0, "", err
		}
	}
	mp4Join := q.Codec == "aac"
	if mp4Join && init == "" {
		return 0, "", linkdl.Gone{Msg: "SoundCloud's AAC stream has no header; choose MP3 instead"}
	}
	s, err := m.openSite(ctx, j, d, part, q.ID, "SoundCloud", func() ([]byte, []byte, int, error) {
		if !mp4Join {
			return linkdl.ID3(tags, cover()), nil, 0, nil
		}
		ib, err := sc.Get(ctx, init, 8<<20)
		if err != nil {
			return nil, nil, 0, err
		}
		if tagged, err := linkdl.TagInit(ib, tags, cover()); err == nil {
			ib = tagged
		}
		moov, _, _, err := mp4.MergeInit(ib, nil, sc.T.Duration/1000)
		if err != nil {
			return nil, nil, 0, linkdl.Gone{Msg: "SoundCloud sent a stream header that cannot be used (" + err.Error() + ")"}
		}
		room := mp4.Room(sc.T.Duration/1000, 1, 48000)
		return append(append(append([]byte(nil), mp4.FTYP...), mp4.Reserve(room)...), mp4.MdatHead(0)...), moov, room, nil
	})
	if err != nil {
		return 0, "", err
	}
	defer s.end()
	var tr uint32
	if mp4Join {
		if tr, _, err = trackIDs(s.init, false); err != nil {
			m.dropSiteState(j)
			return 0, "", linkdl.Retry{Msg: "The kept stream header is damaged; starting again"}
		}
	}
	if s.st.Next > len(segs) {
		m.dropSiteState(j)
		return 0, "", linkdl.Retry{Msg: "SoundCloud now sends this track differently; starting again"}
	}
	j.setLive(func(l *live) { l.stage, l.size, l.done = "Downloading", max(ls.Est, 1), s.at() })
	for s.st.Next < len(segs) {
		i := s.st.Next
		open := func(try int) (io.ReadCloser, error) {
			if try == 3 { // the stream's addresses may have expired: ask again
				if u2, err := sc.StreamURL(ctx, *q); err == nil {
					if q.Proto != "hls" {
						segs = []string{u2}
					} else if _, s2, err := scList(ctx, sc, u2); err == nil && len(s2) == len(segs) {
						segs = s2
					}
				}
			}
			return linkdl.Open(ctx, linkClient, segs[i], 0)
		}
		if err := s.piece(ctx, open, mp4Join, tr); err != nil {
			return 0, "", err
		}
	}
	n, err := s.finish(ctx)
	return n, "", err
}

func scList(ctx context.Context, sc *linkdl.SC, u string) (string, []string, error) {
	b, err := sc.Get(ctx, u, 8<<20)
	if err != nil {
		return "", nil, err
	}
	init, segs := linkdl.M3U8(string(b), u)
	if len(segs) == 0 {
		return "", nil, linkdl.Retry{Msg: "SoundCloud sent an empty stream list"}
	}
	return init, segs, nil
}
