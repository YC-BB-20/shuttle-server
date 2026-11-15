package copyjob

import "testing"

func TestAvgWorkRate(t *testing.T) {
	if avgWorkRate(1e9, 5) != 0 {
		t.Fatal("under 10 s of running is not measured yet")
	}
	if got := avgWorkRate(3e9, 30); got != 1e8 {
		t.Fatalf("got %d", got)
	}
	if cmpRate(0, 5) != 5 || cmpRate(7, 5) != 7 {
		t.Fatal("average wins when measured")
	}
}
