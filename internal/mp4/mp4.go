// Package mp4 joins the picture and sound pieces of a streamed video (DASH or
// HLS fragments) into one ordinary MP4, without decoding anything (v0.2.91).
// The sample data is written as it arrives into one mdat; each piece's moof
// is kept as a record in a side file; at the end the records become ordinary
// sample tables in one moov, placed in room reserved at the front of the
// file, so players read one table of contents and can jump anywhere. The
// method follows Shuttle Router's Vimeo joiner, written again here.
package mp4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// Box is one box: its whole bytes and the size of its header.
type Box struct {
	Type string
	B    []byte
	H    int
}

func u32(b []byte, o int) uint32 { return binary.BigEndian.Uint32(b[o:]) }
func u64(b []byte, o int) uint64 { return binary.BigEndian.Uint64(b[o:]) }
func s32(b []byte, o int) int64  { return int64(int32(u32(b, o))) }
func p32(v uint32) []byte        { var b [4]byte; binary.BigEndian.PutUint32(b[:], v); return b[:] }
func p64(v uint64) []byte        { var b [8]byte; binary.BigEndian.PutUint64(b[:], v); return b[:] }

func patch(b []byte, o int, v []byte) []byte {
	c := append([]byte(nil), b...)
	copy(c[o:], v)
	return c
}

