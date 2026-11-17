package linkdl

import (
	"net/http"
	"testing"
)

func TestParseNamesFolders(t *testing.T) {
	for raw, want := range map[string]string{
		"https://assets.frame.io/x/A001.mov?sig=1#rq-path=Day%201": "",
		"http://example.com/a":             "bad",
		"https://app.frame.io/reviews/abc": "share",
		"https://f.io/abcd":                "share",
		"https://192.168.1.4/file":         "local",
		"https://vimeo.com/123":            "vimeo",
		"https://soundcloud.com/a/b":       "soundcloud",
		"https://example.com/a b":          "bad",
	} {
		if _, why := Parse(raw); why != want {
			t.Errorf("%s: %q, want %q", raw, why, want)
		}
	}
	if l, _ := Parse("https://x.com/f.mov?s=1#rq-path=A"); l != "https://x.com/f.mov?s=1" {
		t.Errorf("fragment kept: %s", l)
	}
	if s := SubFolder("https://x.com/f#rq-path=Day%201/../Cam:A&rq-share=Shoot%201"); s != "Day 1/Cam A" {
		t.Errorf("sub folder %q", s)
	}
	if s := ShareName("https://x.com/f#rq-share=Shoot%201"); s != "Shoot 1" {
		t.Errorf("share %q", s)
	}
	for in, want := range map[string]string{"a/b:c*.mov": "a b c.mov", "  .x. ": "x", "": "download", "clip .mov": "clip.mov"} {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
	h := http.Header{}
	h.Set("Content-Disposition", `attachment; filename*=UTF-8''C%C3%A1mara%201.mov`)
	if n := NameFrom(h, "https://x.com/y.bin"); n != "Cámara 1.mov" {
		t.Errorf("filename* %q", n)
	}
	if n := NameFrom(http.Header{}, "https://s3.x.com/obj?response-content-disposition=attachment%3B%20filename%3D%22B.mxf%22"); n != "B.mxf" {
		t.Errorf("response-content-disposition %q", n)
	}
	if n := NameFrom(http.Header{}, "https://x.com/path/C%20D.wav"); n != "C D.wav" {
		t.Errorf("path name %q", n)
	}
}
