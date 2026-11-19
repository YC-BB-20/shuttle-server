package copyjob

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/YC-BB-20/shuttle-server/internal/mp4"
)

// fakeYTDLP writes a stand-in yt-dlp and Deno into a tools folder. The
// stand-in answers a look-up with two picture streams and one sound stream,
// and "downloads" a stream by copying a fragmented MP4 made from the DASH
// pieces in package mp4's test data.
func fakeYTDLP(t *testing.T) (tools string, video, audio []byte) {
	t.Helper()
	rd := func(n string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "mp4", "testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	video = bytes.Join([][]byte{rd("init-0.m4s"), rd("chunk-0-1.m4s"), rd("chunk-0-2.m4s"), rd("chunk-0-3.m4s")}, nil)
	audio = bytes.Join([][]byte{rd("init-1.m4s"), rd("chunk-1-1.m4s"), rd("chunk-1-2.m4s"), rd("chunk-1-3.m4s"), rd("chunk-1-4.m4s")}, nil)
	tools = t.TempDir()
	streams := t.TempDir()
	os.WriteFile(filepath.Join(streams, "137"), video, 0o644)
	os.WriteFile(filepath.Join(streams, "140"), audio, 0o644)
	json := `{"_type":"video","id":"dQw4w9WgXcQ","title":"A test: video / one","duration":6,"formats":[` +
		`{"format_id":"140","ext":"m4a","vcodec":"none","acodec":"mp4a.40.2","abr":128,"filesize":` + itoa(len(audio)) + `,"protocol":"https"},` +
		`{"format_id":"136","ext":"mp4","vcodec":"avc1.4d401f","acodec":"none","height":720,"fps":30,"filesize":1000,"protocol":"https"},` +
		`{"format_id":"137","ext":"mp4","vcodec":"avc1.640028","acodec":"none","height":1080,"fps":30,"filesize":` + itoa(len(video)) + `,"protocol":"https"}]}`
	script := `#!/bin/sh
fmt=""; out=""; json=0
while [ $# -gt 0 ]; do
  case "$1" in
    -J) json=1 ;;
    -f) fmt="$2"; shift ;;
    -o) out="$2"; shift ;;
    --js-runtimes) [ -x "${2#deno:}" ] || { echo "ERROR: no deno" >&2; exit 1; }; shift ;;
  esac
  shift
done
if [ $json = 1 ]; then cat <<'JSON'
` + json + `
JSON
exit 0; fi
[ -f "` + streams + `/$fmt" ] || { echo "ERROR: Requested format is not available" >&2; exit 1; }
n=$(wc -c < "` + streams + `/$fmt")
echo "SHUTTLE $((n/2)) $n NA"
cp "` + streams + `/$fmt" "$out.part" && mv "$out.part" "$out"
echo "SHUTTLE $n $n NA"
`
	if err := os.WriteFile(filepath.Join(tools, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(tools, "deno"), []byte("#!/bin/sh\necho deno 2.5.0\n"), 0o755)
	return tools, video, audio
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestYouTubeDownloadIsJoinedIntoOneMP4(t *testing.T) {
	tools, video, audio := fakeYTDLP(t)
	root := t.TempDir()
	m := newManager(t, filepath.Join(root, "state"))
	m.Secrets, m.Tools = filepath.Join(root, "links"), tools
	startRun(t, m)
	dest := filepath.Join(root, "dst")
	os.MkdirAll(dest, 0o755)
	link := "https://youtu.be/dQw4w9WgXcQ?si=share"
	in := m.LookUpLinks(context.Background(), []string{link}, "")
	if in[0].Error != "" || in[0].Site != "youtube" || len(in[0].Qualities) != 3 || in[0].Qualities[0].Label != "1080p · H.264" || in[0].Qualities[2].Ext != "m4a" {
		t.Fatalf("look-up: %+v", in[0])
	}
	j, err := m.AddLink(context.Background(), link, dest, LinkOpts{})
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, j.st.ID, "finished")
	if v.LinkSite != "YouTube" || v.LinkHash == "" {
		t.Fatalf("%+v", v)
	}
	got := filepath.Join(dest, "A test video one.mp4")
	out, err := os.ReadFile(got)
	if err != nil {
		es, _ := os.ReadDir(dest)
		t.Fatalf("not there: %v %v", err, es)
	}
	es, _ := os.ReadDir(dest)
	if len(es) != 1 {
		t.Fatalf("leftovers beside the file: %v", es)
	}
	var top []string
	for _, b := range mp4.Boxes(out) {
		top = append(top, b.Type)
	}
	if strings.Join(top, " ") != "ftyp moov free mdat" && strings.Join(top, " ") != "ftyp moov mdat" {
		t.Fatalf("not an ordinary MP4: %v", top)
	}
	if len(out) < (len(video)+len(audio))*8/10 {
		t.Fatalf("joined file too small: %d of %d", len(out), len(video)+len(audio))
	}
	if p, err := exec.LookPath("ffprobe"); err == nil {
		o, err := exec.Command(p, "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", got).CombinedOutput()
		if err != nil || strings.Fields(string(o))[0] != "video" || !strings.Contains(string(o), "audio") {
			t.Fatalf("ffprobe: %v %s", err, o)
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil { // decodes from start to end without a complaint
		o, err := exec.Command(p, "-v", "error", "-i", got, "-f", "null", "-").CombinedOutput()
		if err != nil || len(bytes.TrimSpace(o)) > 0 {
			t.Fatalf("ffmpeg decode: %v %s", err, o)
		}
	}

	// Sound only.
	j, err = m.AddLink(context.Background(), link, dest, LinkOpts{Quality: "140"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, j.st.ID, "finished")
	if _, err := os.Stat(filepath.Join(dest, "A test video one.m4a")); err != nil {
		t.Fatal("sound only: not there")
	}
}

func TestYouTubeWithoutToolsSaysWhatIsMissing(t *testing.T) {
	root := t.TempDir()
	m := newManager(t, filepath.Join(root, "state"))
	m.Secrets, m.Tools = filepath.Join(root, "links"), t.TempDir()
	t.Setenv("PATH", t.TempDir())
	in := m.LookUpLinks(context.Background(), []string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube.com/playlist?list=PL1"}, "")
	if !strings.Contains(in[0].Error, "yt-dlp") || !strings.Contains(in[0].Error, "Settings") {
		t.Fatalf("missing tools: %q", in[0].Error)
	}
	if !strings.Contains(in[1].Error, "one video") {
		t.Fatalf("playlist: %q", in[1].Error)
	}
}
