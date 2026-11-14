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

func TestChecksWhileVerifyRuns(t *testing.T) {
	cp := state{ID: "c1", Files: 11, Phase: "done"}
	ver := &Job{st: state{ID: "v1", Files: 11, Phase: "verify", Spec: Spec{Kind: KindVerify, From: []string{"c1"}}}}
	ver.c.Verified, ver.c.Failed = 3, 1
	k := checksOf([]*Job{ver}, ver, cp, counts{})
	if !k.Checking || k.Queued || k.CheckDone != 4 || k.CheckMatch != 3 || k.CheckOf != 11 {
		t.Errorf("running Verify: got %+v", *k)
	}
	k = checksOf([]*Job{ver}, nil, cp, counts{})
	if k.Checking || !k.Queued {
		t.Errorf("Verify not running yet should be queued: %+v", *k)
	}
	if rowStatus(stCheckWait) != "checkwait" || !filterOK("todo", "checkwait") {
		t.Error("files a Verify has still to check belong under In progress")
	}
}
