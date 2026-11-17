package copyjob

import (
	"path/filepath"
	"strings"
	"time"
)

// The file browser (v0.2.89): for one folder, every copy Shuttle made of what
// is in it, and how each file stands in each copy. A folder is matched as part
// of a copy's source, or as part of a copy's destination; either way all the
// copies of that same source are shown, so a backup drive's folder shows the
// other drives too. Read only, from the copies' own records.

// CopyPlace is one copy of the folder: a copy job and where its files went.
type CopyPlace struct {
	Job     string    `json:"job"`
	Dest    string    `json:"dest"`  // this folder's place in the copy
	Disk    string    `json:"disk"`  // the copy's disk
	State   string    `json:"state"` // running, waiting, paused, queued, finished, stopped
	Created time.Time `json:"created"`
	Algo    string    `json:"algo"`
	Partial bool      `json:"partial,omitempty"` // more files than are read for one folder: some are left out
}

// CopyFile is one file's state in each place, in the order of Places; Status
// is "" when that copy does not hold the file.
type CopyFile struct {
	Status []string `json:"status"`
	I      []int    `json:"i"` // its index in each copy, -1 when not there
}

// CopyFolder is a folder inside, with how its files stand in each place.
type CopyFolder struct {
	Files  []int `json:"files"`
	Proven []int `json:"proven"` // read back and matching (with or without the source read twice)
	Strong []int `json:"strong"` // also the source read twice
	Bad    []int `json:"bad"`
}

// Copies is the answer for one folder.
type Copies struct {
	Source  string                `json:"source"` // the original of this folder, when known
	Places  []CopyPlace           `json:"places"`
	Files   map[string]CopyFile   `json:"files"`
	Folders map[string]CopyFolder `json:"folders"`
}

// maxCopiesFiles bounds how many files of one folder are read from each copy.
const maxCopiesFiles = 4000

// CopiesOf answers for folder dir.
func (m *Manager) CopiesOf(dir string) Copies {
	out := Copies{Places: []CopyPlace{}, Files: map[string]CopyFile{}, Folders: map[string]CopyFolder{}}
	dir = filepath.Clean(dir)
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()
	type cand struct {
		j   *Job
		st  state
		rel string
	}
	// The original: this folder inside a copy's source, or inside a copy's destination.
	src, rel := "", ""
	for i := len(jobs) - 1; i >= 0 && src == ""; i-- {
		j := jobs[i]
		j.mu.Lock()
		st := j.st
		j.mu.Unlock()
		if st.Spec.isVerify() || st.Phase == "scan" {
			continue
		}
		for _, root := range []string{st.Spec.Source, st.Spec.Dest} {
			if r, ok := under(dir, root); ok {
				src, rel = st.Spec.Source, r
				break
			}
		}
	}
	if src == "" {
		m.childCopies(dir, jobs, &out)
		return out
	}
	out.Source = filepath.Join(src, filepath.FromSlash(rel))
	// Every copy of that source, the newest per destination.
	var cs []cand
	seen := map[string]bool{}
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		j.mu.Lock()
		st := j.st
		j.mu.Unlock()
		if st.Spec.isVerify() || st.Phase == "scan" || filepath.Clean(st.Spec.Source) != filepath.Clean(src) || seen[st.Spec.Dest] {
			continue
		}
		if len(st.Spec.Only) > 0 && rel != "" && !onlyCovers(st.Spec.Only, rel) {
			continue
		}
		seen[st.Spec.Dest] = true
		cs = append(cs, cand{j, st, rel})
	}
	views := map[string]View{}
	for _, v := range m.Views() {
		views[v.ID] = v
	}
	for pi, c := range cs {
		disk, _ := placeOf(c.st.Spec.Dest)
		state := views[c.st.ID].State
		out.Places = append(out.Places, CopyPlace{Job: c.st.ID, Dest: filepath.Join(c.st.Spec.Dest, filepath.FromSlash(rel)), Disk: disk, State: state, Created: c.st.Created, Algo: c.st.Spec.Algo})
		n := 0
		for from := 0; from >= 0 && n < maxCopiesFiles; {
			page, err := m.FilesIn(c.st.ID, "all", true, rel, from, MaxFilePage)
			if err != nil {
				break
			}
			if from == 0 {
				for _, f := range page.Folders {
					cf := out.Folders[f.Name]
					cf = growFolder(cf, len(cs))
					cf.Files[pi], cf.Proven[pi], cf.Strong[pi], cf.Bad[pi] = f.Files, f.Strong+f.Dest, f.Strong, f.Problems
					out.Folders[f.Name] = cf
				}
			}
			for _, r := range page.Rows {
				name := r.Path
				if k := strings.LastIndexByte(name, '/'); k >= 0 {
					name = name[k+1:]
				}
				cf, ok := out.Files[name]
				if !ok {
					cf = CopyFile{Status: make([]string, len(cs)), I: make([]int, len(cs))}
					for k := range cf.I {
						cf.I[k] = -1
					}
				}
				cf.Status[pi], cf.I[pi] = r.Status, r.I
				out.Files[name] = cf
				n++
			}
			from = page.Next
		}
		if n >= maxCopiesFiles {
			out.Places[pi].Partial = true
		}
	}
	return out
}

