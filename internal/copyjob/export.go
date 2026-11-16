package copyjob

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// v0.2.80: hash files made from a copy's own records. Nothing is read from a
// disk again and nothing is written to the destination: the digests come from
// the journal, the names and sizes from the manifest. Written one file at a
// time, so a list of hundreds of thousands of files is never held in memory
// (8 bytes per file for the journal positions, as the status table).

// Export formats.
const (
	FormatXXHSum = "xxhsum" // "hash  path" per file, checked by hand with xxhsum -c
	FormatCSV    = "csv"    // path, size, modified, hash, proof, date
)

// MaxExportPaths bounds how many folders or files one export is limited to.
const MaxExportPaths = 1000

// digestOf picks one algorithm's digest out of a stored one ("xxh128",
// "xxh64", or "a+b" when both were made). "" when the copy has none of it.
func digestOf(h, algo string) string {
	a, b, both := strings.Cut(h, "+")
	switch {
	case both && algo == AlgoXXH64:
		return b
	case both:
		return a
	case h == "":
		return ""
	case len(h) == 16 && algo == AlgoXXH64, len(h) == 32 && algo != AlgoXXH64:
		return h
	}
	return ""
}

// proofName is a file's proof level in words, from its row status.
func proofName(status string) string {
	switch status {
	case "strong", "already2":
		return "Strong"
	case "proven", "already":
		return "Verified"
	case "hashed":
		return "Copied, hashed, not read back"
	case "source":
		return "Source matches stored hash"
	}
	return status
}

// inScope says whether manifest path p is one of the chosen folders or files
// (an empty list is everything).
func inScope(p string, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, s := range paths {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

// bestRecords finds, for each file of a copy, the journal line that holds the
// digest of what is at the destination: a read-back (D1) when there is one,
// else the hash made while copying (C). It returns each line's start, -1 when
// the file has none.
func bestRecords(journal string, n int) ([]int64, error) {
	f, err := os.Open(journal)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pos, rank := make([]int64, n), make([]byte, n)
	for i := range pos {
		pos[i] = -1
	}
	br := bufio.NewReaderSize(f, 64<<10)
	var off int64
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if r, ok := parseLine(line); ok && r.I >= 0 && r.I < n {
				k := byte(0)
				switch {
				case r.D1 != "":
					k = 2
				case r.C != "" && r.St != "failed":
					k = 1
				}
				if k > 0 && k >= rank[r.I] {
					pos[r.I], rank[r.I] = off, k
				}
			}
			off += int64(len(line))
		}
		if err != nil {
			break
		}
	}
	return pos, nil
}

// Export writes a hash list of a copy to w. algo is xxh128 or xxh64 (for
// both-hash copies the owner picks; "" is the copy's own). paths limits it to
// folders or files inside the copy, as relative paths. Files that have no
// digest of that kind (failed, deleted, or made with the other hash) are left
// out and counted in skipped. Paths are relative to the copy's destination
// folder.
func (m *Manager) Export(w io.Writer, id, format, algo string, paths []string) (written, skipped int, err error) {
	j := m.find(id)
	if j == nil {
		return 0, 0, errors.New("no such copy")
	}
	if len(paths) > MaxExportPaths {
		return 0, 0, fmt.Errorf("choose at most %d folders or files", MaxExportPaths)
	}
	j.mu.Lock()
	st := j.st
	j.mu.Unlock()
	if st.Spec.isVerify() || st.Phase == "scan" {
		return 0, 0, errors.New("a hash list can be made from a copy that has started")
	}
	if algo == "" {
		algo = normAlgo(st.Spec.Algo)
	}
	if algo == AlgoBoth {
		algo = AlgoXXH128
	}
	for i, p := range paths {
		paths[i] = strings.Trim(filepath.ToSlash(p), "/")
	}
	listMu.Lock()
	defer listMu.Unlock()
	pos, err := bestRecords(j.path("journal.log"), st.Files)
	if err != nil {
		return 0, 0, errors.New("this copy has no records yet")
	}
	table, _ := m.mergedStatus(j)
	jf, err := os.Open(j.path("journal.log"))
	if err != nil {
		return 0, 0, err
	}
	defer jf.Close()
	it, err := openManifest(j.path("manifest.jsonl"))
	if err != nil {
		return 0, 0, err
	}
	defer it.Close()
	bw := bufio.NewWriterSize(w, 64<<10)
	var cw *csv.Writer
	if format == FormatCSV {
		cw = csv.NewWriter(bw)
		cw.Write([]string{"path", "size", "modified", "hash", "proof", "date"})
	}
	for it.Next() {
		e := it.E
		if !inScope(e.P, paths) {
			continue
		}
		if e.I < 0 || e.I >= len(pos) || pos[e.I] < 0 {
			skipped++
			continue
		}
		r, ok := recordAt(jf, pos[e.I])
		if !ok {
			skipped++
			continue
		}
		h := r.D1
		if h == "" {
			h = r.C
		}
		d := digestOf(h, algo)
		if d == "" {
			skipped++
			continue
		}
		if cw == nil {
			fmt.Fprintf(bw, "%s  %s\n", d, e.P)
		} else {
			status := "waiting"
			if e.I < len(table) {
				status = rowStatus(table[e.I])
			}
			size := e.S
			if r.N > 0 || e.S == 0 {
				size = r.N
			}
			cw.Write([]string{e.P, strconv.FormatInt(size, 10), time.Unix(0, e.M).UTC().Format(time.RFC3339), d, proofName(status),
				time.Unix(r.T, 0).UTC().Format(time.RFC3339)})
		}
		written++
	}
	if cw != nil {
		cw.Flush()
		if err := cw.Error(); err != nil {
			return written, skipped, err
		}
	}
	return written, skipped, bw.Flush()
}

// recordAt reads the journal line that starts at off.
func recordAt(f *os.File, off int64) (Rec, bool) {
	br := bufio.NewReaderSize(io.NewSectionReader(f, off, 1<<20), 4<<10)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return Rec{}, false
	}
	return parseLine(line)
}

// writeWrapperHashFile writes the copy's hash list one level above its media
// folder (v0.2.81): wrapper/hashes-YYYYMMDD-HHMM.txt beside wrapper/NAME/
// (named by the time it is written, v0.2.83). Never over an existing file.
func (m *Manager) writeWrapperHashFile(j *Job) {
	j.mu.Lock()
	sp := j.st.Spec
	j.mu.Unlock()
	if !sp.Wrap || !sp.WrapHash || sp.WrapDir == "" {
		return
	}
	var buf strings.Builder
	n, _, err := m.Export(&buf, j.st.ID, FormatXXHSum, "", nil)
	if err != nil || n == 0 {
		if err != nil {
			m.Log.Warn("the hash file beside the media folder could not be made", "job", j.st.ID, "err", err)
		}
		return
	}
	stamp := "hashes-" + time.Now().Format("20060102-1504") // year month day - hour minute
	for k := 1; k < 1000; k++ {
		file := stamp + ".txt"
		if k > 1 { // two lists in the same minute: the second is never allowed to replace the first
			file = fmt.Sprintf("%s-%d.txt", stamp, k)
		}
		p := filepath.Join(sp.WrapDir, file)
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		if err := fsx.WriteFileAtomic(p, []byte(buf.String()), 0o644); err != nil {
			m.Log.Warn("the hash file beside the media folder could not be written", "job", j.st.ID, "err", err)
		} else {
			m.Log.Info("hash file written beside the media folder", "job", j.st.ID, "file", p, "files", n)
		}
		return
	}
}
