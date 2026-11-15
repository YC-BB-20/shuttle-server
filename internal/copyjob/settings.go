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
}

// DefaultSettings are used until the owner saves a choice.
var DefaultSettings = Settings{FormatVersion: 1, SkipSystem: true, Hash: AlgoXXH128}

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
			var r Settings
			if json.Unmarshal(b, &r) == nil {
				s = r
			}
		}
		s.Hash = normAlgo(s.Hash)
		m.set = &s
	}
	return *m.set
}

// SetSettings saves the choices with an atomic write.
func (m *Manager) SetSettings(s Settings) (Settings, error) {
	s.FormatVersion = 1
	s.Hash = normAlgo(s.Hash)
	b, _ := json.MarshalIndent(s, "", "  ")
	m.setMu.Lock()
	defer m.setMu.Unlock()
	if err := fsx.WriteFileAtomic(m.settingsPath(), append(b, '\n'), 0o600); err != nil {
		return Settings{}, err
	}
	m.set = &s
	return s, nil
}
