package linkdl

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Tags written into a downloaded track (v0.2.91): title, artist, album,
// genre, date, source address and description, and the cover picture.
type Tags struct {
	Title, Artist, Album, Genre, Date, URL, Desc string
}

func (t Tags) comment() string {
	c := ""
	if t.URL != "" {
		c = "Source: " + t.URL
	}
	d := t.Desc
	if len(d) > 2000 {
		d = trimUTF8(d, 2000)
	}
	if t.URL != "" && d != "" {
		c += "\n\n"
	}
	return c + d
}

// ImageMime says what a cover picture is: "image/jpeg", "image/png" or "".
func ImageMime(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	}
	return ""
}

func syncsafe(n int) []byte {
	return []byte{byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
}

// ID3 is an ID3v2.4 tag to put in front of an MP3.
func ID3(t Tags, cover []byte) []byte {
	var body bytes.Buffer
	frame := func(id string, data []byte) {
		body.WriteString(id)
		body.Write(syncsafe(len(data)))
		body.Write([]byte{0, 0})
		body.Write(data)
	}
	text := func(id, s string) {
		if s != "" {
			frame(id, append([]byte{3}, s...))
		}
	}
	text("TIT2", t.Title)
	text("TPE1", t.Artist)
	text("TALB", t.Album)
	text("TCON", t.Genre)
	text("TDRC", t.Date)
	if t.URL != "" {
		frame("WOAS", []byte(t.URL))
	}
	if c := t.comment(); c != "" {
		frame("COMM", append([]byte("\x03eng\x00"), c...))
	}
	if m := ImageMime(cover); m != "" {
		frame("APIC", append(append([]byte{3}, m...), append([]byte{0, 3, 0}, cover...)...))
	}
	return append(append([]byte("ID3\x04\x00\x00"), syncsafe(body.Len())...), body.Bytes()...)
}

func mbox(typ string, parts ...[]byte) []byte {
	n := 8
	for _, p := range parts {
		n += len(p)
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(n))
	out = append(out, typ...)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// udta is the iTunes-style tag box of an M4A.
func udta(t Tags, cover []byte) []byte {
	data := func(flag byte, p []byte) []byte { return mbox("data", []byte{0, 0, 0, flag, 0, 0, 0, 0}, p) }
	var items [][]byte
	item := func(typ, s string) {
		if s != "" {
			items = append(items, mbox(typ, data(1, []byte(s))))
		}
	}
	item("\xa9nam", t.Title)
	item("\xa9ART", t.Artist)
	item("aART", t.Artist)
	item("\xa9alb", t.Album)
	item("\xa9gen", t.Genre)
	item("\xa9day", t.Date)
	item("\xa9cmt", t.comment())
	if t.URL != "" {
		items = append(items, mbox("----", mbox("mean", []byte("\x00\x00\x00\x00com.apple.iTunes")), mbox("name", []byte("\x00\x00\x00\x00SOURCE")), data(1, []byte(t.URL))))
	}
	if m := ImageMime(cover); m != "" {
		f := byte(13)
		if m == "image/png" {
			f = 14
		}
		items = append(items, mbox("covr", data(f, cover)))
	}
	hdlr := mbox("hdlr", []byte("\x00\x00\x00\x00\x00\x00\x00\x00mdirappl\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	return mbox("udta", mbox("meta", []byte{0, 0, 0, 0}, hdlr, mbox("ilst", items...)))
}

// TagInit puts the tags into an M4A stream header's moov (replacing any udta).
func TagInit(init []byte, t Tags, cover []byte) ([]byte, error) {
	var out []byte
	done := false
	for i := 0; i+8 <= len(init); {
		size := int(binary.BigEndian.Uint32(init[i:]))
		if size < 8 || i+size > len(init) {
			return nil, errors.New("an unexpected stream header")
		}
		if string(init[i+4:i+8]) == "moov" && !done {
			var kids [][]byte
			for j, e := i+8, i+size; j+8 <= e; {
				ks := int(binary.BigEndian.Uint32(init[j:]))
				if ks < 8 || j+ks > e {
					return nil, errors.New("an unexpected stream header")
				}
				if string(init[j+4:j+8]) != "udta" {
					kids = append(kids, init[j:j+ks])
				}
				j += ks
			}
			out = append(out, mbox("moov", append(kids, udta(t, cover))...)...)
			done = true
		} else {
			out = append(out, init[i:i+size]...)
		}
		i += size
	}
	if !done {
		return nil, errors.New("no moov in the stream header")
	}
	return out, nil
}
