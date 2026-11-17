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
		"--copy:#a07ef2;", "--verify:#dcd0fd;",
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
	for _, want := range []string{`--accent:#7c4de0;`, `.qfinfo { font-size:13px; margin:12px 0 8px; }`, `"Verify files"`, `"Verify files again"`, `Add job as paused`, `<b>Destinations, then the source read again</b>`} {
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
		`"average copy speed"`, `(v.hashed || 0) + v.identical`, `<span>After each file is copied</span>`,
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
	// v0.2.85: New copy sits in the top bar, on every screen; the queue title leads its own line.
	if !strings.Contains(s, `<button class="primary" type="button" id="newBtn">New copy</button>`) || !strings.Contains(s, `<div class="qhead"><h2>Queue<span class="qcount" id="qCount"></span></h2>`) {
		t.Error("New copy should be in the top bar and the Queue title first in the queue's head")
	}
	if !strings.Contains(s, `<p class="mute" id="noJobs">Nothing in the queue.</p>`) || strings.Contains(s, "Press New copy") {
		t.Error("empty-queue message should be just \"Nothing in the queue.\"")
	}
}

func TestPagePickerStaysPutAndAlreadyThere(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`#pickDlg { height:min(80vh, 640px);`, `#pickDlg .body, #pickDlg .list { scrollbar-gutter:stable; }`,
		`$("newDirBtn").style.visibility =`, `.job.going .sub, .job.going .speed { white-space:nowrap;`, `const b = el("div", "bar edge"`, `.frow .chip { min-width:11.5em;`, `c.classList.toggle("going", live);`, `already: "Already there · hash checked"`, `already2: "Already there · read twice"`,
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
		`id="pickSize"`, `id="srcSize"`, `function askSize(path, only, out, lead)`, `"/api/size?path="`, `"Counting…"`,
		`"Copy size: "`, `"This whole folder: "`, `useLabel(); pickSize();`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.49: every file list shows its folders as rows that close, even one
// folder and few files; small lists start open; a folder click never closes
// the copy card; verifying bars are purple on a light purple track.
func TestPageFolderHeadsAndVerifyBars(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`tree: true,`, `function openExport`, `function problemCounts`, `id="layoutSet"`, `/check-source`, `/copy-added`, `function awaitCount(seq)`, `Preparing: listing the files`, `body.est_bytes = lastCount.bytes`, `v.est_bytes ? v.est_bytes`, `run.avg_rate || run.rate`, `e.stopPropagation(); openDir(f, fr.path)`,
		`.bar.vbar {`, `.bar.vbar > i { background:var(--verify) !important; }`, `(isCheckStage(r.stage) ? " vbar" : "")`, `classList.toggle("vbar", vnow && ver)`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.73: opening a copy shows its folders closed; nothing opens on its own.
func TestPageFoldersStartClosed(t *testing.T) {
	s := string(IndexHTML)
	for _, bad := range []string{"autoOpen", "f.auto", "autoPer"} {
		if strings.Contains(s, bad) {
			t.Errorf("page still opens folders by itself: %q", bad)
		}
	}
}

// v0.2.75: a name declared twice at the top of the page script is a syntax
// error that stops the whole page (v0.2.74 shipped one: "Loading..." forever).
func TestPageNoDuplicateTopLevelNames(t *testing.T) {
	s := string(IndexHTML)
	a, b := strings.Index(s, "<script"), strings.LastIndex(s, "</script>")
	if a < 0 || b < a {
		t.Fatal("no script in the page")
	}
	seen := map[string]bool{}
	for _, ln := range strings.Split(s[a:b], "\n") {
		for _, kw := range []string{"let ", "const ", "var ", "function "} {
			if !strings.HasPrefix(ln, kw) {
				continue
			}
			name := strings.TrimPrefix(ln, kw)
			end := strings.IndexFunc(name, func(r rune) bool {
				return !(r == '_' || r == '$' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
			})
			if end > 0 {
				name = name[:end]
			}
			if seen[name] {
				t.Errorf("the page declares %q twice at the top level", name)
			}
			seen[name] = true
		}
	}
}

// v0.2.74: adding a folder to the chosen ones never drops the count to zero.
func TestPageCountKeepsAddingUp(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{`const keepsUp = (path) =>`, `Math.max(c.bytes, floor.bytes)`, `Math.max(c.files, floor.files)`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.77: the count is two lines (label, then numbers) and removing a name
// keeps the last numbers until the new count is done.
func TestPageCountTwoLinesNoDropOnRemove(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{`const fshow = (out, head, bytes, files)`, `const keepsUp = (path) => lastShown && lastShown.path === path;`, `.sizeline { min-height:2.8em;`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, "lastShown.only.every") {
		t.Error("removing a chosen name still restarts the count at zero")
	}
}

// v0.2.78: ⌘A / Ctrl+A ticks everything in the picker's folder, with Shift unticks all.
func TestPagePickerSelectAllKeys(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{`pickTickAll = on =>`, `e.key.toLowerCase() !== "a"`, `pickTickAll(!e.shiftKey)`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.50: Queue under New copy, speed beside the pause button, Now: without
// the file name, no repeated counts, From and To on one line, Verify files
// at the left of the line above the files.
func TestPageCardLayout50(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`.qhead { display:grid;`, `.qhead h2 { grid-row:2; grid-column:1 / -1;`, `<div class="rspeed" hidden></div><div class="acts">`, `"average copy speed"`,
		`line = "Now: " + v.stage +`, `<div class="paths" data-t="proof"><p class="path from"></p><p class="path to"></p></div>`,
		`top.append(c._d.querySelector(".fpage"), bar, el("p", "fhead"))`, `c._d.querySelector(".brow").append(c._d.querySelector(".fpage"))`, `q(".counts").textContent = "";`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `"Now: " + v.file`) || strings.Contains(s, `"average copy speed " + rate(v.copy_rate)].filter`) {
		t.Error("the file name or the speed is still in the old place")
	}
}

// v0.2.50: several folders or files are chosen with ⌘/Ctrl-click (Ctrl-click
// is a right click on a Mac) and Shift-click for a run.
func TestPagePickerMultiSelect(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`e.metaKey || e.ctrlKey || e.shiftKey`, `row.oncontextmenu = e => { if (e.ctrlKey) multi(e, i); };`,
		`b.onclick = e => { if (!multi(e, i)) browse(f.path); };`, `"Tick files or folders (or ⌘-click / Ctrl-click them; Shift-click ticks a run)`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.51: verifying is pink progress on the copy's purple; the file named
// in "Now:" moves with it in the file list.
func TestPageVerifyColoursAndSync(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`.bar.vbar { background:var(--copy); }`, `function syncRow(row, w)`, `row.dataset.p = r.path;`,
		`if (f.live && f.live.file === r.path) syncRow(row, f.live);`, `if (r.dataset.p === lw.file) syncRow(r, lw);`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.53: the Status panel has one bar; the running job's line sits above the
// % line, its speed at the right of the % line in the same font; queue count
// beside the Queue title; no file lines.
func TestPageStatusPanelSlim(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`<h2>Queue<span class="qcount" id="qCount"></span></h2>`, `$("qCount").textContent = [nc ?`,
		`<div class="mcopy" id="monCopy"></div>`, `<div class="pcrow"><b id="monPc"></b><span id="monOf"></span><b id="monRate"></b></div>`,
		`.status.monitor .pcrow #monRate { flex:none; margin-left:auto; }`,
		`.qhead h2 .qcount { color:var(--mute);`, `.status.monitor .pcrow #monOf { flex:1 1 100%; order:3; }`,
		`rate(run.kind === "verify" ? run.rate : run.copy_rate) || "measuring…"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`id="monLines"`, `rows.push(["This file"`, `rows.push(["Files"`, `rows.push(["Average copy speed"`, `[["Queue", [nc`, `"This copy"`} {
		if strings.Contains(s, gone) {
			t.Errorf("Status panel still has %q", gone)
		}
	}
}

// v0.2.54: one bar per copy in two layers, written in purple and proven in
// pink over it, on the job card and on the Status bar.
func TestPageTwoLayerBars(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`.bar > i.pv { position:absolute; left:0; top:0; background:var(--verify); }`, `function provenLayer(bar, part)`,
		`if (layered) mb.style.width = (v.copied_part || 0) * 100 + "%";`, `provenLayer(q(".r .bar"), layered ? v.proven_part : 0);`,
		`provenLayer($("monBar").parentNode,`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `$("monBar").parentNode.classList.toggle("vbar"`) {
		t.Error("the Status bar should use the two layers, not turn pink while verifying")
	}
}

// v0.2.55: every speed is in decimal MB/s, like the speed test and other copy
// tools, and a finished copy's history line keeps its average copy speed.
func TestPageDecimalSpeedsAndCopyRateInHistory(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`const v = n / 1000 ** i;`, `" " + u[i] + "/s"`,
		`(!ver && v.copy_rate ? " · copying " + rate(v.copy_rate) : "")`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `const rate = n => n > 0 ? size(Math.round(n)) + "/s"`) {
		t.Error("speeds still 1024-based")
	}
}

// v0.2.59: the queue item's name, places, speeds under the % line, no file
// bar, and the Now line below the file list.
func TestPageQueueItem59(t *testing.T) {
	page := string(IndexHTML)
	for _, want := range []string{`function jobName(v)`, `" folders"`, `"Destination: " + (at(v.to_disk, v.to_path)`, `"Source: " + (at(v.from_disk, v.from_path)`,
		`<div class="sub"></div><p class="speed mute"></p></div>`, `<div class="tail" data-t="overview"><p class="line"></p></div>`, `insertBefore(box, c._d.querySelector(".tail"))`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`class="bar file"`, `c.classList.toggle("live"`} {
		if strings.Contains(page, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

// v0.2.79: New copy warns before Start when the counted size may not fit.
func TestPageWarnsBeforeStartWhenCopyMayNotFit(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{`id="dstSpace"`, `function spaceWarn()`, `/api/spacecheck?dest=`, `$("dst").addEventListener("input", spaceWarn)`, `spaceWarn();`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.85: the new frame (top bar, sections on the left, status strip), the
// queue order the owner can change, and the On set view of camera cards.
func TestPageNewLookQueueOrderOnSet(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`color-scheme: dark;`, `<header class="topbar">`, `<div class="side"><nav class="tabs" role="tablist"`, `<footer class="strip" id="strip">`, `id="sysBtn">System</button>`,
		`id="styleSw"`, `data-style="onset"`, `id="onset"`, `id="onLayout"`, `data-l="table"`,
		`const SAFE_COPIES = 2;`, `"SAFE TO UNPLUG"`, `"VERIFIED"`, `"KEEP CONNECTED"`, `function driveState(v)`, `k.source >= of`, `k.dest >= of`,
		`"/api/jobs/" + id + "/move"`, `"Run next"`, `function markMovable(list)`, `e.altKey`, `(a.order ?? 0) - (b.order ?? 0)`,
		`$("stripHead").textContent = s.headline;`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, "prefers-color-scheme: light") {
		t.Error("the page has one dark look")
	}
}

// v0.2.86: rows are table rows with a short state; a row's details open in
// the panel on the right, wherever the details element sits.
func TestPageTableRowsAndDetailsPanel(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`<aside class="insp" id="insp" aria-label="Details">`, `function selectJob(id, keepOpen)`, `function stateShort(v, problems, pc, ver, scanning)`,
		`<span class="hn">Name</span>`, `c._d = c.querySelector(".detail");`, `c._d.querySelector(sel.replace(`, `c._d.insertBefore(box, c._d.querySelector(".tail"))`,
		`place($("jobs"), withVerifies(qShow), 1);`, `.jobs { --acts:170px; } .hist { --acts:34px; }`, `e.key === "Escape"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `c.querySelector(".detail").insertBefore`) {
		t.Error("toggleFiles must not look for the details inside the row: they may be in the panel")
	}
}

// v0.2.87: tabs in the details panel, the strip's items, System, sidebar
// disks and the command palette.
func TestPagePanelTabsStripSystemPalette(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="inspTabs"`, `data-tab="overview"`, `data-tab="files"`, `data-tab="proof"`, `data-tab="problems"`, `data-tab="log"`,
		`.ibody[data-tab="log"] .detail > [data-t]:not([data-t="log"])`, `box.dataset.t = "files";`, `function fillOverview(v, q, o)`,
		`"/api/jobs/" + v.id + "/log?since="`, `fetch("/api/log")`, `function loadProblems(c)`, `function renderStrip(s)`, `id="sysDlg"`,
		`id="sideDisks"`, `function loadSideDisks()`, `id="cmdDlg"`, `e.key.toLowerCase() === "k"`, `#headline { display:none !important; }`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
