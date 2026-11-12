package sysinfo

import "testing"

func TestFacts(t *testing.T) {
	if TotalRAM() < 64<<20 {
		t.Fatalf("total RAM looks wrong: %d", TotalRAM())
	}
	if OpenFileLimit() == 0 {
		t.Fatal("open-file limit unknown")
	}
}
