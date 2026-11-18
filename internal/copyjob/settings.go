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
	// Folder layout (v0.2.81), the defaults New copy starts from; each copy
	// can change them. Wrap puts a copy in a wrapper folder (D/<name>/<source
	// folder>); WrapNameFrom is where the wrapper's name comes from, "folder"
	// (the source folder's name, the default) or "volume" (its disk's name);
	// WrapHash writes the hash list beside the media folder when the copy ends.
	Wrap         bool   `json:"wrap"`
	WrapNameFrom string `json:"wrap_name_from"`
	WrapHash     bool   `json:"wrap_hash"`
	// SafeCopies (v0.2.91) is how many read-back copies make a card Safe to
	// unplug in the On set view, and Verified once the source is read again:
	// 1 to 4, 2 by default.
	SafeCopies int `json:"safe_copies"`
	// Delete (v0.2.106, COMPATIBILITY.md rule 2): off until the owner turns
	// it on. DeleteShares is the separate second switch for SMB shares and
	// the LucidLink mount, where an item is deleted for good (no Trash).
	// DeleteConfirms is how many questions the page asks first: 1 or 2.
	DeleteOn       bool `json:"delete_on"`
	DeleteShares   bool `json:"delete_shares"`
	DeleteConfirms int  `json:"delete_confirms"`
	// LucidRead (v0.2.110) is how copies to LucidLink are read back unless
	// New copy says otherwise: "cloud" (the default) or "cache".
	LucidRead string `json:"lucid_read"`
}

// DefaultSettings are used until the owner saves a choice.
var DefaultSettings = Settings{FormatVersion: 1, SkipSystem: true, Hash: AlgoXXH128, WrapNameFrom: "folder", SafeCopies: 2, DeleteConfirms: 2, LucidRead: LucidReadCloud}

// How a copy on LucidLink is read back (Spec.LucidRead).
const (
	LucidReadCloud = "cloud"
	LucidReadCache = "cache"
)

func normLucidRead(r string) string {
	if r == LucidReadCache {
		return r
	}
	return LucidReadCloud
}

func normConfirms(n int) int {
	if n != 1 {
		return 2 // anything but 1 is the safer choice
	}
	return 1
}

func normSafe(n int) int {
	if n < 1 || n > 4 {
		return DefaultSettings.SafeCopies
	}
	return n
}

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
		s.WrapNameFrom = normWrapFrom(s.WrapNameFrom)
		s.SafeCopies = normSafe(s.SafeCopies)
		s.DeleteConfirms = normConfirms(s.DeleteConfirms)
		s.LucidRead = normLucidRead(s.LucidRead)
		m.set = &s
	}
	return *m.set
}

// SetSettings saves the choices with an atomic write.
func (m *Manager) SetSettings(s Settings) (Settings, error) {
	s.FormatVersion = 1
	s.Hash = normAlgo(s.Hash)
	s.WrapNameFrom = normWrapFrom(s.WrapNameFrom)
	s.SafeCopies = normSafe(s.SafeCopies)
	s.DeleteConfirms = normConfirms(s.DeleteConfirms)
	s.LucidRead = normLucidRead(s.LucidRead)
	if !s.DeleteOn {
		s.DeleteShares = false // the second switch only means something with the first
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	m.setMu.Lock()
	defer m.setMu.Unlock()
	if err := fsx.WriteFileAtomic(m.settingsPath(), append(b, '\n'), 0o600); err != nil {
		return Settings{}, err
	}
	m.set = &s
	return s, nil
}

func normWrapFrom(f string) string {
	if f == "volume" {
		return f
	}
	return "folder"
}
