package mp4

import (
	"errors"
	"fmt"
	"io"
	"math"
)

// The sample tables, built from every piece's moof at the end.

type runs struct { // run-length list of (count, value), kept packed
	b      []byte
	n      int
	cc, cv uint32
	has    bool
}

func (r *runs) add(v uint32) {
	if r.has && r.cv == v {
		r.cc++
		return
	}
	if r.has {
		r.b = append(append(r.b, p32(r.cc)...), p32(r.cv)...)
		r.n++
	}
	r.cv, r.cc, r.has = v, 1, true
}

func (r *runs) done() (int, []byte) {
	if !r.has {
		return r.n, r.b
	}
	return r.n + 1, append(append(append([]byte(nil), r.b...), p32(r.cc)...), p32(r.cv)...)
}

type track struct {
	id, ts                 uint32
	mt                     uint64 // media time of the first edit
	ddur, dsize, dflags    uint32 // trex defaults
	n                      uint32
	dur                    uint64
	sizes, sync            []byte
	nsync                  uint32
	stts, ctts             runs
	ctoAny, neg, notAllSyn bool
	chunks                 [][2]uint64 // position in the mdat payload, samples
}

type tables struct {
	t     map[uint32]*track
	order []uint32
	moov  Box
}

func newTables(moov []byte) (*tables, error) {
	top := Boxes(moov)
	if len(top) == 0 || top[0].Type != "moov" {
		return nil, errors.New("the saved header is not a moov")
	}
	tb := &tables{t: map[uint32]*track{}, moov: top[0]}
	for _, k := range kids(tb.moov) {
		switch k.Type {
		case "trak":
			tk := kids(k)
			th := find(tk, "tkhd")
			md := find(tk, "mdia")
			if th == nil || md == nil {
				continue
			}
			mdhd := find(kids(*md), "mdhd")
			if mdhd == nil {
				continue
			}
			t := &track{id: u32(th.B, idOff(th.B)), ts: u32(mdhd.B, idOff(mdhd.B))}
			if ed := find(tk, "edts"); ed != nil {
				if el := find(kids(*ed), "elst"); el != nil && u32(el.B, 12) >= 1 {
					if ver1(el.B) {
						if m := u64(el.B, 24); m < 9.2e18 {
							t.mt = m
						}
					} else if m := s32(el.B, 20); m > 0 {
						t.mt = uint64(m)
					}
				}
			}
			tb.t[t.id] = t
			tb.order = append(tb.order, t.id)
		case "mvex":
			for _, x := range kids(k) {
				if t := tb.t[u32(x.B, 12)]; x.Type == "trex" && t != nil {
					t.ddur, t.dsize, t.dflags = u32(x.B, 20), u32(x.B, 24), u32(x.B, 28)
				}
			}
		}
	}
	return tb, nil
}

func has(fl, bit uint32) bool { return fl&bit != 0 }

