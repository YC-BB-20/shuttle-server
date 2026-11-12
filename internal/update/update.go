// Package update installs new releases with one click. It asks GitHub's
// Actions API for the newest green CI run on main, downloads that run's
// binary for this machine, checks it starts and reports the expected commit,
// keeps the current binary as a backup, swaps the new one in, and asks the
// app to restart into it.
//
// The repository is private, so this needs a read-only access key. It is
// kept in a file only this user can read, sent only to api.github.com, and
// never logged, shown or returned by the API.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

// Release is a build that can be installed.
type Release struct {
	Version string    `json:"version"`
	Commit  string    `json:"commit"`
	Title   string    `json:"title"`
	Date    time.Time `json:"date"`
	RunID   int64     `json:"-"`
	ArtID   int64     `json:"-"`
	ZipURL  string    `json:"-"`
}

// State is what the page and the menu bar show. It never holds the key.
type State struct {
	KeySet    bool      `json:"key_set"`
	Busy      string    `json:"busy,omitempty"` // checking, downloading, installing
	LastCheck time.Time `json:"last_check,omitzero"`
	Error     string    `json:"error,omitempty"`
	Available *Release  `json:"available,omitempty"`
	Waiting   bool      `json:"waiting_for_copy"` // install when no copy is running
	Backup    string    `json:"backup,omitempty"` // previous binary, for rolling back
	Undo      string    `json:"undo,omitempty"`   // one Terminal line that undoes the last update
}

// Updater does the work.
type Updater struct {
	Repo     string // owner/name
	API      string // https://api.github.com (tests replace it)
	KeyPath  string
	BinPath  string
	Version  string
	Commit   string
	Log      *slog.Logger
	CopyBusy func() bool // a copy is running now
	Restart  func()      // restart into BinPath
	Client   *http.Client

	mu sync.Mutex
	st State
}

// Init fills in defaults and reads whether a key exists.
func (u *Updater) Init() {
	if u.API == "" {
		u.API = "https://api.github.com"
	}
	if u.Client == nil {
		u.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	_, err := os.Stat(u.KeyPath)
	u.mu.Lock()
	u.st.KeySet = err == nil
	if b := u.latestBackup(); b != "" {
		u.st.Backup, u.st.Undo = b, undoLine(b, u.BinPath)
	}
	u.mu.Unlock()
}

// State returns a copy for display.
func (u *Updater) State() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.st
	if s.Available != nil {
		a := *s.Available
		s.Available = &a
	}
	return s
}

var keyShape = regexp.MustCompile(`^(github_pat_[A-Za-z0-9_]{20,255}|gh[pousr]_[A-Za-z0-9]{20,255})$`)

// SetKey stores (or, if empty, removes) the access key.
func (u *Updater) SetKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		os.Remove(u.KeyPath) // the owner asked to forget it
		u.mu.Lock()
		u.st.KeySet, u.st.Available = false, nil
		u.mu.Unlock()
		u.Log.Info("update key removed")
		return nil
	}
	if !keyShape.MatchString(key) {
		return errors.New("that does not look like a GitHub access key (it should start with github_pat_)")
	}
	if err := os.MkdirAll(filepath.Dir(u.KeyPath), 0o700); err != nil {
		return err
	}
	if err := fsx.WriteFileAtomic(u.KeyPath, []byte(key), 0o600); err != nil {
		return err
	}
	u.mu.Lock()
	u.st.KeySet = true
	u.mu.Unlock()
	u.Log.Info("update key saved")
	return nil
}

func (u *Updater) key() (string, error) {
	b, err := os.ReadFile(u.KeyPath)
	if err != nil {
		return "", errors.New("updates are not set up yet: add a GitHub access key on the page")
	}
	return strings.TrimSpace(string(b)), nil
}

func (u *Updater) get(ctx context.Context, url string, out any) (*http.Response, error) {
	key, err := u.key()
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "shuttle-server/"+u.Version)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub (%v)", errors.Unwrap(err))
	}
	switch res.StatusCode {
	case 200:
	case 401:
		res.Body.Close()
		return nil, errors.New("GitHub refused the access key (it may have expired); add a new one on the page")
	case 403, 404:
		res.Body.Close()
		return nil, fmt.Errorf("GitHub answered %d: the key cannot read this repository's builds (it needs Actions: read)", res.StatusCode)
	default:
		res.Body.Close()
		return nil, fmt.Errorf("GitHub answered %d; try again later", res.StatusCode)
	}
	if out != nil {
		defer res.Body.Close()
		return res, json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
	}
	return res, nil
}

