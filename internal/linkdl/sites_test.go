package linkdl

import (
	"bytes"
	"testing"
)

// v0.2.91: Vimeo and SoundCloud links are recognised the way Shuttle Router
// recognises them; tags are written in the two formats.
func TestSiteLinks(t *testing.T) {
	for raw, want := range map[string][2]string{
		"https://vimeo.com/76543210":                         {"76543210", ""},
		"https://vimeo.com/76543210/abcdef1234":              {"76543210", "abcdef1234"},
		"https://player.vimeo.com/video/76543210?h=00ff00ff": {"76543210", "00ff00ff"},
		"https://vimeo.com/channels/staffpicks/76543210":     {"76543210", ""},
		"https://vimeo.com/about":                            {"", ""},
		"https://example.com/76543210":                       {"", ""},
	} {
		if id, h := VimeoID(raw); id != want[0] || h != want[1] {
			t.Errorf("VimeoID(%s) = %s %s", raw, id, h)
		}
	}
	for raw, want := range map[string][2]string{
		"https://soundcloud.com/artist/track-name":        {SCWeb + "/artist/track-name", ""},
		"https://m.soundcloud.com/artist/track-name?si=1": {SCWeb + "/artist/track-name", ""},
		"https://soundcloud.com/artist/sets/album":        {"", "set"},
		"https://soundcloud.com/artist":                   {"", "bad"},
		"https://soundcloud.com/discover/sets/x":          {"", "bad"},
		"https://on.soundcloud.com/AbC123":                {"https://on.soundcloud.com/AbC123", ""},
	} {
		if l, why := SCLink(raw); l != want[0] || why != want[1] {
			t.Errorf("SCLink(%s) = %q %q", raw, l, why)
		}
	}
	if n := SCFileName("A/B", "Song: one", "M4A"); n != CleanName("A/B")+" - "+CleanName("Song: one")+".m4a" {
		t.Errorf("SCFileName %q", n)
	}
}

func TestTags(t *testing.T) {
	tg := Tags{Title: "Night Drive", Artist: "Some One", Date: "2026", URL: "https://soundcloud.com/x/y"}
	id := ID3(tg, []byte("\xff\xd8\xff\xe0jpegdata"))
	if !bytes.HasPrefix(id, []byte("ID3\x04")) || !bytes.Contains(id, []byte("TIT2")) || !bytes.Contains(id, []byte("APIC")) || !bytes.Contains(id, []byte("Night Drive")) {
		t.Fatalf("ID3 tag %q", id)
	}
	if ImageMime([]byte("\xff\xd8\xff\xe0")) != "image/jpeg" || ImageMime([]byte("GIF8")) != "" {
		t.Fatal("ImageMime")
	}
	init := append(mbox("ftyp", []byte("iso6")), mbox("moov", mbox("mvhd", make([]byte, 100)), mbox("udta", []byte("old")))...)
	out, err := TagInit(init, tg, nil)
	if err != nil || bytes.Contains(out, []byte("old")) || !bytes.Contains(out, []byte("ilst")) || !bytes.Contains(out, []byte("Some One")) {
		t.Fatalf("TagInit: %v", err)
	}
	if _, err := TagInit(mbox("ftyp", nil), tg, nil); err == nil {
		t.Fatal("a header without moov was accepted")
	}
}
