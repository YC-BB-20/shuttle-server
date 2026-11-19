package ytdlp

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// Installing and updating: Shuttle fetches the official builds from the two
// projects' GitHub releases into its own tools folder, checks each against
// the checksum the release publishes, and only then puts it in place. Nothing
// is installed anywhere else on the computer. A build whose checksum does not
// match is thrown away and the one in place is kept.

// Release addresses; tests point them at a local server.
var (
	YTDLPBase = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/"
	DenoBase  = "https://github.com/denoland/deno/releases/latest/download/"
)

var updateMu sync.Mutex

// Client is the HTTP client for fetching releases: it follows GitHub's
// redirect to its download servers; each request has its own time limit.
func Client() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 30 * time.Second,
		ResponseHeaderTimeout: time.Minute, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 2}}
}

// maxTool bounds a download (yt-dlp is about 35 MB, Deno's zip about 50 MB
// and the program inside it about 130 MB).
const maxTool = 400 << 20

// Assets names the release files for a platform.
func Assets(goos, goarch string) (ytdlp, deno string, err error) {
	switch goos + "/" + goarch {
	case "darwin/arm64": // v0.2.124: the folder build, which starts in a moment; the single file unpacks itself on every run
		return "yt-dlp_macos.zip", "deno-aarch64-apple-darwin.zip", nil
	case "darwin/amd64":
		return "yt-dlp_macos.zip", "deno-x86_64-apple-darwin.zip", nil
	case "linux/arm64":
		return "yt-dlp_linux_aarch64", "deno-aarch64-unknown-linux-gnu.zip", nil
	case "linux/amd64":
		return "yt-dlp_linux", "deno-x86_64-unknown-linux-gnu.zip", nil
	}
	return "", "", fmt.Errorf("no YouTube tools are published for %s/%s", goos, goarch)
}

// installed is tools/installed.json: the checksums of what is in place.
type installed struct {
	YTDLP   string    `json:"yt_dlp_sha256,omitempty"`
	Deno    string    `json:"deno_zip_sha256,omitempty"`
	Checked time.Time `json:"checked,omitzero"`
}

func readInstalled(dir string) installed {
	var in installed
	if b, err := os.ReadFile(filepath.Join(dir, "installed.json")); err == nil {
		json.Unmarshal(b, &in)
	}
	return in
}

// LastChecked is when Shuttle last looked for newer builds.
func LastChecked(dir string) time.Time { return readInstalled(dir).Checked }

// Update installs yt-dlp and Deno into dir, or replaces them when the
// releases hold newer builds. say reports each step in plain words. It
// returns what changed ("yt-dlp", "Deno"), nothing when both were current.
func Update(ctx context.Context, c *http.Client, dir, goos, goarch string, say func(string)) ([]string, error) {
	updateMu.Lock() // one update at a time: the page's and the daily one
	defer updateMu.Unlock()
	ya, da, err := Assets(goos, goarch)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	in := readInstalled(dir)
	var changed []string
	say("Checking for the newest yt-dlp")
	sums, err := fetchSmall(ctx, c, YTDLPBase+"SHA2-256SUMS")
	if err != nil {
		return nil, fmt.Errorf("could not read yt-dlp's release: %w", err)
	}
	want := sumFor(sums, ya)
	if want == "" {
		return nil, errors.New("yt-dlp's release does not list " + ya)
	}
	folder := strings.HasSuffix(ya, ".zip")
	have := runnable(filepath.Join(dir, YTDLPName))
	if folder {
		have = runnable(filepath.Join(dir, AppDir, strings.TrimSuffix(ya, ".zip")))
	}
	if want != in.YTDLP || !have {
		say("Downloading yt-dlp")
		var err error
		if folder {
			err = fetchFolder(ctx, c, YTDLPBase+ya, want, dir)
		} else {
			err = fetchChecked(ctx, c, YTDLPBase+ya, want, filepath.Join(dir, YTDLPName), "")
		}
		if err != nil {
			return changed, fmt.Errorf("yt-dlp: %w", err)
		}
		if folder {
			os.Remove(filepath.Join(dir, YTDLPName)) // our own single-file build from v0.2.123, replaced by the folder
		}
		in.YTDLP = want
		changed = append(changed, "yt-dlp")
	}
	say("Checking for the newest Deno")
	ds, err := fetchSmall(ctx, c, DenoBase+da+".sha256sum")
	if err != nil {
		return changed, fmt.Errorf("could not read Deno's release: %w", err)
	}
	dwant := sumFor(ds, da)
	if dwant == "" {
		return changed, errors.New("Deno's release has no checksum for " + da)
	}
	if dwant != in.Deno || !runnable(filepath.Join(dir, DenoName)) {
		say("Downloading Deno")
		if err := fetchChecked(ctx, c, DenoBase+da, dwant, filepath.Join(dir, DenoName), DenoName); err != nil {
			return changed, fmt.Errorf("Deno: %w", err)
		}
		in.Deno = dwant
		changed = append(changed, "Deno")
	}
	in.Checked = time.Now()
	b, _ := json.Marshal(in)
	return changed, fsx.WriteFileAtomic(filepath.Join(dir, "installed.json"), b, 0o644)
}

