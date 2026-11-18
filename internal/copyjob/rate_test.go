package copyjob

import (
	"testing"
	"time"
)

// A disk moving 16 MB every 5 seconds (3.2 MB/s) shows about 3.2 MB/s every
// second, never nothing (v0.2.110).
func TestDiskRatesSteadyForBursts(t *testing.T) {
	t0 := time.Unix(1000, 0)
	r := &rateMemo{disk: []diskSample{{t0, 0, 0}}}
	var d int64
	for sec := 1; sec <= 60; sec++ {
		if sec%5 == 0 {
			d += 16 << 20
		}
		_, dr := r.diskRates(t0.Add(time.Duration(sec)*time.Second), 0, d)
		if sec >= 20 && (dr < 2_500_000 || dr > 4_500_000) {
			t.Fatalf("second %d: %d B/s", sec, dr)
		}
	}
	if len(r.disk) > 32 {
		t.Fatalf("kept %d samples", len(r.disk))
	}
	// Nothing moving for a whole window: the speed falls to nothing.
	var dr int64
	for sec := 61; sec <= 80; sec++ {
		_, dr = r.diskRates(t0.Add(time.Duration(sec)*time.Second), 0, d)
	}
	if dr != 0 {
		t.Fatalf("idle: %d", dr)
	}
}
