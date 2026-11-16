package copyjob

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/YC-BB-20/shuttle-server/internal/fsx"
)

// Settings are the owner's choices for new copies and verifies, kept in
// state/copy-settings.json. Each job copies them into its Spec when it is
// added, so changing them never changes a job already in the queue.
type Settings struct {
	FormatVersion int `json:"format_version"`
	// SkipSystem leaves out .DS_Store and the other files and folders the
	// system makes for itself (on by default). SkipAppleDouble also leaves
	// out "._" files; it applies only with SkipSystem on (off by default).
	SkipSystem      bool `json:"skip_system_files"`
	SkipAppleDouble bool `json:"skip_appledouble_files"`
	// Hash is which hashes a new copy makes and proves with: "xxh128" (the
	// default), "xxh64" or "both" (added in 0.2.58; it replaced 0.2.57's
	// XXH64 lists, which are a later function).
	Hash string `json:"hash"`
	// RelistMinutes (added in 0.2.72): a copy's files are listed once, when it
	// is added. If it then waits in the queue longer than this before it
	// begins, its folder is listed again. 0 means never; default 60.
	RelistMinutes int `json:"relist_minutes"`
}

// DefaultSettings are used until the owner saves a choice.
var DefaultSettings = Settings{FormatVersion: 1, SkipSystem: true, Hash: AlgoXXH128, RelistMinutes: 60}

func (m *Manager) settingsPath() string {
	return filepath.Join(filepath.Dir(m.Dir), "copy-settings.json")
}

// Settings returns the current choices. A missing or damaged file gives the
// defaults; a damaged one is left in place for the owner to look at.
func (m *Manager) Settings() Settings {
	m.setMu.Lock()
	defer m.setMu.Unlock()
	if m.set == nil {
		s := DefaultSettings
		if b, err := os.ReadFile(m.settingsPath()); err == nil {
			r := DefaultSettings // a setting missing from an older file keeps its default
			if json.Unmarshal(b, &r) == nil {
				s = r
			}
		}
		s.Hash = normAlgo(s.Hash)
		s.RelistMinutes = clampRelist(s.RelistMinutes)
		m.set = &s
	}
	return *m.set
}

// SetSettings saves the choices with an atomic write.
func (m *Manager) SetSettings(s Settings) (Settings, error) {
	s.FormatVersion = 1
	s.Hash = normAlgo(s.Hash)
	s.RelistMinutes = clampRelist(s.RelistMinutes)
	b, _ := json.MarshalIndent(s, "", "  ")
	m.setMu.Lock()
	defer m.setMu.Unlock()
	if err := fsx.WriteFileAtomic(m.settingsPath(), append(b, '\n'), 0o600); err != nil {
		return Settings{}, err
	}
	m.set = &s
	return s, nil
}

// clampRelist keeps the re-list time between never (0) and a week.
func clampRelist(m int) int { return min(max(m, 0), 7*24*60) }
