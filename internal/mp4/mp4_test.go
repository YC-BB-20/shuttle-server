package mp4

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// v0.2.91: real DASH pieces (6 s of picture and sound made by ffmpeg) are
// joined into one ordinary MP4 with its table of contents at the front.
// Where ffprobe is installed, the result must also decode without errors.
func TestJoinDash(t *testing.T) {
	rd := func(n string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	moov, vid, aid, err := MergeInit(rd("init-0.m4s"), rd("init-1.m4s"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if vid == aid {
		t.Fatalf("track ids %d %d", vid, aid)
	}
	room := Room(6, 25, 48000)
	var file bytes.Buffer
	file.Write(FTYP)
	file.Write(Reserve(room))
	file.Write(MdatHead(16)) // placeholder, rewritten by Finish
	var recs []byte
	var data uint64
	buf := make([]byte, 4096)
	pieces := []struct {
		name  string
		track uint32
	}{{"chunk-0-1.m4s", vid}, {"chunk-1-1.m4s", aid}, {"chunk-0-2.m4s", vid}, {"chunk-1-2.m4s", aid}, {"chunk-0-3.m4s", vid}, {"chunk-1-3.m4s", aid}, {"chunk-1-4.m4s", aid}}
	for _, p := range pieces {
		w, r, moofs, rc, err := CopyPayload(bytes.NewReader(rd(p.name)), func(b []byte) error { file.Write(b); return nil }, data, p.track, buf)
		if err != nil || moofs == 0 || r != uint64(len(rd(p.name))) {
			t.Fatalf("%s: %v, %d moofs, read %d", p.name, err, moofs, r)
		}
		data += w
		recs = append(recs, rc...)
	}
	out := file.Bytes()
	msg, err := Finish(moov, bytes.NewReader(recs), room, data, int64(len(out)), func(b []byte, off int64) error {
		if int(off)+len(b) > len(out) {
			out = append(out, make([]byte, int(off)+len(b)-len(out))...)
		}
		copy(out[off:], b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	top := Boxes(out)
	if len(top) != 4 || top[0].Type != "ftyp" || top[1].Type != "moov" || top[2].Type != "free" || top[3].Type != "mdat" {
		types := []string{}
		for _, b := range top {
			types = append(types, b.Type)
		}
		t.Fatalf("file layout %v (%s)", types, msg)
	}
	if bytes.Contains(top[1].B, []byte("mvex")) || len(top[3].B) != MdatHeader+int(data) {
		t.Fatal("the moov still says fragmented, or the mdat size is wrong")
	}
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		p := filepath.Join(t.TempDir(), "joined.mp4")
		os.WriteFile(p, out, 0o644)
		res, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", p, "-f", "null", "-").CombinedOutput()
		if err != nil || len(bytes.TrimSpace(res)) > 0 {
			t.Fatalf("ffmpeg could not decode the joined file: %v %s", err, res)
		}
		d, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", p).Output()
		if len(d) < 3 || d[0] != '6' && !bytes.HasPrefix(d, []byte("5.9")) {
			t.Fatalf("duration %q", d)
		}
	}
}