// box makes a box of type typ around the payload parts.
func box(typ string, parts ...[]byte) []byte {
	n := 8
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	out = append(out, p32(uint32(n))...)
	out = append(out, typ...)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func full(typ string, v byte, payload ...[]byte) []byte {
	return box(typ, append([][]byte{{v, 0, 0, 0}}, payload...)...)
}

// Boxes splits b into its top-level boxes; a broken tail is dropped.
func Boxes(b []byte) []Box {
	var out []Box
	for i := 0; i+8 <= len(b); {
		size, h := uint64(u32(b, i)), 8
		if size == 1 {
			if i+16 > len(b) {
				break
			}
			size, h = u64(b, i+8), 16
		} else if size == 0 {
			size = uint64(len(b) - i)
		}
		if size < uint64(h) || uint64(i)+size > uint64(len(b)) {
			break
		}
		out = append(out, Box{Type: string(b[i+4 : i+8]), B: b[i : i+int(size)], H: h})
		i += int(size)
	}
	return out
}

func kids(x Box) []Box { return Boxes(x.B[x.H:]) }

func find(l []Box, t string) *Box {
	for i := range l {
		if l[i].Type == t {
			return &l[i]
		}
	}
	return nil
}

func ver1(b []byte) bool { return len(b) > 8 && b[8] == 1 }

// idOff is where a tkhd's track id (or an mvhd's or mdhd's timescale) is.
func idOff(b []byte) int {
	if ver1(b) {
		return 28
	}
	return 20
}

type initSeg struct {
	ftyp, mvhd, trak, mvex, trex *Box
	kids                         []Box
	timescale, trackID           uint32
}

func parseInit(data []byte) (*initSeg, error) {
	top := Boxes(data)
	moov := find(top, "moov")
	if moov == nil {
		return nil, errors.New("no moov in the stream header")
	}
	r := &initSeg{ftyp: find(top, "ftyp"), kids: kids(*moov)}
	r.mvhd, r.trak, r.mvex = find(r.kids, "mvhd"), find(r.kids, "trak"), find(r.kids, "mvex")
	if r.mvhd == nil || r.trak == nil {
		return nil, errors.New("stream header without mvhd or trak")
	}
	r.timescale = u32(r.mvhd.B, idOff(r.mvhd.B))
	tkhd := find(kids(*r.trak), "tkhd")
	if tkhd == nil {
		return nil, errors.New("stream header without tkhd")
	}
	r.trackID = u32(tkhd.B, idOff(tkhd.B))
	if r.mvex != nil {
		r.trex = find(kids(*r.mvex), "trex")
	}
	return r, nil
}

func scaled(v uint64, scale float64) uint64 { return uint64(math.Floor(float64(v)*scale + 0.5)) }

// trakPatch gives a sound track a new id and moves its movie-time fields to
// the picture's timescale.
func trakPatch(trak Box, id uint32, scale float64) []byte {
	var parts [][]byte
	for _, k := range kids(trak) {
		b := k.B
		switch {
		case k.Type == "tkhd":
			b = patch(b, idOff(b), p32(id))
			if scale != 1 {
				if ver1(b) {
					if d := u64(b, 36); d != math.MaxUint64 {
						b = patch(b, 36, p64(scaled(d, scale)))
					}
				} else if d := u32(b, 28); d != math.MaxUint32 {
					b = patch(b, 28, p32(uint32(scaled(uint64(d), scale))))
				}
			}
		case k.Type == "edts" && scale != 1:
			var e [][]byte
			for _, x := range kids(k) {
				xb := x.B
				if x.Type == "elst" {
					n, pos := int(u32(xb, 12)), 16
					for i := 0; i < n && pos+12 <= len(xb); i++ {
						if ver1(xb) {
							xb = patch(xb, pos, p64(scaled(u64(xb, pos), scale)))
							pos += 20
						} else {
							xb = patch(xb, pos, p32(uint32(scaled(uint64(u32(xb, pos)), scale))))
							pos += 12
						}
					}
				}
				e = append(e, xb)
			}
			b = box("edts", e...)
		}
		parts = append(parts, b)
	}
	return box("trak", parts...)
}

func trexFor(in *initSeg, id uint32) []byte {
	if in.trex != nil {
		return patch(in.trex.B, 12, p32(id))
	}
	return box("trex", []byte{0, 0, 0, 0}, p32(id), p32(1), p32(0), p32(0), p32(0))
}

// MergeInit makes one moov from a picture stream header and, optionally, a
// sound stream header. It returns the moov box and both track ids.
func MergeInit(video, audio []byte, duration float64) (moov []byte, vid, aid uint32, err error) {
	v, err := parseInit(video)
	if err != nil {
		return nil, 0, 0, err
	}
	var a *initSeg
	if audio != nil {
		if a, err = parseInit(audio); err != nil {
			return nil, 0, 0, err
		}
	}
	vid = v.trackID
	aid = vid + 1
	if vid == 1 {
		aid = 2
	}
	next := vid + 1
	if a != nil {
		next = max(vid, aid) + 1
	}
	mvhd := append(append([]byte(nil), v.mvhd.B[:len(v.mvhd.B)-4]...), p32(next)...)
	parts := [][]byte{mvhd}
	for _, k := range v.kids {
		if k.Type != "mvhd" && k.Type != "trak" && k.Type != "mvex" {
			parts = append(parts, k.B)
		}
	}
	parts = append(parts, v.trak.B)
	if a != nil {
		parts = append(parts, trakPatch(*a.trak, aid, float64(v.timescale)/float64(a.timescale)))
	}
	var mvex [][]byte
	if duration > 0 {
		mvex = append(mvex, box("mehd", []byte{1, 0, 0, 0}, p64(uint64(math.Floor(duration*float64(v.timescale)+0.5)))))
	}
	mvex = append(mvex, trexFor(v, vid))
	if a != nil {
		mvex = append(mvex, trexFor(a, aid))
	}
	parts = append(parts, box("mvex", mvex...))
	return box("moov", parts...), vid, aid, nil
}

// FTYP is the file type box of a joined file.
var FTYP = box("ftyp", []byte("isom"), p32(512), []byte("isomiso2avc1mp41"))

// MdatHeader is the size of the mdat header (64-bit size).
const MdatHeader = 16

// Reserve is a free box of n bytes (n ≥ 8).
func Reserve(n int) []byte { return box("free", make([]byte, n-8)) }

// MdatHead is the mdat header for dataLen bytes of samples.
func MdatHead(dataLen uint64) []byte {
	return append(append(p32(1), "mdat"...), p64(MdatHeader+dataLen)...)
}

// Room is generous space for the moov, from the length and stream rates;
// 8 when the length is unknown (the moov then goes to the end).
func Room(duration, fps, rate float64) int {
	if duration <= 0 {
		return 8
	}
	if fps <= 0 {
		fps = 60
	}
	if rate <= 0 {
		rate = 48000
	}
	vs := duration * fps * 1.05
	as := duration * rate / 1024 * 1.05
	chunks := duration
	return int((vs*16+as*12+chunks*24)*1.1) + 65536
}

// Record is one moof of a piece: its track, where its data starts in the
// output (from the start of the mdat payload), and the distance from the
// moof to its data in the piece.
func Record(track uint32, outRel uint64, delta uint32, moof []byte) []byte {
	return bytes.Join([][]byte{p32(track), p64(outRel), p32(delta), p32(uint32(len(moof))), moof}, nil)
}

// CopyPayload reads one media piece from r: the sample data goes to write
// (in order), the moof boxes come back as records. buf is the copy buffer.
func CopyPayload(r io.Reader, write func([]byte) error, outRel uint64, track uint32, buf []byte) (wrote, read uint64, moofs int, recs []byte, err error) {
	var pend []byte
	var pendAt uint64
	hdr := make([]byte, 16)
	short := errors.New("the piece was cut short")
	for {
		n, e := io.ReadFull(r, hdr[:8])
		if n == 0 && (e == io.EOF || e == io.ErrUnexpectedEOF) {
			break
		}
		if e != nil {
			return 0, 0, 0, nil, short
		}
		size, typ, h := uint64(u32(hdr, 0)), string(hdr[4:8]), uint64(8)
		if size == 1 {
			if _, e := io.ReadFull(r, hdr[8:16]); e != nil {
				return 0, 0, 0, nil, short
			}
			size, h = u64(hdr, 8), 16
		} else if size == 0 {
			return 0, 0, 0, nil, errors.New("an open-ended box in a piece")
		}
		if size < h {
			return 0, 0, 0, nil, errors.New("a broken box in a piece")
		}
		start := read
		read += size
		switch typ {
		case "moof":
			if size > 16<<20 {
				return 0, 0, 0, nil, errors.New("a moof too large")
			}
			m := make([]byte, size)
			copy(m, hdr[:h])
			if _, e := io.ReadFull(r, m[h:]); e != nil {
				return 0, 0, 0, nil, short
			}
			pend, pendAt = m, start
		case "mdat":
			if pend != nil {
				recs = append(recs, Record(track, outRel+wrote, uint32(start+h-pendAt), pend)...)
				moofs++
				pend = nil
			}
			for left := size - h; left > 0; {
				k := min(left, uint64(len(buf)))
				if _, e := io.ReadFull(r, buf[:k]); e != nil {
					return 0, 0, 0, nil, short
				}
				if e := write(buf[:k]); e != nil {
					return 0, 0, 0, nil, e
				}
				wrote += k
				left -= k
			}
		default: // styp, sidx, free, emsg, prft…: not needed in the joined file
			if _, e := io.CopyN(io.Discard, r, int64(size-h)); e != nil {
				return 0, 0, 0, nil, short
			}
		}
	}
	if pend != nil {
		return 0, 0, 0, nil, errors.New("a moof without its data")
	}
	return wrote, read, moofs, recs, nil
}