// sumFor finds a file's checksum in a "HEX  NAME" list.
func sumFor(list []byte, name string) string {
	sc := bufio.NewScanner(strings.NewReader(string(list)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return strings.ToLower(f[0])
			}
		}
	}
	return ""
}

func get(ctx context.Context, c *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, errors.New("the download failed; is the internet connection up?")
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("the release answered HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func fetchSmall(ctx context.Context, c *http.Client, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	resp, err := get(ctx, c, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// fetchChecked downloads u into a temporary file beside dst, checks its
// SHA-256, and puts it (or, for a zip, the entry named inZip) in place.
func fetchChecked(ctx context.Context, c *http.Client, u, want, dst, inZip string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	resp, err := get(ctx, c, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp := dst + ".download"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // our own temporary file
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxTool+1))
	if err == nil && n > maxTool {
		err = errors.New("the file is larger than expected")
	}
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return errors.New("the download does not match the release's checksum; nothing was changed")
	}
	src := tmp
	if inZip != "" {
		out := dst + ".unzipped"
		defer os.Remove(out)
		if err := unzipOne(tmp, inZip, out); err != nil {
			return err
		}
		src = out
	}
	if err := os.Chmod(src, 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// unzipOne writes the zip entry named name to out.
func unzipOne(zipPath, name, out string) error {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return errors.New("the download is not a readable zip")
	}
	defer z.Close()
	for _, e := range z.File {
		if e.Name != name {
			continue
		}
		r, err := e.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(r, maxTool+1))
		if err == nil && n > maxTool {
			err = errors.New("the program in the zip is larger than expected")
		}
		if err == nil {
			err = f.Sync()
		}
		f.Close()
		return err
	}
	return errors.New("the zip does not hold " + name)
}

// AppDir is the folder build of yt-dlp inside the tools folder (macOS).
const AppDir = "yt-dlp-app"

// fetchFolder downloads a zipped folder build, checks it, unpacks it beside
// the one in place and swaps them while no yt-dlp is running.
func fetchFolder(ctx context.Context, c *http.Client, u, want, dir string) error {
	tmpZip := filepath.Join(dir, AppDir+".zip.download")
	defer os.Remove(tmpZip) // our own temporary files
	if err := fetchChecked(ctx, c, u, want, tmpZip+".ok", ""); err != nil {
		return err
	}
	if err := os.Rename(tmpZip+".ok", tmpZip); err != nil {
		return err
	}
	fresh, old := filepath.Join(dir, AppDir+".new"), filepath.Join(dir, AppDir+".old")
	os.RemoveAll(fresh) // our own leftovers from a cut-off try
	os.RemoveAll(old)
	if err := unzipAll(tmpZip, fresh); err != nil {
		os.RemoveAll(fresh)
		return err
	}
	if err := take(ctx); err != nil { // never swap under a running yt-dlp
		os.RemoveAll(fresh)
		return err
	}
	defer give()
	cur := filepath.Join(dir, AppDir)
	if _, err := os.Stat(cur); err == nil {
		if err := os.Rename(cur, old); err != nil {
			os.RemoveAll(fresh)
			return err
		}
	}
	if err := os.Rename(fresh, cur); err != nil {
		os.Rename(old, cur) // put the working one back
		return err
	}
	os.RemoveAll(old)
	return nil
}

// unzipAll unpacks a zip into out: regular files and folders only, nothing
// outside out, with the program bits the zip gives.
func unzipAll(zipPath, out string) error {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return errors.New("the download is not a readable zip")
	}
	defer z.Close()
	if len(z.File) > 5000 {
		return errors.New("the zip holds more files than expected")
	}
	var total int64
	for _, e := range z.File {
		name := filepath.FromSlash(e.Name)
		if !filepath.IsLocal(name) {
			return errors.New("the zip holds a name outside its folder")
		}
		p := filepath.Join(out, name)
		mode := e.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			return errors.New("the zip holds something other than files and folders")
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		r, err := e.Open()
		if err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if mode&0o111 != 0 {
			perm = 0o755
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
		if err != nil {
			r.Close()
			return err
		}
		n, err := io.Copy(f, io.LimitReader(r, maxTool+1-total))
		total += n
		r.Close()
		if err == nil && total > maxTool {
			err = errors.New("the zip unpacks larger than expected")
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
