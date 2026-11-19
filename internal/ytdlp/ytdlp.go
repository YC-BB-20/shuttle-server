// Package ytdlp runs yt-dlp, the outside program that reads YouTube
// (DESIGN.md section 9, v0.2.123). YouTube needs a JavaScript engine beside
// it: Deno, the one yt-dlp enables by default. Shuttle asks yt-dlp what a
// video offers and has it download the chosen picture and sound streams; the
// two are joined into one MP4 by Shuttle itself (package mp4), so ffmpeg is
// not needed.
//
// Rules kept here (section 18): one yt-dlp at a time, in its own process
// group so it is stopped completely, at a lower priority, with a time limit
// on look-ups and a stall timer on downloads, and its output capped.
package ytdlp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Tools are the paths of the two programs; empty when not found.
type Tools struct {
	YTDLP string `json:"yt_dlp"`
	Deno  string `json:"deno"`
	Own   bool   `json:"own"` // installed by Shuttle in its tools folder (and so updated by it)
}

// Missing says in plain words what is not installed, or "".
func (t Tools) Missing() string {
	switch {
	case t.YTDLP == "" && t.Deno == "":
		return "YouTube needs yt-dlp and Deno, which are not installed. Install them in Settings → YouTube."
	case t.YTDLP == "":
		return "YouTube needs yt-dlp, which is not installed. Install it in Settings → YouTube."
	case t.Deno == "":
		return "YouTube needs Deno (yt-dlp's JavaScript engine), which is not installed. Install it in Settings → YouTube."
	}
	return ""
}

// Names of the programs in Shuttle's tools folder.
const (
	YTDLPName = "yt-dlp"
	DenoName  = "deno"
)

// Find looks in Shuttle's tools folder first, then the usual places.
func Find(dir string) Tools {
	var t Tools
	look := func(name string) string {
		if dir != "" {
			if p := filepath.Join(dir, name); runnable(p) {
				return p
			}
		}
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"} {
			if p := filepath.Join(d, name); runnable(p) {
				return p
			}
		}
		return ""
	}
	t.YTDLP, t.Deno = look(YTDLPName), look(DenoName)
	t.Own = dir != "" && t.YTDLP == filepath.Join(dir, YTDLPName)
	return t
}

func runnable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// Video is what a look-up found.
type Video struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Duration float64  `json:"duration"`
	Live     bool     `json:"is_live"`
	Type     string   `json:"_type"`
	Formats  []Format `json:"formats"`
}

