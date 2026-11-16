package copyjob

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/location"
)

// A Draft is a finished list of a source folder (v0.2.72), written by the
// picker's count while the owner chooses. A new copy adopts it as its own
// file list, so the folder is listed once and the copy is sized the moment
// it is added. Only names, sizes and times are read; no file data.
type Draft struct {
	Dir                    string // holds manifest.jsonl and skipped.jsonl, in the job's own format
	Source                 string
	Only                   []string
	System                 bool // the rules the list was made with
	AppleDouble            bool
	Files, Bytes           int64
	Skipped, SystemSkipped int // links and unreadable items; system files left out
	Listed                 time.Time
}

// WriteDraft lists f into dir (made if missing) and returns the draft.
// progress is called every 500 files. A cancelled or failed list leaves
// nothing the caller should keep: remove dir.
func WriteDraft(ctx context.Context, f location.Folder, dir string, progress func(files, bytes int64)) (*Draft, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l, err := listManifest(ctx, f, filepath.Join(dir, "manifest.jsonl"), filepath.Join(dir, "skipped.jsonl"), func(n int, total int64) {
		if progress != nil {
			progress(int64(n), total)
		}
	})
	if err != nil {
		return nil, err
	}
	return &Draft{Dir: dir, Source: f.Dir, Only: f.Only, System: f.System, AppleDouble: f.AppleDouble,
		Files: int64(l.n), Bytes: l.total, Skipped: l.skipped, SystemSkipped: l.system, Listed: time.Now()}, nil
}

// fits says whether the draft was made for this very copy: the same folder,
// the same chosen names, the same system-file rules.
func (d *Draft) fits(src string, only []string, skipSystem, skipDot bool) bool {
	return d != nil && d.Source == src && slices.Equal(d.Only, only) && d.System == skipSystem && (!skipSystem || d.AppleDouble == skipDot)
}

// DraftDir is where drafts are made: inside the data folder's temp.noindex.
func (m *Manager) DraftDir() string {
	return filepath.Join(filepath.Dir(filepath.Dir(m.Dir)), "temp.noindex", "draft")
}

// adopt gives the job the draft's list by hard link (a copy of the file when
// a link is not possible), so the draft can be reused or removed. On failure
// the job keeps Phase "scan" and lists the folder itself.
func (j *Job) adopt(d *Draft) bool {
	for _, n := range []string{"manifest.jsonl", "skipped.jsonl"} {
		if err := linkOrCopy(filepath.Join(d.Dir, n), j.path(n)); err != nil {
			return false
		}
	}
	j.st.Files, j.st.Bytes, j.st.Skipped, j.st.SystemSkipped = int(d.Files), d.Bytes, d.Skipped, d.SystemSkipped
	j.st.Phase, j.st.ListedAt = "copy", d.Listed
	return true
}

func linkOrCopy(from, to string) error {
	os.Remove(to) // our own file in the job's folder, if a failed try left one
	if err := os.Link(from, to); err == nil {
		return nil
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// measureManifest is measure over a finished list: what a copy to dst still
// has to write, counting a file already there with the same size as done.
func measureManifest(ctx context.Context, manifest, dst string) (int64, error) {
	it, err := openManifest(manifest)
	if err != nil {
		return 0, err
	}
	defer it.Close()
	var total int64
	for it.Next() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if fi, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(it.E.P))); err == nil && fi.Mode().IsRegular() && fi.Size() == it.E.S {
			continue
		}
		total += it.E.S
	}
	return total, nil
}
