package shuttle

import (
	"strings"
	"testing"
)

// Copies are purple and verifies pink while they work, green when done and
// red for a problem, in the queue, its bars and the file chips.
func TestPageColoursCopyAndVerify(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		"--copy:#8e44d6", "--verify:#d63384",
		`.chip.copying { color:var(--copy)`, `.chip.verifying { color:var(--verify)`,
		`.job .ico.copy { background:var(--copy); }`, `.job .ico.verify { background:var(--verify); }`,
		`v.state === "running" ? "var(--" + kind + ")"`,
		`v.state === "finished" ? (problems ? "var(--bad)" : "var(--ok)")`,
		`problems ? ["!", "bad"] : ["✓", "ok"]`,
		`"chip " + (checking ? "verifying" : "copying")`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// Queue and History filter like Shuttle Router: kinds with counts, Active
// only, a time range and a sort, remembered in the browser.
func TestPageQueueFilters(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="qf"`, `data-k="done">Completed`, `data-k="skipped"`, `data-k="stopped">Stopped`,
		`attention">Failed <span`, `you paused">Paused <span`, `id="qfActive">Active only`,
		`<option value="yesterday">Yesterday</option>`, `<option value="week">Last 7 days</option>`,
		`<option value="queue:1">Queue order</option>`, `<option value="left:1">Time left</option>`,
		`function jobCat(v)`, `"Showing " + vis + " of " + total`, `localStorage.setItem("shuttle.qf"`,
		`return k === "active" || qf.show[k] !== false`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageSystemFilesSetting(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="skipSys"`, `id="skipDot"`, `id="sysNote"`, `post("/api/settings/copy"`,
		`" · system files left out: " + v.system_skipped`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPagePurpleAccentNoVerifyButton(t *testing.T) {
	s := string(IndexHTML)
	if strings.Contains(s, "0a64d8") {
		t.Error("page still uses the old blue accent")
	}
	if strings.Contains(s, `id="verBtn"`) {
		t.Error("page still has the top Verify… button")
	}
	for _, want := range []string{`--accent:#7a3cc8;`, `--accent:#8a4fe0;`, `.qfinfo { font-size:13px; margin:12px 0 8px; }`, `"Verify files"`, `"Read destination now"`, `"Read source again now"`, `"Verify files again"`, `Add job as paused`, `<b>Destinations, then the source read again</b>`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.30: room above the filters; history entries can be hidden, shown
// again from Settings or the Hidden button among the filters.
func TestPageHideHistory(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`.qf { margin:14px 0 4px; }`, `id="qfHidden"`, `id="showHidden"`,
		`"Hide from history"`, `"Show in history"`, `(hide ? "hide" : "unhide")`,
		`const listed = v => !v.hidden || qf.showHidden;`, `done.filter(v => listed(v) && shown(v) && inRange(v))`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.31: a Verify names its kind and what it reads now; rows show disks;
// a finished copy's row says what has been read back since.
func TestPageVerifyKindsDisksProof(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`"Source verification"`, `"Destination + source verification"`, `"Destination verification"`,
		`const diskOf = p =>`, `el("span", "dk", " · " + diskOf(place))`, `[ver && verNow(v), pc,`,
		`function proofLine(k)`, `v.checks ? " · " + proofLine(v.checks)`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.32: a Storage tab lists disks with connection and use, and offers
// copying from or to each.
func TestPageStorageTab(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="tabStorageBtn"`, `id="tabStorage"`, `fetch("/api/storage")`, `function diskCard(d)`,
		`"Copy from here"`, `"Copy to here"`, `function diskUse(path)`, `!d.read_only && !d.camera`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