// add takes one moof: its samples, whose data is at outRel + (data offset - delta).
func (tb *tables) add(moof []byte, outRel uint64, delta uint32, trackID uint32) {
	mb := Box{Type: "moof", B: moof, H: 8}
	for _, t := range kids(mb) {
		if t.Type != "traf" {
			continue
		}
		var tr *track
		var dd, ds, df *uint32
		var nxt *uint64
		for _, x := range kids(t) {
			s := x.B
			switch x.Type {
			case "tfhd":
				fl := u32(s, 8) & 0xffffff
				id := trackID
				if id == 0 {
					id = u32(s, 12)
				}
				tr = tb.t[id]
				p := 16
				if has(fl, 1) {
					p += 8
				}
				if has(fl, 2) {
					p += 4
				}
				if has(fl, 8) {
					v := u32(s, p)
					dd, p = &v, p+4
				}
				if has(fl, 16) {
					v := u32(s, p)
					ds, p = &v, p+4
				}
				if has(fl, 32) {
					v := u32(s, p)
					df = &v
				}
			case "trun":
				if tr == nil {
					continue
				}
				v1 := s[8] == 1
				fl, c, p := u32(s, 8)&0xffffff, u32(s, 12), 16
				var off *int64
				var ffl *uint32
				if has(fl, 1) {
					o := s32(s, p)
					off, p = &o, p+4
				}
				if has(fl, 4) {
					f := u32(s, p)
					ffl, p = &f, p+4
				}
				pos := outRel
				if off != nil {
					pos = uint64(int64(outRel) + *off - int64(delta))
				} else if nxt != nil {
					pos = *nxt
				}
				var total uint64
				for i := uint32(0); i < c && p <= len(s); i++ {
					d, z, sf, cto := tr.ddur, tr.dsize, tr.dflags, int64(0)
					if dd != nil {
						d = *dd
					}
					if ds != nil {
						z = *ds
					}
					if df != nil {
						sf = *df
					}
					if has(fl, 0x100) {
						d, p = u32(s, p), p+4
					}
					if has(fl, 0x200) {
						z, p = u32(s, p), p+4
					}
					if has(fl, 0x400) {
						sf, p = u32(s, p), p+4
					}
					if i == 0 && ffl != nil {
						sf = *ffl
					}
					if has(fl, 0x800) {
						if v1 {
							cto = s32(s, p)
						} else {
							cto = int64(u32(s, p))
						}
						p += 4
					}
					tr.n++
					tr.sizes = append(tr.sizes, p32(z)...)
					total += uint64(z)
					tr.stts.add(d)
					tr.dur += uint64(d)
					tr.ctts.add(uint32(cto))
					if cto != 0 {
						tr.ctoAny = true
					}
					if cto < 0 {
						tr.neg = true
					}
					if sf&0x10000 != 0 {
						tr.notAllSyn = true
					} else {
						tr.sync = append(tr.sync, p32(tr.n)...)
						tr.nsync++
					}
				}
				tr.chunks = append(tr.chunks, [2]uint64{pos, uint64(c)})
				e := pos + total
				nxt = &e
			}
		}
	}
}

func replaceKids(b Box, fn func(Box) []byte) []byte {
	var out [][]byte
	for _, k := range kids(b) {
		if r := fn(k); r != nil {
			out = append(out, r)
		} else {
			out = append(out, k.B)
		}
	}
	return box(b.Type, out...)
}

func durField(b []byte, v1o, v0o int, val uint64) []byte {
	if ver1(b) {
		return patch(b, v1o, p64(val))
	}
	return patch(b, v0o, p32(uint32(min(val, math.MaxUint32))))
}