var versionInTitle = regexp.MustCompile(`^v(\d+\.\d+\.\d+)`)

func (u *Updater) setBusy(b string) {
	u.mu.Lock()
	u.st.Busy = b
	u.mu.Unlock()
}

func (u *Updater) fail(err error) error {
	u.mu.Lock()
	u.st.Busy, u.st.Error = "", err.Error()
	u.mu.Unlock()
	u.Log.Warn("update", "problem", err.Error())
	return err
}

type run struct {
	ID         int64     `json:"id"`
	HeadSHA    string    `json:"head_sha"`
	Created    time.Time `json:"created_at"`
	HeadCommit struct {
		Message string `json:"message"`
	} `json:"head_commit"`
}

func (r run) commit() string {
	if len(r.HeadSHA) > 7 {
		return r.HeadSHA[:7]
	}
	return r.HeadSHA
}

func (r run) title() string { return strings.SplitN(r.HeadCommit.Message, "\n", 2)[0] }

func versionOf(title string) string {
	if m := versionInTitle.FindStringSubmatch(title); m != nil {
		return m[1]
	}
	return ""
}

// greenRuns lists the newest successful builds of main, newest first.
func (u *Updater) greenRuns(ctx context.Context, n int) ([]run, error) {
	var runs struct {
		Runs []run `json:"workflow_runs"`
	}
	if _, err := u.get(ctx, fmt.Sprintf("%s/repos/%s/actions/runs?branch=main&status=success&event=push&per_page=%d", u.API, u.Repo, n), &runs); err != nil {
		return nil, err
	}
	return runs.Runs, nil
}

// Check looks for a newer green build.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	u.setBusy("checking")
	runs, err := u.greenRuns(ctx, 5)
	if err != nil {
		return nil, u.fail(err)
	}
	u.mu.Lock()
	u.st.LastCheck, u.st.Error, u.st.Busy = time.Now(), "", ""
	u.mu.Unlock()
	if len(runs) == 0 {
		return nil, u.fail(errors.New("no finished builds found"))
	}
	r := runs[0]
	if r.commit() == u.Commit {
		u.mu.Lock()
		u.st.Available = nil
		u.mu.Unlock()
		return nil, nil
	}
	rel, err := u.releaseOf(ctx, r)
	if err != nil {
		return nil, u.fail(err)
	}
	u.mu.Lock()
	u.st.Available = rel
	u.mu.Unlock()
	u.Log.Info("update available", "version", rel.Version, "commit", rel.Commit)
	return rel, nil
}

// releaseOf finds the download for this computer in one build.
func (u *Updater) releaseOf(ctx context.Context, r run) (*Release, error) {
	var arts struct {
		Artifacts []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Expired bool   `json:"expired"`
			URL     string `json:"archive_download_url"`
		} `json:"artifacts"`
	}
	if _, err := u.get(ctx, fmt.Sprintf("%s/repos/%s/actions/runs/%d/artifacts", u.API, u.Repo, r.ID), &arts); err != nil {
		return nil, err
	}
	want := "shuttle-server-" + runtime.GOOS + "-" + runtime.GOARCH
	rel := &Release{Commit: r.commit(), Title: r.title(), Date: r.Created, RunID: r.ID, Version: versionOf(r.title())}
	for _, a := range arts.Artifacts {
		if a.Name == want && !a.Expired {
			rel.ArtID, rel.ZipURL = a.ID, a.URL
		}
	}
	if rel.ZipURL == "" {
		return nil, fmt.Errorf("build %s has no download for this computer (%s); GitHub keeps downloads for 90 days", rel.Commit, want)
	}
	return rel, nil
}

// Choice is one version in the history list.
type Choice struct {
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	Title     string    `json:"title,omitempty"`
	Date      time.Time `json:"date,omitzero"`
	Installed bool      `json:"installed"` // running now
	Kept      bool      `json:"kept"`      // a copy is kept on this computer
	OnGitHub  bool      `json:"on_github"` // a green build that can be downloaded
}

