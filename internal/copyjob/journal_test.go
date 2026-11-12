package copyjob

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJournalReplayAndTornTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal.log")
	j, n, err := OpenJournal(p, func(Rec) {})
	if err != nil || n != 0 {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		j.Append(Rec{I: i, St: "verified", N: 10})
	}
	j.Close()
	// A crash mid-write leaves half a line.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`0badc0de {"i":5,"st":"veri`)
	f.Close()
	var got []int
	j, n, err = OpenJournal(p, func(r Rec) { got = append(got, r.I) })
	if err != nil || n != 5 || len(got) != 5 {
		t.Fatalf("n=%d got=%v err=%v", n, got, err)
	}
	j.Append(Rec{I: 5, St: "verified"})
	j.Close()
	got = nil
	j, n, _ = OpenJournal(p, func(r Rec) { got = append(got, r.I) })
	j.Close()
	if n != 6 || got[5] != 5 {
		t.Fatalf("torn tail not cut cleanly: %v", got)
	}
}

func TestJournalRejectsDamagedLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal.log")
	j, _, _ := OpenJournal(p, func(Rec) {})
	j.Append(Rec{I: 0, St: "verified"})
	j.Append(Rec{I: 1, St: "verified"})
	j.Close()
	b, _ := os.ReadFile(p)
	b[len(b)-5] ^= 1 // flip a bit in the last record
	os.WriteFile(p, b, 0o600)
	n := 0
	j, _, _ = OpenJournal(p, func(Rec) { n++ })
	j.Close()
	if n != 1 {
		t.Fatalf("damaged record accepted: %d", n)
	}
}