// Format is one stream YouTube offers.
type Format struct {
	ID       string  `json:"format_id"`
	Ext      string  `json:"ext"`
	VCodec   string  `json:"vcodec"`
	ACodec   string  `json:"acodec"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Size     int64   `json:"filesize"`
	Approx   int64   `json:"filesize_approx"`
	TBR      float64 `json:"tbr"`
	ABR      float64 `json:"abr"`
	Protocol string  `json:"protocol"`
}

// Bytes is the format's size, or its estimate.
func (f Format) Bytes(duration float64) int64 {
	switch {
	case f.Size > 0:
		return f.Size
	case f.Approx > 0:
		return f.Approx
	case f.TBR > 0 && duration > 0:
		return int64(f.TBR * 1000 / 8 * duration)
	}
	return 0
}

func (f Format) direct() bool {
	return f.Protocol == "" || f.Protocol == "https" || f.Protocol == "http"
}

// Codec names a picture codec in plain words.
func (f Format) Codec() string {
	switch {
	case strings.HasPrefix(f.VCodec, "avc1"):
		return "H.264"
	case strings.HasPrefix(f.VCodec, "av01"):
		return "AV1"
	case strings.HasPrefix(f.VCodec, "hev1"), strings.HasPrefix(f.VCodec, "hvc1"):
		return "HEVC"
	}
	return f.VCodec
}

// Choice is one quality offered in the page: a picture stream with the best
// sound, or the sound alone. ID is "VIDEO+AUDIO" or "AUDIO".
type Choice struct {
	ID     string
	Label  string
	Bytes  int64
	Height int
	FPS    float64
	Ext    string // mp4 or m4a
}

// Choices lists what can be downloaded as one MP4 (picture in an MP4
// container with M4A sound) or as M4A sound: the H.264 picture at its best
// first (it opens everywhere), then every other height, best first.
func (v Video) Choices() []Choice {
	var audio *Format
	for i, f := range v.Formats {
		if f.VCodec == "none" && strings.HasPrefix(f.ACodec, "mp4a") && f.Ext == "m4a" && f.direct() {
			if audio == nil || f.ABR > audio.ABR {
				audio = &v.Formats[i]
			}
		}
	}
	if audio == nil {
		return nil
	}
	best := map[string]Format{} // "height codec" → the best of them
	for _, f := range v.Formats {
		if f.VCodec == "" || f.VCodec == "none" || f.ACodec != "none" || f.Ext != "mp4" || f.Height == 0 || !f.direct() {
			continue
		}
		k := fmt.Sprintf("%d %s", f.Height, f.Codec())
		if b, ok := best[k]; !ok || f.FPS > b.FPS || f.FPS == b.FPS && f.TBR > b.TBR {
			best[k] = f
		}
	}
	fs := make([]Format, 0, len(best))
	for _, f := range best {
		fs = append(fs, f)
	}
	sort.Slice(fs, func(a, b int) bool {
		if fs[a].Height != fs[b].Height {
			return fs[a].Height > fs[b].Height
		}
		return fs[a].Codec() == "H.264" // at one height, H.264 first
	})
	for i, f := range fs { // the best H.264 goes first: the default
		if f.Codec() == "H.264" {
			fs = append([]Format{f}, append(fs[:i:i], fs[i+1:]...)...)
			break
		}
	}
	ab := audio.Bytes(v.Duration)
	var out []Choice
	for _, f := range fs {
		label := fmt.Sprintf("%dp", f.Height)
		if f.FPS > 30 {
			label += fmt.Sprintf("%.0f", f.FPS)
		}
		out = append(out, Choice{ID: f.ID + "+" + audio.ID, Label: label + " · " + f.Codec(), Bytes: f.Bytes(v.Duration) + ab, Height: f.Height, FPS: f.FPS, Ext: "mp4"})
	}
	out = append(out, Choice{ID: audio.ID, Label: "Sound only (M4A)", Bytes: ab, Ext: "m4a"})
	return out
}

// VideoID finds the video in a YouTube link: watch?v=, youtu.be/, /shorts/,
// /live/, /embed/. "" when the link is not one video.
func VideoID(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	var id string
	switch h {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		} else {
			for _, p := range []string{"/shorts/", "/live/", "/embed/", "/v/"} {
				if r, ok := strings.CutPrefix(u.Path, p); ok {
					id = strings.Trim(r, "/")
				}
			}
		}
	}
	if !idRe.MatchString(id) {
		return ""
	}
	return id
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// IsYouTube says whether a link is on YouTube at all (a playlist or a
// channel too), so the page can say it needs one video.
func IsYouTube(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	return h == "youtube.com" || h == "youtu.be" || h == "m.youtube.com" || h == "music.youtube.com"
}

// WatchURL is the plain link of a video, the only thing given to yt-dlp.
func WatchURL(id string) string { return "https://www.youtube.com/watch?v=" + id }

// one yt-dlp at a time (DESIGN.md section 18: one heavy outside program).
var slot = make(chan struct{}, 1)

func take(ctx context.Context) error {
	select {
	case slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func give() { <-slot }

// LookTimeout bounds one look-up.
var LookTimeout = 90 * time.Second

// command builds a yt-dlp run: no config file of the owner's, Deno as its
// JavaScript engine, its own process group, a lower priority.
func command(ctx context.Context, t Tools, args ...string) *exec.Cmd {
	base := []string{"--ignore-config", "--no-playlist", "--no-warnings", "--color", "never"}
	if t.Deno != "" {
		base = append(base, "--js-runtimes", "deno:"+t.Deno)
	}
	c := exec.CommandContext(ctx, t.YTDLP, append(base, args...)...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 3 * time.Second
	c.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	return c
}

// lower runs a started program at a lower priority, like Shuttle itself.
func lower(c *exec.Cmd) {
	if c.Process != nil {
		syscall.Setpriority(syscall.PRIO_PGRP, c.Process.Pid, 10)
	}
}

// capped keeps at most 32 MB of output (a look-up's JSON can be a few MB).
type capped struct {
	mu sync.Mutex
	b  []byte
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.b) < 32<<20 {
		c.b = append(c.b, p[:min(len(p), 32<<20-len(c.b))]...)
	}
	c.mu.Unlock()
	return len(p), nil
}

// problem turns yt-dlp's last error line into plain words.
func problem(stderr string) error {
	line := ""
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "ERROR:") {
			line = strings.TrimSpace(strings.TrimPrefix(l, "ERROR:"))
		}
	}
	low := strings.ToLower(line)
	switch {
	case line == "":
		return errors.New("yt-dlp stopped without saying why")
	case strings.Contains(low, "requested format is not available"):
		return Gone("YouTube no longer offers the chosen quality; add the link again")
	case strings.Contains(low, "private video"):
		return Gone("This video is private")
	case strings.Contains(low, "sign in to confirm your age"), strings.Contains(low, "age-restricted"):
		return Gone("This video is age-restricted; YouTube wants a signed-in account")
	case strings.Contains(low, "sign in to confirm you"), strings.Contains(low, "not a bot"):
		return errors.New("YouTube asked to confirm this is not a bot; try again later")
	case strings.Contains(low, "http error 403"):
		return errors.New("YouTube refused the download (403); updating yt-dlp in Settings → YouTube usually fixes this")
	case strings.Contains(low, "video unavailable"), strings.Contains(low, "has been removed"), strings.Contains(low, "is not available"):
		return Gone("YouTube says this video is not available")
	case strings.Contains(low, "unable to download"), strings.Contains(low, "unable to connect"), strings.Contains(low, "timed out"),
		strings.Contains(low, "name resolution"), strings.Contains(low, "nodename nor servname"):
		return errors.New("YouTube could not be reached; is the internet connection up?")
	}
	if len(line) > 300 {
		line = line[:300]
	}
	return errors.New("yt-dlp: " + line)
}

// Gone is a problem trying again cannot fix.
type Gone string

func (g Gone) Error() string { return string(g) }

// Look asks yt-dlp what a video offers.
func Look(ctx context.Context, t Tools, id string) (Video, error) {
	if m := t.Missing(); m != "" {
		return Video{}, Gone(m)
	}
	if err := take(ctx); err != nil { // waiting for another yt-dlp does not count against the time limit
		return Video{}, err
	}
	defer give()
	ctx, cancel := context.WithTimeout(ctx, LookTimeout)
	defer cancel()
	c := command(ctx, t, "-J", "--no-progress", WatchURL(id))
	var out, errs capped
	c.Stdout, c.Stderr = &out, &errs
	if err := c.Start(); err != nil {
		return Video{}, fmt.Errorf("yt-dlp could not be started: %w", err)
	}
	lower(c)
	err := c.Wait()
	if ctx.Err() != nil {
		return Video{}, errors.New("YouTube did not answer in time")
	}
	if err != nil {
		return Video{}, problem(string(errs.b))
	}
	var v Video
	if err := json.Unmarshal(out.b, &v); err != nil {
		return Video{}, errors.New("yt-dlp's answer could not be read")
	}
	if v.Live {
		return Video{}, Gone("This is a live stream; Shuttle downloads finished videos only")
	}
	if v.Type == "playlist" {
		return Video{}, Gone("This is a playlist. Paste the link of one video")
	}
	return v, nil
}

// StallAfter is how long a download may go without any progress.
var StallAfter = 5 * time.Minute

var progRe = regexp.MustCompile(`^SHUTTLE (\S+) (\S+) (\S+)`)

// Download has yt-dlp download one stream to out, continuing a partial
// download it left as out.part. progress gets the bytes so far and the total
// (0 when unknown).
func Download(ctx context.Context, t Tools, id, format, out string, progress func(done, total int64)) error {
	if m := t.Missing(); m != "" {
		return Gone(m)
	}
	if err := take(ctx); err != nil {
		return err
	}
	defer give()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	c := command(ctx, t, "-f", format, "--continue", "--no-mtime", "--no-overwrites", "--newline", "--progress",
		"--progress-template", "download:SHUTTLE %(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s",
		"--retries", "20", "--fragment-retries", "20", "-o", out, WatchURL(id))
	pr, pw := io.Pipe()
	var errs capped
	c.Stdout, c.Stderr = pw, io.MultiWriter(&errs, pw)
	if err := c.Start(); err != nil {
		return fmt.Errorf("yt-dlp could not be started: %w", err)
	}
	lower(c)
	moved := make(chan struct{}, 1)
	go func() { // the stall timer: no progress line for StallAfter stops it
		timer := time.NewTimer(StallAfter)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-moved:
				timer.Reset(StallAfter)
			case <-timer.C:
				cancel(errors.New("yt-dlp stopped making progress"))
				return
			}
		}
	}()
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			m := progRe.FindStringSubmatch(sc.Text())
			if m == nil {
				continue
			}
			done := num(m[1])
			total := num(m[2])
			if total <= 0 {
				total = num(m[3])
			}
			select {
			case moved <- struct{}{}:
			default:
			}
			if progress != nil {
				progress(done, total)
			}
		}
		io.Copy(io.Discard, pr)
	}()
	err := c.Wait()
	pw.Close()
	if cause := context.Cause(ctx); cause != nil && ctx.Err() != nil {
		return cause
	}
	if err != nil {
		return problem(string(errs.b))
	}
	if _, err := os.Stat(out); err != nil {
		return errors.New("yt-dlp finished but the file is not there")
	}
	return nil
}

func num(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f)
}

// Version asks a program for its version (yt-dlp --version, deno --version).
func Version(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, path, "--version")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second
	var out capped
	c.Stdout = &out
	if c.Run() != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out.b)), "\n")
	if len(line) > 80 {
		line = line[:80]
	}
	return strings.TrimSpace(line)
}