// History lists the newest green builds and every version kept on this
// computer, newest first. Without a key, or offline, it lists the kept ones.
func (u *Updater) History(ctx context.Context) ([]Choice, string) {
	kept := u.kept()
	var out []Choice
	seen := map[string]bool{}
	note := ""
	if runs, err := u.greenRuns(ctx, 30); err != nil {
		note = err.Error()
	} else {
		for _, r := range runs {
			c := r.commit()
			if seen[c] {
				continue
			}
			seen[c] = true
			_, k := kept[c]
			out = append(out, Choice{Version: versionOf(r.title()), Commit: c, Title: r.title(), Date: r.Created,
				Installed: c == u.Commit, Kept: k, OnGitHub: true})
		}
	}
	for c, k := range kept {
		if !seen[c] {
			out = append(out, Choice{Version: k.version, Commit: c, Date: k.date, Installed: c == u.Commit, Kept: true})
		}
	}
	if !seen[u.Commit] && kept[u.Commit].path == "" {
		out = append(out, Choice{Version: u.Version, Commit: u.Commit, Installed: true})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Date.After(out[b].Date) })
	return out, note
}

type keptBin struct {
	path, version string
	date          time.Time
}

// kept lists bin/backups by commit. Names are shuttle-server-VERSION-COMMIT.
func (u *Updater) kept() map[string]keptBin {
	dir := filepath.Join(filepath.Dir(u.BinPath), "backups")
	ents, _ := os.ReadDir(dir)
	out := map[string]keptBin{}
	for _, e := range ents {
		parts := strings.Split(strings.TrimPrefix(e.Name(), "shuttle-server-"), "-")
		fi, err := e.Info()
		if len(parts) != 2 || err != nil || !fi.Mode().IsRegular() {
			continue
		}
		// A backup's own time may be reset by Rollback; the version number orders it well enough.
		out[parts[1]] = keptBin{path: filepath.Join(dir, e.Name()), version: parts[0], date: fi.ModTime()}
	}
	return out
}

// InstallCommit installs one chosen version, from the copy kept on this
// computer if there is one, otherwise downloaded from its green build. The
// same checks apply as for an update: it must start and name its commit, and
// the version replaced is kept.
func (u *Updater) InstallCommit(ctx context.Context, commit string) error {
	if commit == u.Commit {
		return errors.New("that version is already running")
	}
	u.mu.Lock()
	busy := u.st.Busy
	u.mu.Unlock()
	if busy != "" {
		return errors.New("an update step is already running")
	}
	if k, ok := u.kept()[commit]; ok {
		bin, err := os.ReadFile(k.path)
		if err != nil {
			return u.fail(err)
		}
		return u.put(ctx, &Release{Version: k.version, Commit: commit}, bin)
	}
	u.setBusy("checking")
	runs, err := u.greenRuns(ctx, 30)
	if err != nil {
		return u.fail(err)
	}
	for _, r := range runs {
		if r.commit() == commit {
			rel, err := u.releaseOf(ctx, r)
			if err != nil {
				return u.fail(err)
			}
			return u.download(ctx, rel)
		}
	}
	return u.fail(fmt.Errorf("version %s is not among the recent green builds", commit))
}

// Install downloads, checks and swaps in the available release, then asks
// for a restart. If a copy is running and now is false, it waits for the
// copy to finish instead (Watch does it).
func (u *Updater) Install(ctx context.Context, now bool) error {
	u.mu.Lock()
	rel := u.st.Available
	busy := u.st.Busy
	u.mu.Unlock()
	if busy != "" {
		return errors.New("an update step is already running")
	}
	if rel == nil {
		var err error
		if rel, err = u.Check(ctx); err != nil {
			return err
		}
		if rel == nil {
			return errors.New("Shuttle Server is already up to date")
		}
	}
	if !now && u.CopyBusy != nil && u.CopyBusy() {
		u.mu.Lock()
		u.st.Waiting = true
		u.mu.Unlock()
		u.Log.Info("update will install when the running copy finishes", "version", rel.Version)
		return nil
	}
	u.mu.Lock()
	u.st.Waiting = false
	u.mu.Unlock()
	return u.download(ctx, rel)
}

func (u *Updater) download(ctx context.Context, rel *Release) error {
	u.setBusy("downloading")
	res, err := u.get(ctx, rel.ZipURL, nil)
	if err != nil {
		return u.fail(err)
	}
	zipped, err := io.ReadAll(io.LimitReader(res.Body, 200<<20))
	res.Body.Close()
	if err != nil {
		return u.fail(fmt.Errorf("download interrupted: %v", err))
	}
	bin, err := extract(zipped)
	if err != nil {
		return u.fail(err)
	}
	return u.put(ctx, rel, bin)
}