func growFolder(f CopyFolder, n int) CopyFolder {
	if len(f.Files) == n {
		return f
	}
	return CopyFolder{Files: make([]int, n), Proven: make([]int, n), Strong: make([]int, n), Bad: make([]int, n)}
}

// under says whether path is root or inside it, and the path inside it with "/".
func under(path, root string) (string, bool) {
	root = filepath.Clean(root)
	if path == root {
		return "", true
	}
	if strings.HasPrefix(path, root+string(filepath.Separator)) {
		return filepath.ToSlash(path[len(root)+1:]), true
	}
	return "", false
}

// onlyCovers: a copy of chosen names holds folder rel when rel is one of them
// or inside one.
func onlyCovers(only []string, rel string) bool {
	top := rel
	if k := strings.IndexByte(rel, '/'); k >= 0 {
		top = rel[:k]
	}
	for _, o := range only {
		if o == top {
			return true
		}
	}
	return false
}

// childCopies fills a folder that holds folders copied on their own (a card's
// parent): one place per destination disk, and each copied folder's counts
// on it. These places carry no destination folder: copying more into them is
// done from inside the copied folder.
func (m *Manager) childCopies(dir string, jobs []*Job, out *Copies) {
	views := map[string]View{}
	for _, v := range m.Views() {
		views[v.ID] = v
	}
	type key struct{ name, disk string }
	newest := map[key]View{}
	disks := []string{}
	for _, j := range jobs {
		j.mu.Lock()
		st := j.st
		j.mu.Unlock()
		if st.Spec.isVerify() || st.Phase == "scan" || len(st.Spec.Only) > 0 || filepath.Dir(filepath.Clean(st.Spec.Source)) != dir {
			continue
		}
		v, ok := views[st.ID]
		if !ok {
			continue
		}
		disk, _ := placeOf(st.Spec.Dest)
		k := key{filepath.Base(st.Spec.Source), disk}
		if o, ok := newest[k]; !ok || o.Created.Before(v.Created) {
			newest[k] = v
		}
		if !contains(disks, disk) {
			disks = append(disks, disk)
		}
	}
	for _, d := range disks {
		out.Places = append(out.Places, CopyPlace{Disk: d})
	}
	for k, v := range newest {
		pi := index(disks, k.disk)
		cf := growFolder(out.Folders[k.name], len(disks))
		files, proven, strong := v.Files, v.Verified+v.Identical, v.Strong
		if v.Checks != nil {
			files, proven, strong = v.Checks.Of, v.Checks.Dest, v.Checks.Source
		}
		cf.Files[pi], cf.Proven[pi], cf.Strong[pi], cf.Bad[pi] = files, proven, strong, v.Failed
		out.Folders[k.name] = cf
	}
}

func contains(l []string, s string) bool { return index(l, s) >= 0 }

func index(l []string, s string) int {
	for i, x := range l {
		if x == s {
			return i
		}
	}
	return -1
}
