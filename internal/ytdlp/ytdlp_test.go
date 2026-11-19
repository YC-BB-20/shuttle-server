package ytdlp

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoID(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":              "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=abc":                      "dQw4w9WgXcQ",
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ&list=PL123":     "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":               "dQw4w9WgXcQ",
		"https://www.youtube.com/playlist?list=PL123":              "",
		"https://www.youtube.com/@channel":                         "",
		"https://vimeo.com/123456":                                 "",
		"https://www.youtube.com/watch?v=short":                    "",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ%22%3E%3Cx%3E": "",
	} {
		if got := VideoID(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
	if !IsYouTube("https://www.youtube.com/playlist?list=PL1") || IsYouTube("https://vimeo.com/1") {
		t.Error("IsYouTube")
	}
}

func TestChoicesPutH264FirstAndAddSoundOnly(t *testing.T) {
	v := Video{Duration: 100, Formats: []Format{
		{ID: "18", Ext: "mp4", VCodec: "avc1.42001E", ACodec: "mp4a.40.2", Height: 360, Protocol: "https"}, // picture and sound together: not offered
		{ID: "137", Ext: "mp4", VCodec: "avc1.640028", ACodec: "none", Height: 1080, FPS: 30, Size: 50e6, Protocol: "https"},
		{ID: "299", Ext: "mp4", VCodec: "avc1.64002a", ACodec: "none", Height: 1080, FPS: 60, Size: 80e6, Protocol: "https"},
		{ID: "401", Ext: "mp4", VCodec: "av01.0.12M.08", ACodec: "none", Height: 2160, FPS: 30, Approx: 300e6, Protocol: "https"},
		{ID: "313", Ext: "webm", VCodec: "vp9", ACodec: "none", Height: 2160, Protocol: "https"},
		{ID: "96", Ext: "mp4", VCodec: "avc1", ACodec: "none", Height: 1080, Protocol: "m3u8_native"},
		{ID: "139", Ext: "m4a", VCodec: "none", ACodec: "mp4a.40.5", ABR: 48, Size: 1e6, Protocol: "https"},
		{ID: "140", Ext: "m4a", VCodec: "none", ACodec: "mp4a.40.2", ABR: 128, Size: 2e6, Protocol: "https"},
		{ID: "251", Ext: "webm", VCodec: "none", ACodec: "opus", ABR: 160, Protocol: "https"},
	}}
	c := v.Choices()
	var ids []string
	for _, x := range c {
		ids = append(ids, x.ID+"="+x.Label)
	}
	want := "299+140=1080p60 · H.264,401+140=2160p · AV1,140=Sound only (M4A)"
	if got := strings.Join(ids, ","); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if c[0].Bytes != 82e6 || c[2].Ext != "m4a" {
		t.Fatalf("%+v", c)
	}
}

// release serves a fake GitHub release of both tools.
func release(t *testing.T, yt, denoBin []byte, badSum bool) *httptest.Server {
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("deno")
	f.Write(denoBin)
	zw.Close()
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	var yb bytes.Buffer // the folder build: the program and its _internal folder
	yw := zip.NewWriter(&yb)
	h := &zip.FileHeader{Name: "yt-dlp_macos", Method: zip.Deflate}
	h.SetMode(0o755)
	pf, _ := yw.CreateHeader(h)
	pf.Write(yt)
	yw.Create("_internal/")
	lf, _ := yw.Create("_internal/lib.txt")
	lf.Write([]byte("library"))
	yw.Close()
	yzip := yb.Bytes()
	ytSum := sum(yzip)
	if badSum {
		ytSum = strings.Repeat("0", 64)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/yt/SHA2-256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  yt-dlp\n%s  yt-dlp_macos.zip\n", sum([]byte("x")), ytSum)
	})
	mux.HandleFunc("/yt/yt-dlp_macos.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(yzip) })
	mux.HandleFunc("/deno/deno-aarch64-apple-darwin.zip.sha256sum", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  deno-aarch64-apple-darwin.zip\n", sum(zb.Bytes()))
	})
	mux.HandleFunc("/deno/deno-aarch64-apple-darwin.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(zb.Bytes()) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	YTDLPBase, DenoBase = ts.URL+"/yt/", ts.URL+"/deno/"
	return ts
}

func TestUpdateChecksAndKeepsWhatWorks(t *testing.T) {
	defer func(a, b string) { YTDLPBase, DenoBase = a, b }(YTDLPBase, DenoBase)
	dir := t.TempDir()
	ctx := context.Background()
	release(t, []byte("#!/bin/sh\necho 2026.11.01\n"), []byte("#!/bin/sh\necho deno 2.5.0\n"), false)
	os.WriteFile(filepath.Join(dir, YTDLPName), []byte("#!/bin/sh\necho old\n"), 0o755) // v0.2.123's single file
	changed, err := Update(ctx, http.DefaultClient, dir, "darwin", "arm64", func(string) {})
	if err != nil || strings.Join(changed, ",") != "yt-dlp,Deno" {
		t.Fatalf("install: %v %v", changed, err)
	}
	tl := Find(dir)
	if !tl.Own || tl.Missing() != "" || Version(ctx, tl.YTDLP) != "2026.11.01" || tl.YTDLP != filepath.Join(dir, AppDir, "yt-dlp_macos") {
		t.Fatalf("after install: %+v", tl)
	}
	if _, err := os.Stat(filepath.Join(dir, AppDir, "_internal", "lib.txt")); err != nil {
		t.Fatal("the folder build was not unpacked whole")
	}
	if _, err := os.Stat(filepath.Join(dir, YTDLPName)); err == nil {
		t.Fatal("the old single-file yt-dlp was left beside the folder build")
	}
	if changed, err := Update(ctx, http.DefaultClient, dir, "darwin", "arm64", func(string) {}); err != nil || len(changed) != 0 {
		t.Fatalf("nothing new: %v %v", changed, err)
	}
	// A newer yt-dlp whose checksum does not match is refused; the old one stays.
	release(t, []byte("#!/bin/sh\necho evil\n"), []byte("#!/bin/sh\necho deno 2.5.0\n"), true)
	if _, err := Update(ctx, http.DefaultClient, dir, "darwin", "arm64", func(string) {}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a bad checksum was accepted: %v", err)
	}
	if Version(ctx, filepath.Join(dir, AppDir, "yt-dlp_macos")) != "2026.11.01" {
		t.Fatal("the working yt-dlp was replaced")
	}
	if es, _ := os.ReadDir(dir); len(es) != 3 {
		t.Fatalf("leftovers in the tools folder: %v", es)
	}
}

func TestProblemsInPlainWords(t *testing.T) {
	for in, want := range map[string]string{
		"ERROR: [youtube] x: Unable to download API page: ('Unable to connect to proxy')": "could not be reached",
		"ERROR: [youtube] x: Private video. Sign in if you've been granted access":        "private",
		"ERROR: [youtube] x: Video unavailable":                                           "not available",
		"ERROR: [youtube] x: Requested format is not available":                           "chosen quality",
		"ERROR: unable to download video data: HTTP Error 403: Forbidden":                 "updating yt-dlp",
		"WARNING: something\nnothing else":                                                "without saying why",
	} {
		if got := problem(in).Error(); !strings.Contains(got, want) {
			t.Errorf("%q: got %q", in, got)
		}
	}
	var g Gone
	if !errors.As(problem("ERROR: Private video"), &g) {
		t.Error("a private video should not be tried again")
	}
}

func TestUnzipAllRefusesNamesOutside(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create("../evil")
	f.Write([]byte("x"))
	w.Close()
	p := filepath.Join(t.TempDir(), "z.zip")
	os.WriteFile(p, b.Bytes(), 0o644)
	out := filepath.Join(t.TempDir(), "out")
	if err := unzipAll(p, out); err == nil {
		t.Fatal("a name outside the folder was unpacked")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(out), "evil")); err == nil {
		t.Fatal("a file was written outside the folder")
	}
}
