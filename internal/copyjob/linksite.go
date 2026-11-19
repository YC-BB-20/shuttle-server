package copyjob

import (
	"bufio"
	"context"
	"encoding/binary"
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
	"github.com/YC-BB-20/shuttle-server/internal/ytdlp"
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

// siteOf says which site a pasted link is for: "vimeo", "soundcloud",
// "youtube" or "".
func siteOf(raw string) string {
	if ytdlp.IsYouTube(raw) {
		return "youtube" // v0.2.123; a playlist or channel is refused at the look-up
	}
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
	case "youtube":
		id := ytdlp.VideoID(raw)
		if id == "" {
			return siteInfo{}, linkdl.Gone{Msg: "This is a YouTube playlist or channel. Paste the link of one video."}
		}
		if in, ok := m.ytCached(id); ok { // v0.2.124: Add reuses the look-up the pop-up just made
			in.Info.Sub = linkdl.SubFolder(raw)
			return in, nil
		}
		v, err := ytdlp.Look(ctx, m.ytTools(), id)
		if err != nil {
			return siteInfo{}, ytErr(err)
		}
		var qs []linkdl.Quality
		for _, c := range v.Choices() {
			qs = append(qs, linkdl.Quality{ID: c.ID, Label: c.Label, Bytes: c.Bytes, Height: c.Height, Ext: c.Ext})
		}
		if len(qs) == 0 {
			return siteInfo{}, linkdl.Gone{Msg: "YouTube offers no MP4 picture and M4A sound for this video"}
		}
		in := siteInfo{Info: linkdl.Info{Name: ytFileName(v.Title, id, "mp4"), Size: qs[0].Bytes, Type: "video/mp4",
			Resumable: true, Host: "youtube.com", Sub: linkdl.SubFolder(raw)}, Title: v.Title, Duration: v.Duration, Qualities: qs}
		m.ytKeep(id, in)
		return in, nil
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
	if site == "youtube" {
		ls.Duration = in.Duration
		ls.Name = ytFileName(in.Title, ytdlp.VideoID(raw), q.Ext)
	}
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

// YouTube (v0.2.123): yt-dlp downloads the chosen picture stream and the
// best M4A sound beside the file, as <name>.shuttle-dl.shuttle-yt-video and
// …-audio (it continues them after a pause or a restart); Shuttle then joins
// them into one ordinary MP4 as two pieces of a site download, so a join that
// was cut off continues too. Sound only is the M4A, made into an ordinary file
// the same way. The two stream files are removed once the file is joined.

func (m *Manager) ytTools() ytdlp.Tools { return ytdlp.Find(m.Tools) }

// ytErr turns a yt-dlp problem that trying again cannot fix into Gone.
func ytErr(err error) error {
	var g ytdlp.Gone
	if errors.As(err, &g) {
		return linkdl.Gone{Msg: string(g)}
	}
	return err
}

// ytFileName is the video's title as a file name.
func ytFileName(title, id, ext string) string {
	n := linkdl.VimeoFileName(title, id) // the same cleaning: "Title.mp4"
	n = strings.TrimSuffix(n, ".mp4")
	if strings.TrimSpace(title) == "" {
		n = "YouTube " + id
	}
	if ext == "" {
		ext = "mp4"
	}
	return n + "." + ext
}

// ytTemps are the stream files of a YouTube download (ours to remove).
func ytTemps(part string) []string {
	var out []string
	for _, k := range []string{"video", "audio"} {
		p := part + ".shuttle-yt-" + k
		out = append(out, p, p+".part", p+".ytdl")
	}
	return out
}

// readInit reads a stream file's header: its boxes up to and including the moov.
func readInit(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []byte
	hdr := make([]byte, 16)
	for len(out) < 64<<20 {
		if _, err := io.ReadFull(f, hdr[:8]); err != nil {
			return nil, errors.New("the stream has no header")
		}
		size, h := int64(binary.BigEndian.Uint32(hdr)), 8
		typ := string(hdr[4:8])
		if size == 1 {
			if _, err := io.ReadFull(f, hdr[8:16]); err != nil {
				return nil, errors.New("the stream has no header")
			}
			size, h = int64(binary.BigEndian.Uint64(hdr[8:])), 16
		}
		if size < int64(h) || size > 64<<20 && typ != "mdat" {
			return nil, errors.New("the stream's header is damaged")
		}
		if typ != "ftyp" && typ != "moov" {
			if _, err := f.Seek(size-int64(h), io.SeekCurrent); err != nil {
				return nil, err
			}
			continue
		}
		b := make([]byte, size)
		copy(b, hdr[:h])
		if _, err := io.ReadFull(f, b[h:]); err != nil {
			return nil, errors.New("the stream's header is cut short")
		}
		out = append(out, b...)
		if typ == "moov" {
			return out, nil
		}
	}
	return nil, errors.New("the stream has no moov")
}

// fetchYouTube downloads a YouTube video into part as one MP4 (or M4A) and
// returns its size.
func (m *Manager) fetchYouTube(ctx context.Context, j *Job, d *diskio.Disk, link, part string) (int64, error) {
	ls := j.st.Spec.Link
	t := m.ytTools()
	if miss := t.Missing(); miss != "" {
		return 0, linkdl.Gone{Msg: miss}
	}
	id := ytdlp.VideoID(link)
	if id == "" {
		return 0, linkdl.Gone{Msg: "This is not the link of one YouTube video"}
	}
	vfid, afid, both := strings.Cut(ls.Quality, "+")
	if !both {
		vfid, afid = "", vfid
	}
	if !diskio.VolumeMounted(j.st.Spec.Dest) {
		return 0, diskio.ErrGone
	}
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return 0, err
	}
	vfile, afile := part+".shuttle-yt-video", part+".shuttle-yt-audio"
	est := max(ls.Est, 1)
	var got int64
	grab := func(fid, out, stage string) error {
		if fi, err := os.Stat(out); err == nil { // yt-dlp finished it earlier
			got += fi.Size()
			return nil
		}
		base := got
		j.setLive(func(l *live) { l.stage, l.done, l.size = stage, base, est })
		var last int64
		err := ytdlp.Download(ctx, t, id, fid, out, func(done, total int64) {
			if done > last {
				j.dstBytes.Add(done - last)
				last = done
			}
			j.setLive(func(l *live) { l.done = base + done })
		})
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			if !diskio.Present(d) {
				return diskio.ErrGone
			}
			return ytErr(err)
		}
		if fi, err := os.Stat(out); err == nil {
			got += fi.Size()
		}
		return nil
	}
	pieces, first := []string{afile}, afile
	if vfid != "" {
		if err := grab(vfid, vfile, "Downloading the picture from YouTube"); err != nil {
			return 0, err
		}
		pieces, first = []string{vfile, afile}, vfile
	}
	if err := grab(afid, afile, "Downloading the sound from YouTube"); err != nil {
		return 0, err
	}
	vinit, err := readInit(first)
	if err != nil {
		os.Remove(first) // our own: downloaded again
		return 0, linkdl.Retry{Msg: "YouTube's stream could not be read (" + err.Error() + "); downloading it again"}
	}
	var ainit []byte
	if vfid != "" {
		if ainit, err = readInit(afile); err != nil {
			os.Remove(afile)
			return 0, linkdl.Retry{Msg: "YouTube's sound could not be read (" + err.Error() + "); downloading it again"}
		}
	}
	moov, vid, aid, err := mp4.MergeInit(vinit, ainit, ls.Duration)
	if err != nil {
		return 0, linkdl.Gone{Msg: "YouTube's streams cannot be joined (" + err.Error() + ")"}
	}
	tracks := []uint32{vid, aid}
	j.setLive(func(l *live) { l.stage, l.done, l.size = "Joining the picture and the sound", 0, got })
	s, err := m.openSite(ctx, j, d, part, "youtube "+ls.Quality, "YouTube", func() ([]byte, []byte, int, error) {
		room := mp4.Room(ls.Duration, 60, 48000)
		head := append(append(append([]byte(nil), mp4.FTYP...), mp4.Reserve(room)...), mp4.MdatHead(0)...)
		return head, moov, room, nil
	})
	if err != nil {
		return 0, err
	}
	defer s.end()
	for s.st.Next < len(pieces) {
		p := pieces[s.st.Next]
		if err := s.piece(ctx, func(int) (io.ReadCloser, error) { return os.Open(p) }, true, tracks[s.st.Next]); err != nil {
			return 0, err
		}
	}
	n, err := s.finish(ctx)
	if err == nil {
		for _, p := range ytTemps(part) {
			os.Remove(p) // our own stream files, now joined
		}
	}
	return n, err
}

// A YouTube look-up runs yt-dlp, which can take most of a minute, so the
// pop-up's look-up is kept for a while and Add reuses it (v0.2.124). At most
// ytCacheMax are kept; nothing in them is a secret (no download address).
const (
	ytCacheFor = 15 * time.Minute
	ytCacheMax = 50
)

type ytLooked struct {
	at time.Time
	in siteInfo
}

func (m *Manager) ytCached(id string) (siteInfo, bool) {
	m.ytMu.Lock()
	defer m.ytMu.Unlock()
	c, ok := m.ytSeen[id]
	if !ok || time.Since(c.at) > ytCacheFor {
		return siteInfo{}, false
	}
	return c.in, true
}

func (m *Manager) ytKeep(id string, in siteInfo) {
	m.ytMu.Lock()
	defer m.ytMu.Unlock()
	if m.ytSeen == nil {
		m.ytSeen = map[string]ytLooked{}
	}
	for k, c := range m.ytSeen {
		if time.Since(c.at) > ytCacheFor || len(m.ytSeen) >= ytCacheMax {
			delete(m.ytSeen, k)
		}
	}
	m.ytSeen[id] = ytLooked{time.Now(), in}
}