// moov is the finished table of contents; dataStart is the file offset of
// the first sample byte.
func (tb *tables) moovBox(dataStart uint64) []byte {
	mvhd := find(kids(tb.moov), "mvhd")
	mts := u32(mvhd.B, idOff(mvhd.B))
	var movieDur uint64
	var maxID uint32
	stblFor := func(tr *track, old Box) []byte {
		var o [][]byte
		for _, k := range kids(old) {
			if k.Type == "stsd" {
				o = append(o, k.B)
			}
		}
		n1, b1 := tr.stts.done()
		o = append(o, full("stts", 0, p32(uint32(n1)), b1))
		if tr.ctoAny {
			n2, b2 := tr.ctts.done()
			v := byte(0)
			if tr.neg {
				v = 1
			}
			o = append(o, full("ctts", v, p32(uint32(n2)), b2))
		}
		if tr.notAllSyn {
			o = append(o, full("stss", 0, p32(tr.nsync), tr.sync))
		}
		var e []byte
		var last uint64 = math.MaxUint64
		cnt := 0
		for i, ch := range tr.chunks {
			if ch[1] != last {
				e = append(e, p32(uint32(i+1))...)
				e = append(e, p32(uint32(ch[1]))...)
				e = append(e, p32(1)...)
				last = ch[1]
				cnt++
			}
		}
		o = append(o, full("stsc", 0, p32(uint32(cnt)), e))
		o = append(o, full("stsz", 0, p32(0), p32(tr.n), tr.sizes))
		e = nil
		for _, ch := range tr.chunks {
			e = append(e, p64(dataStart+ch[0])...)
		}
		o = append(o, full("co64", 0, p32(uint32(len(tr.chunks))), e))
		return box("stbl", o...)
	}
	var traks [][]byte
	for _, k := range kids(tb.moov) {
		if k.Type != "trak" {
			continue
		}
		th := find(kids(k), "tkhd")
		if th == nil {
			continue
		}
		tr := tb.t[u32(th.B, idOff(th.B))]
		if tr == nil || tr.n == 0 {
			continue
		}
		edit := uint64(math.Floor(float64(tr.dur-min(tr.mt, tr.dur))*float64(mts)/float64(tr.ts) + 0.5))
		movieDur = max(movieDur, edit)
		maxID = max(maxID, tr.id)
		traks = append(traks, replaceKids(k, func(c Box) []byte {
			switch c.Type {
			case "tkhd":
				return durField(c.B, 36, 28, edit)
			case "edts":
				return replaceKids(c, func(x Box) []byte {
					if x.Type == "elst" && u32(x.B, 12) >= 1 {
						return durField(x.B, 16, 16, edit)
					}
					return nil
				})
			case "mdia":
				return replaceKids(c, func(m Box) []byte {
					switch m.Type {
					case "mdhd":
						return durField(m.B, 32, 24, tr.dur)
					case "minf":
						return replaceKids(m, func(n Box) []byte {
							if n.Type == "stbl" {
								return stblFor(tr, n)
							}
							return nil
						})
					}
					return nil
				})
			}
			return nil
		}))
	}
	mv := durField(mvhd.B, 32, 24, movieDur)
	out := [][]byte{append(append([]byte(nil), mv[:len(mv)-4]...), p32(maxID+1)...)}
	for _, k := range kids(tb.moov) {
		if k.Type != "mvhd" && k.Type != "trak" && k.Type != "mvex" {
			out = append(out, k.B)
		}
	}
	return box("moov", append(out, traks...)...)
}

// Finish makes the joined file an ordinary MP4. The file was written as
// FTYP | free box of room bytes | mdat header | dataLen bytes of samples.
// records holds every piece's moof records; writeAt writes into the file,
// whose length is fileLen. The moov goes into the room, or at the end when it
// does not fit; then the mdat header gets its size.
func Finish(initMoov []byte, records io.Reader, room int, dataLen uint64, fileLen int64, writeAt func([]byte, int64) error) (string, error) {
	tb, err := newTables(initMoov)
	if err != nil {
		return "", err
	}
	h := make([]byte, 20)
	for {
		if _, err := io.ReadFull(records, h); err != nil {
			if err == io.EOF {
				break
			}
			return "", errors.New("the list of pieces is damaged")
		}
		m := make([]byte, u32(h, 16))
		if _, err := io.ReadFull(records, m); err != nil {
			return "", errors.New("the list of pieces is damaged")
		}
		tb.add(m, u64(h, 4), u32(h, 12), u32(h, 0))
	}
	dataStart := uint64(len(FTYP) + room + MdatHeader)
	moov := tb.moovBox(dataStart)
	where := "front"
	if len(moov) == room || len(moov) <= room-8 {
		b := moov
		if len(moov) < room {
			b = append(append([]byte(nil), moov...), Reserve(room-len(moov))...)
		}
		if err := writeAt(b, int64(len(FTYP))); err != nil {
			return "", err
		}
	} else {
		if err := writeAt(moov, fileLen); err != nil {
			return "", err
		}
		where = "end"
	}
	if err := writeAt(MdatHead(dataLen), int64(len(FTYP)+room)); err != nil {
		return "", err
	}
	var n uint32
	for _, id := range tb.order {
		n += tb.t[id].n
	}
	if n == 0 {
		return "", errors.New("no picture or sound arrived")
	}
	return fmt.Sprintf("an ordinary MP4, table of contents at the %s (%d samples)", where, n), nil
}
