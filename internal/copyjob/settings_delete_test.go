package copyjob

import (
	"path/filepath"
	"testing"
)

// Delete's settings (v0.2.106): off by default, the share switch only with
// the main one, and 1 or 2 confirmations (anything else is 2).
func TestDeleteSettings(t *testing.T) {
	m := newManager(t, filepath.Join(t.TempDir(), "state"))
	if s := m.Settings(); s.DeleteOn || s.DeleteShares || s.DeleteConfirms != 2 {
		t.Fatalf("defaults %+v", s)
	}
	s, err := m.SetSettings(Settings{DeleteShares: true, DeleteConfirms: 0})
	if err != nil || s.DeleteShares || s.DeleteConfirms != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	s, _ = m.SetSettings(Settings{DeleteOn: true, DeleteShares: true, DeleteConfirms: 1})
	if !s.DeleteOn || !s.DeleteShares || s.DeleteConfirms != 1 {
		t.Fatalf("%+v", s)
	}
	m2 := newManager(t, m.Dir)
	if s := m2.Settings(); !s.DeleteOn || s.DeleteConfirms != 1 {
		t.Fatalf("not kept: %+v", s)
	}
}
