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
	for _, want := range []string{`--accent:#7a3cc8;`, `--accent:#8a4fe0;`, `.qfinfo { font-size:13px; margin:12px 0 8px; }`, `"Verify files"`, `"Verify files again"`, `Add job as paused`, `<b>Destinations, then the source read again</b>`} {
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
		`id="tabStorageBtn"`, `id="tabStorage"`, `fetch("/api/storage"`, `function diskCard(d)`,
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
	if !strings.Contains(s, `<div class="qhead"><button class="primary big" type="button" id="newBtn">New copy</button><h2>Queue</h2>`) || strings.Contains(s, `margin-left:auto; padding:9px 18px`) {
		t.Error("New copy should come first, before the Queue title and the filters, on one line")
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
		`.job.verify.nested { margin-left:28px;`, `const fileHeads = { done: "Files verified:"`, `.job.open .acts .hidebtn { display:inline-block; }`, `function verifyBox(path, title, from, pick)`, `btn(acts, v.hidden ? "↺" : "✕", () => setHidden(v, !v.hidden), "hidebtn"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageNoRereadButtonsInChecks(t *testing.T) {
	s := string(IndexHTML)
	if strings.Contains(s, "Read destination now") || strings.Contains(s, "Read source again now") {
		t.Error("re-reads start only from Verify files")
	}
}

// v0.2.44: comparing a file already there says Verifying, never Copying;
// files already there count as verified, not copied; file info shows three
// reads and a Verify button for any that was never made.
func TestPageVerifyingWordAndThreeReads(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{"^Already at the destination", "function proofSlots", "Not done: the copy has not been read back.", "Not done: the source has not been read a second time.", "verifyBox(fi.job_dest", "already there (verified, not copied)"} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, "· Checked \" + checked") {
		t.Error("old Copied / Checked / Completed counts still there")
	}
}

// v0.2.45: a Verify is set in only under the copy it checks; elsewhere it
// stands on its own, never under an unrelated copy.
func TestPageVerifyNestedOnlyUnderItsCopy(t *testing.T) {
	s := string(IndexHTML)
	if !strings.Contains(s, "function withVerifies") || !strings.Contains(s, ".job.verify.nested {") || strings.Contains(s, ".job.verify { margin-left:28px") {
		t.Error("Verify rows must be set in only under their own copy")
	}
}

// v0.2.46: many files show as closed folders with counts; Show more / Show
// fewer beside Verify files; Verify files only on a copy; a Verify's file
// info has the three lines too, with a button for the missing one.
func TestPageFoldersPagingAndVerifyInfo(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{"function dirRows", "function folderChip", `"&tree=1&dir="`, `"Show more files"`, `"Show fewer files"`, `<div class="fpage">`,
		`if (old && !ver) btn(btns, "Verify files"`, `ver ? fi.from : [infoAt.id]`, `body.push(...proofSlots(fi));`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.47: Storage updates itself while open, and shows each disk's port,
// what is in between, disks sharing the port, and items in its Trash.
func TestPageStorageLive(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`stoTimer = setTimeout(loadStorage, 3000)`, `!document.hidden`, `now !== stoLast`, `"Attached to " + d.bus`,
		`"Shares this port with "`, `d.trash`, `loadStorage(true)`, `?fresh=1`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.48: the picker and New copy show the copy size while it is counted.
func TestPageCopySize(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="pickSize"`, `id="srcSize"`, `function askSize(path, only, out, lead)`, `"/api/size?path="`, `"Counting… "`,
		`"Copy size: "`, `"This whole folder: "`, `useLabel(); pickSize();`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
