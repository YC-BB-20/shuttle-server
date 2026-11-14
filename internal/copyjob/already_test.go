package copyjob

import "testing"

func TestAlreadyThereStatus(t *testing.T) {
	j := &Job{status: make([]byte, 130)}
	j.apply(Rec{I: 129, St: "identical", N: 5})
	j.apply(Rec{I: 3, St: "verified", N: 5})
	if !isAlready(j.already, 129) || isAlready(j.already, 3) || isAlready(j.already, 500) {
		t.Fatal("already bits wrong")
	}
	if got := alreadyStatus(rowStatus(j.status[129]), isAlready(j.already, 129)); got != "already" {
		t.Errorf("file found at destination shows %q, want already", got)
	}
	if got := alreadyStatus("strong", true); got != "already2" {
		t.Errorf("got %q, want already2", got)
	}
	if got := alreadyStatus("failed", true); got != "failed" {
		t.Errorf("a later failure must win, got %q", got)
	}
	if !filterOK("done", "already") || !filterOK("done", "already2") {
		t.Error("already-there files belong under Verified")
	}
}
