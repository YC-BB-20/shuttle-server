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

// v0.2.33: a disk speed test with write and read gauges, from the Storage page.
func TestPageSpeedTest(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="spdDlg"`, `function gauge(box, label, val, color)`, `b("Test speed", () => openSpeed(d))`,
		`post("/api/speedtest/start"`, `post("/api/speedtest/stop")`, `$("spdRO").disabled = forced`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageSeparateCheckOnlyAfterAll(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`if (!end) $("ckSep").checked = false;`, `$("ckSep").closest(".opt").hidden = !end;`,
		`last.hash === "later" || sep ? "whenEnd" : "whenFile"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `$("whenBox").hidden = sep`) {
		t.Error("the when-to-check choice must stay visible with a separate check")
	}
}

func TestPageNewFolderInPicker(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="newDirBtn"`, `id="newDirBox"`, `post("/api/mkdir", { parent: pickAt, name: $("newDirName").value })`,
		`$("newDirBtn").hidden = pickFor !== "dst"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.36: the file info pop-up is as wide as the app on a wide screen, still
// fits a phone, and the copy speed is shown as an average.
func TestPageWideFileInfoAndAverageSpeed(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`#infoDlg { width:min(960px, calc(100vw - 24px));`, `#infoDlg .head h2 { min-width:0; overflow-wrap:anywhere; }`,
		`"Average copy speed "`, `(v.hashed || 0) + v.identical`, `<span>After each file is copied</span>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, "As each file is copied") {
		t.Error(`"As each file is copied" should read "After each file is copied"`)
	}
}

func TestPageNewCopyBeforeQueueTitle(t *testing.T) {
	s := string(IndexHTML)
	if !strings.Contains(s, `<div class="qhead"><h2>Queue</h2>`) || !strings.Contains(s, `<button class="primary big" type="button" id="newBtn">New copy</button></div>`) || !strings.Contains(s, `.qhead .big { flex:none; margin-left:auto;`) {
		t.Error("Queue, its filters and New copy (right end) should share one line")
	}
	if !strings.Contains(s, `<p class="mute" id="noJobs">Nothing in the queue.</p>`) || strings.Contains(s, "Press New copy") {
		t.Error("empty-queue message should be just \"Nothing in the queue.\"")
	}
}

func TestPagePickerStaysPutAndAlreadyThere(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`#pickDlg { height:min(80vh, 640px);`, `#pickDlg .body, #pickDlg .list { scrollbar-gutter:stable; }`,
		`$("newDirBtn").style.visibility =`, `.job.live .line, .job.live .sub, .job.live .speed { white-space:nowrap;`, `const b = el("div", "bar edge")`, `.frow .chip { min-width:11.5em;`, `c.classList.toggle("live", live);`, `already: "Already there · hash checked"`, `already2: "Already there · read twice"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageLiveChecksAndVerifyRows(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`proven: "Destination verified", strong: "Fully verified"`, `checkwait: "Copied · check queued"`,
		`f.v.checks && f.v.checks.checking`, `"↻ Checking now: "`, `"↳ Checks the copy "`,
		`.job.verify { margin-left:28px;`, `const fileHeads = { done: "Files verified:"`, `.job.open .acts .hidebtn { display:inline-block; }`, `btn(acts, v.hidden ? "↺" : "✕", () => setHidden(v, !v.hidden), "hidebtn"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