// put checks the new program, keeps the current one, swaps and restarts.
func (u *Updater) put(ctx context.Context, rel *Release, bin []byte) error {
	u.setBusy("installing")
	dir := filepath.Dir(u.BinPath)
	next := filepath.Join(dir, ".shuttle-server.new")
	if err := fsx.WriteFileAtomic(next, bin, 0o755); err != nil {
		return u.fail(fmt.Errorf("could not save the new version: %v", err))
	}
	// The new program must start and name the commit it was built from.
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	out, err := exec.CommandContext(vctx, next, "-version").CombinedOutput()
	cancel()
	if err != nil || !strings.Contains(string(out), rel.Commit) {
		os.Remove(next) // our own download
		return u.fail(fmt.Errorf("the new version did not start correctly (%s); nothing was changed", strings.TrimSpace(string(out))))
	}
	// Keep the current program. Backups are never deleted automatically.
	cur, err := os.ReadFile(u.BinPath)
	if err != nil {
		return u.fail(err)
	}
	backup := filepath.Join(dir, "backups", fmt.Sprintf("shuttle-server-%s-%s", u.Version, u.Commit))
	os.MkdirAll(filepath.Dir(backup), 0o755)
	if _, err := os.Stat(backup); err != nil {
		if err := fsx.WriteFileAtomic(backup, cur, 0o755); err != nil {
			return u.fail(fmt.Errorf("could not keep a backup of the current version: %v", err))
		}
	}
	if err := os.Rename(next, u.BinPath); err != nil {
		return u.fail(fmt.Errorf("could not put the new version in place: %v", err))
	}
	u.mu.Lock()
	u.st.Busy, u.st.Backup, u.st.Undo, u.st.Available = "", backup, undoLine(backup, u.BinPath), nil
	u.mu.Unlock()
	u.Log.Info("update installed; restarting", "from", u.Version+" ("+u.Commit+")", "to", rel.Version+" ("+rel.Commit+")",
		"xxh128", xxh3.Sum128(bin).String(), "undo", undoLine(backup, u.BinPath))
	if u.Restart != nil {
		u.Restart()
	}
	return nil
}

// Rollback puts the most recent backup back and restarts.
func (u *Updater) Rollback() error {
	b := u.latestBackup()
	if b == "" {
		return errors.New("there is no earlier version to go back to")
	}
	data, err := os.ReadFile(b)
	if err != nil {
		return err
	}
	cur, _ := os.ReadFile(u.BinPath)
	keep := filepath.Join(filepath.Dir(b), fmt.Sprintf("shuttle-server-%s-%s", u.Version, u.Commit))
	if _, err := os.Stat(keep); err != nil && cur != nil {
		fsx.WriteFileAtomic(keep, cur, 0o755)
	}
	if err := fsx.WriteFileAtomic(u.BinPath, data, 0o755); err != nil {
		return err
	}
	os.Chtimes(b, time.Unix(0, 0), time.Unix(0, 0)) // so it is not picked again as "latest"
	u.Log.Info("rolled back to an earlier version; restarting", "version", filepath.Base(b))
	if u.Restart != nil {
		u.Restart()
	}
	return nil
}

func (u *Updater) latestBackup() string {
	ents, err := os.ReadDir(filepath.Join(filepath.Dir(u.BinPath), "backups"))
	if err != nil {
		return ""
	}
	var best string
	var bt time.Time
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && fi.ModTime().After(bt) && !strings.Contains(e.Name(), "-"+u.Commit) {
			best, bt = filepath.Join(filepath.Dir(u.BinPath), "backups", e.Name()), fi.ModTime()
		}
	}
	return best
}

func undoLine(backup, bin string) string {
	return fmt.Sprintf("cp %q %q", backup, bin)
}

// extract takes the binary out of the artifact: a zip holding a .tar.gz
// holding shuttle-server.
func extract(zipped []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, errors.New("the download is not a valid archive")
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".tar.gz") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		gz, err := gzip.NewReader(io.LimitReader(rc, 200<<20))
		if err != nil {
			return nil, errors.New("the download is damaged")
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err != nil {
				return nil, errors.New("the download does not contain shuttle-server")
			}
			if filepath.Base(h.Name) == "shuttle-server" && h.Typeflag == tar.TypeReg {
				return io.ReadAll(io.LimitReader(tr, 200<<20))
			}
		}
	}
	return nil, errors.New("the download does not contain the program")
}

// Watch checks every few hours while a key is set, and installs a waiting
// update once no copy is running.
func (u *Updater) Watch(ctx context.Context, every time.Duration) {
	next := time.Now().Add(30 * time.Second)
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s := u.State()
			if s.Waiting && s.Busy == "" && (u.CopyBusy == nil || !u.CopyBusy()) {
				u.Install(ctx, true)
			}
			if s.KeySet && s.Busy == "" && now.After(next) {
				u.Check(ctx)
				next = now.Add(every)
			}
		}
	}
}
