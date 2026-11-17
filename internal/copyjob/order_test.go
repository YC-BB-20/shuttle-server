package copyjob

import (
	"path/filepath"
	"testing"
)

// queueIDs is the queue as it will run: the order of m.jobs.
func queueIDs(m *Manager) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, j := range m.jobs {
		out = append(out, j.st.ID)
	}
	return out
}

func sameOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("queue %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("queue %v, want %v", got, want)
		}
	}
}

// v0.2.85: the owner moves waiting jobs; the order is saved and survives a
// restart, picks follow it, and finished jobs cannot move.
func TestMoveQueue(t *testing.T) {
	src, dir, state := t.TempDir(), t.TempDir(), t.TempDir()
	makeTree(t, src, 1000)
	m := newManager(t, state)
	var ids []string
	for i := 0; i < 3; i++ {
		j, err := m.Add(Spec{Source: src, Dest: filepath.Join(dir, string(rune('a'+i)))})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.st.ID)
	}
	a, b, c := ids[0], ids[1], ids[2]
	sameOrder(t, queueIDs(m), a, b, c)

	// Run next: c before a.
	if err := m.Move(c, a); err != nil {
		t.Fatal(err)
	}
	sameOrder(t, queueIDs(m), c, a, b)
	if p := m.pick(); p == nil || p.st.ID != c {
		t.Fatalf("pick after the move is not the moved job")
	}
	// To the end, and between two.
	if err := m.Move(c, ""); err != nil {
		t.Fatal(err)
	}
	sameOrder(t, queueIDs(m), a, b, c)
	if err := m.Move(a, c); err != nil {
		t.Fatal(err)
	}
	sameOrder(t, queueIDs(m), b, a, c)
	// Many moves into the same gap still keep the order (respacing).
	for i := 0; i < 70; i++ {
		x, y := a, b
		if i%2 == 1 {
			x, y = b, a
		}
		if err := m.Move(x, y); err != nil {
			t.Fatal(err)
		}
	}
	sameOrder(t, queueIDs(m), b, a, c)

	// The view carries the order, lowest first.
	order := map[string]int{}
	for _, v := range m.Views() {
		order[v.ID] = v.Order
	}
	if !(order[b] < order[a] && order[a] < order[c]) {
		t.Fatalf("view order %v does not follow the queue", order)
	}

	// A restart reads the same order back.
	m2 := newManager(t, state)
	sameOrder(t, queueIDs(m2), b, a, c)

	// Unknown jobs and finished jobs are refused.
	if err := m2.Move("nope", ""); err == nil {
		t.Fatal("moving an unknown job worked")
	}
	j := m2.find(a)
	j.mu.Lock()
	j.st.Phase = "done"
	j.mu.Unlock()
	if err := m2.Move(a, ""); err == nil {
		t.Fatal("moving a finished job worked")
	}
	if err := m2.Move(c, a); err == nil {
		t.Fatal("moving before a finished job worked")
	}
}

// v0.2.85: jobs added in the same millisecond each get their own id and
// folder; before, the second overwrote the first's job.json.
func TestSameMillisecondJobsKeepTheirOwnFolder(t *testing.T) {
	m := newManager(t, t.TempDir())
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		id, err := m.newJobDir("20261117-120000.000")
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("id %s given twice", id)
		}
		seen[id] = true
	}
	if !seen["20261117-120000.000"] || !seen["20261117-120000.000-2"] || !seen["20261117-120000.000-3"] {
		t.Fatalf("ids %v", seen)
	}
}
