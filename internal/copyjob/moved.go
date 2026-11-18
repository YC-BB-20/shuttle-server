package copyjob

import (
	"path/filepath"
	"strings"
)

// Moved (v0.2.116) tells the records of earlier copies that the owner moved
// names from folder into to (the Move in Files: a rename on one drive). A
// copy's source, destination or wrapper folder at or inside a moved item
// takes its new place; a copy of chosen items of folder (Spec.Only) follows
// when every chosen item moved. Only finished or stopped copies are changed:
// Move refuses anything an unfinished job uses. Each record is saved with the
// usual crash-safe write. updated counts the copies changed; partial those
// of a choice only partly moved, which stay as they were.
func (m *Manager) Moved(folder string, names []string, to string) (updated, partial int) {
	folder, to = filepath.Clean(folder), filepath.Clean(to)
	moved := map[string]bool{}
	for _, n := range names {
		moved[n] = true
	}
	follow := func(p string) (string, bool) {
		if p == "" {
			return p, false
		}
		for n := range moved {
			old := filepath.Join(folder, n)
			if p == old || strings.HasPrefix(p, old+string(filepath.Separator)) {
				return filepath.Join(to, n) + strings.TrimPrefix(p, old), true
			}
		}
		return p, false
	}
	states := map[string]string{}
	for _, v := range m.Views() {
		states[v.ID] = v.State
	}
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()
	for _, j := range jobs {
		j.mu.Lock()
		if st := states[j.st.ID]; st != "finished" && st != "stopped" {
			j.mu.Unlock()
			continue
		}
		s := &j.st.Spec
		changed := false
		for _, p := range []*string{&s.Source, &s.Dest, &s.WrapDir} {
			if np, ok := follow(*p); ok {
				*p, changed = np, true
			}
		}
		if len(s.Only) > 0 && (filepath.Clean(s.Source) == folder || filepath.Clean(s.Dest) == folder) {
			all, some := true, false
			for _, n := range s.Only {
				if moved[n] {
					some = true
				} else {
					all = false
				}
			}
			switch {
			case all:
				if filepath.Clean(s.Source) == folder {
					s.Source = to
				}
				if filepath.Clean(s.Dest) == folder {
					s.Dest = to
				}
				changed = true
			case some:
				partial++
			}
		}
		j.mu.Unlock()
		if changed {
			if err := j.save(); err == nil {
				updated++
				if m.Log != nil {
					m.Log.Info("move: a copy's record follows what was moved", "job", j.st.ID, "into", to)
				}
			}
		}
	}
	return updated, partial
}
