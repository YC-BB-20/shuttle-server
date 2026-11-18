package shuttle

import (
	"os"
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
		`const listed = v => !v.hidden || qf.showHidden;`, `done.filter(v => listed(v) && shown(v) && inRange(v) && inView(v))`,
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
		`function proofParts(k)`, `const proofLine = k => proofParts(k).join(" · ");`, `v.checks ? proofParts(v.checks)`,
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
		`$("newDirBtn").hidden = !isDst(pickFor)`,
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
		`if (old && !ver && !lnk) btn(btns, "Verify files"`, `ver ? fi.from : [infoAt.id]`, `body.push(...proofSlots(fi));`} {
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
		`!ver && v.copy_rate && "copying " + rate(v.copy_rate)`,
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
		`function SAFE_COPIES()`, `"SAFE TO UNPLUG"`, `"FULLY VERIFIED"`, `card not re-read yet`, `"Leave them failed"`, `"/keep-failed"`, `"KEEP CONNECTED"`, `function driveState(v)`, `k.source >= of`, `k.dest >= of`,
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
		`class="hn" data-sort="name">Name</button>`, `c._d = c.querySelector(".detail");`, `c._d.querySelector(sel.replace(`, `c._d.insertBefore(box, c._d.querySelector(".tail"))`,
		`const qCards = withVerifies(qShow);`, `.jobs { --acts:170px; } .hist { --acts:34px; }`, `e.key !== "Escape"`,
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

// v0.2.88: one card to several drives, the card's panel and wall view.
func TestPageOffloadSeveralDrivesCardPanelWall(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="addDst"`, `function addDstRow(value)`, `const isDst = id => /^dst\d*$/.test(id) || id === "linkDst" || id === "moveTo";`, `for (const dest of dests)`, `"Start " + n + " copies"`,
		`"Offload a card"`, `function selectCard(src)`, `function cardPanel(g, copies, st, word, why, cls)`, `"Source read a second time"`,
		`id="wallBtn"`, `function setWall(on)`, `body.wall .ocard .nm`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.89: the Files section.
func TestPageFiles(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="tabFilesBtn"`, `id="tabFiles"`, `fetch("/api/copies?path="`, `function fbDraw(p)`, `function fbTray()`, `async function fbCopy(onlyMissing)`,
		`$("fbAll").textContent = on.length > 1 ? "Copy to " + on.length + " places" : "Copy";`, `id="fbTwo"`, `function fbOpenFile(p, f)`, `"Read this file again"`, `Copy to another place…</button>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.90: Add link.
func TestPageAddLink(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="linkBtn"`, `id="linkDlg"`, `post("/api/links/info", { urls: text, password: $("linkPw").value })`, `post("/api/links/add", { urls: linkOK, dest, next: $("linkNext").checked, before: queueNext, password: $("linkPw").value, quality:`,
		`document.addEventListener("paste", e =>`, `if (v.kind === "link") return v.link_name || "Download";`, `id === "linkDst"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.91: Vimeo and SoundCloud in Add link, the quick views, the safe count.
func TestPageSitesViewsSafeCount(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="linkPw"`, `type="password" id="linkPw" autocomplete="off"`, `id="linkQ"`, `x.site === "vimeo" ? "Vimeo video" : "SoundCloud track"`, `function linkDur(s)`,
		`id="qView"`, `data-v="active"`, `>Done today <span`, `>Only problems <span`, `function jobProblem(v)`, `function inView(v, view)`,
		`name="safeN"`, `function SAFE_COPIES() { return (copySet && copySet.safe_copies) || 2; }`, `const need = SAFE_COPIES(), short`, `id="onRuleN"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, "const SAFE_COPIES = 2") {
		t.Error("the safe count is still fixed")
	}
}

// v0.2.92: try failed files again, the destination-format question.
func TestPageRetryAndFormat(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`async function addCopy(b)`, `if (!await fmtBox(r.format)) return false;`, `ignore_format: true`, `function fmtBox(f)`,
		`if (!await addCopy(b)) notAdded.push(dest);`, `if (await addCopy(body)) n++;`,
		`post("/api/jobs/" + v.id + "/retry")`, `post("/api/jobs/" + at.id + "/retry", { file: at.i })`, `"Try this file again"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.93: several links are one download, shown as the folders they make;
// a link job lists its files; the column headings sort the lists.
func TestPageLinkGroupAndSortHeads(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`function linkTreeRows(list)`, `function cmpLinkPath(a, b)`, `" files as one download"`, `el("div", "ltree")`, `.ltree {`,
		`function linkChip(r)`, `function linkFolderChip(fr)`, `if (f.v.kind !== "link") { row.tabIndex = 0;`,
		`$("inspTabs").querySelector('[data-tab="files"]').hidden = scanning || !v.files;`,
		`data-sort="kind">Kind</button>`, `data-sort="size">Size</button>`, `data-sort="prog">Written · proven</button>`, `data-sort="status">State</button>`,
		`document.querySelectorAll(".jh button[data-sort]").forEach(b => b.onclick`, `case "kind":`, `case "prog":`, `aria-sort`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `<div class="jh" aria-hidden="true">`) {
		t.Error("the column headings are still hidden from use")
	}
}

// v0.2.94: ⇧-click and ⌘-click select queue rows; the bar pauses, resumes,
// stops (asked twice) or moves them; ⌥↑ / ⌥↓ keep the row's focus.
func TestPageSelectAndBatchMove(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="pickBar"`, `id="pkStop"`, `id="pkUp"`, `id="pkDown"`, `id="pkTop"`, `id="pkEnd"`, `function pickRow(c, e)`, `function drawPicked()`,
		`if ((e.shiftKey || e.metaKey || e.ctrlKey) && c.parentNode === $("jobs")) { pickRow(c, e); return; }`,
		`function planMoves(dir, ids)`, `function moveRows(dir, ids)`, `post("/api/jobs/bulk", { action: "move", moves })`, `post("/api/jobs/bulk", { action, ids })`,
		`function keepFocus(fn)`, `if (!moving) place($("jobs"), qCards, 1);`, `"Really stop " + what + "?"`, `if (picked.size) { picked.clear(); drawPicked(); return; }`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, `c.dataset.mv !== "1" || e.target !== c) return;`) {
		t.Error("⌥↑ still works only while the row itself has the focus")
	}
}

// v0.2.95: a download's row names the site it comes from.
func TestPageLinkSite(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`const kindName = v => v.kind === "verify" ? "Verify" : v.kind === "link" ? v.link_site || "Link" : "Copy";`, `q(".k").textContent = kindName(v);`,
		`v.kind === "link" ? v.link_site || "web" : diskOf(v.source)`, `case "kind": return kindName(v).toLowerCase();`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// v0.2.100: the top bar's order, the card above the queue, scrollable
// filter chips, a finished job's status on separate lines, the Files
// columns and sort, Eject, and Settings in three pages.
func TestPageV0_2_100(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="newBtn">New copy</button>
  <button type="button" id="linkBtn">Add link</button>
  <button type="button" class="cmdk" id="cmdBtn">`,
		`id="qcard"`, `function renderQcard(active)`, `.qhead .qf::before { content:""; margin-left:auto; }`,
		`.job.verify.nested::before`, `subCls += " lines";`, `.dsub.lines, .sub.lines { white-space:pre-line; }`,
		`data-tab="proof">Status</button>`, `["Status", proofScheme(v)]`, `return "Copied, hash generated";`,
		`["created", "Created", ""]];`, `const fbSorted = (list, p) =>`,
		`"Backups (" + have.length + ")"`, `el("h4", "fsec", "Source")`, `.insp:not(.on) { display:none; }`,
		`post("/api/disks/eject", { path: d.path })`, `"slowname"`, `function setPage(pg)`, `id="verLog"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`"Proven copies"`, `What this Verify read`, `"stored when the copy was made"`, `"by this Verify"`, `read back by " + (v.verify_next`} {
		if strings.Contains(s, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
	if strings.Index(s, `id="styleSw"`) < strings.Index(s, `id="live"`) {
		t.Error("the General / On set switch is not at the far right of the top bar")
	}
}

// v0.2.101: a Kind column (sortable), the column chooser on a right-click
// (or a held heading on a phone), columns resized by dragging a heading's
// edge, folder sizes (automatic where cheap, or Calculate sizes), and the
// All / Missing filter gone.
func TestPageV0_2_101(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`const FB_COLS = [["kind", "Kind", ""], ["size", "Size", "r"], ["changed", "Changed", ""], ["created", "Created", ""]];`,
		`k === "kind" ? (f.file ? "1" : "0") + fbKind(f).toLowerCase()`, `head.oncontextmenu = e => { e.preventDefault(); fbColMenu(e.clientX, e.clientY, p); };`,
		`function fbResizer(k, cell, p)`, `p.w[k] = Math.round(Math.max(k === "name" ? 120 : 50, Math.min(900, w0 + m.clientX - x0))); fbTemplate(p);`, `function fbSizes(p, ask)`,
		`fetch("/api/browse/sizes?path=" + encodeURIComponent(path) + (ask ? "" : "&auto=1"), { signal: ac.signal })`, `fbDraw(p); fbSizes(p, false);`,
		`data-p="updates" aria-pressed="false">System</button>`, `<section class="card" id="runSet" data-page="updates">`, `$("runBtns").replaceChildren(...rbtns);`,
		`<section class="card" id="loginSet" data-page="updates" hidden>`, `<section class="card" data-page="updates"><h2>Data folder</h2>`, `<section class="card" id="safeSet" hidden>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`id="fbShow"`, `Missing on a chosen drive`, `fbShowMode`, `<details class="fbcols"`} {
		if strings.Contains(s, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

func TestPageV0_2_102(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`.insp:not(.on) { display:none; }`, `if ((e.key !== "x" && e.key !== "X") || e.metaKey`, `title="Close (Esc or X)"`,
		`<button type="button" class="hq" aria-label="Back to the queue order"></button>`, `.jh .hq { grid-column:1 / 3;`,
		`b.onclick = () => { if (qf.sort !== "queue:1") { qf.sort = "queue:1"; qfChanged(); } }`,
		`.job.verify.nested { margin-left:0; border-left:0; padding-left:calc(14px + 28px + 3px); position:relative; }`,
		`<h2 class="sechead" data-page="updates" style="margin-top:0">How Shuttle is using this computer</h2>`, `<div class="grid resources" data-page="updates"`,
		`#linkDlg .lhint { font-size:12px; margin-right:auto; margin-left:12px; padding-right:16px; min-width:0; }`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// v0.2.114: the resource cards head the System page, before Updates.
	if strings.Index(s, `How Shuttle is using this computer</h2>`) > strings.Index(s, `<section class="card" id="updates" data-page="updates">`) {
		t.Error("resource cards are not at the top of the System page")
	}
}

func TestPageV0_2_104(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`#newDlg { width:min(1040px, calc(100vw - 24px));`, `#newDlg .body { display:grid; grid-template-columns:minmax(0,1.1fr) minmax(0,1fr);`,
		`<div class="ncol">`, `<p class="mute full" id="sysNote"></p>`, `$("sysNote").textContent = c.skip_system_files ? "" :`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`Strongest: the copy read back`, `The source is only read, never changed. Files already at the destination`, `" are left out (Settings)."`} {
		if strings.Contains(s, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

func TestPageV0_2_105(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`.fbnote { margin:0; padding:14px 14px 14px calc(14px + 22px + 12px);`, `el("p", "mute fbnote", words.length ? "Nothing matches." : "This folder is empty.")`,
		`let fbLay = pref("shuttle.fblayout", "1") === "2" ? "2" : "1";`, `function fbSetLayout(n) {`, `if (fbPanes.length) fbSave(); // the layout being left`,
		`keep("shuttle.fblay" + fbLay, JSON.stringify({ panes: fbPanes.map(p => ({ path: p.path || "", cols: [...p.cols].join(","), w: p.w, sort: p.sort })) }));`,
		`(e.key !== "1" && e.key !== "2")`, `e.preventDefault(); fbSetLayout(e.key);`,
		`<form class="newdir fbnew" id="fbNewBox" hidden>`, `post("/api/mkdir", { parent: p.path, name: $("fbNewName").value })`,
		`p.sort = { k, dir: p.sort.k === k ? -p.sort.dir : k === "name" || k === "kind" ? 1 : -1 }; fbSave(); fbDraw(p);`, `function fbColMenu(x, y, p) {`, `function fbResizer(k, cell, p) {`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// A pane's change never reaches the other pane: no shared settings are written any more.
	for _, gone := range []string{`keep("shuttle.fbsort"`, `keep("shuttle.fbw"`, `keep("shuttle.fbcols2"`, `keep("shuttle.fb" + p.i`, `fbPanes.forEach(fbTemplate)`} {
		if strings.Contains(s, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

func TestPageV0_2_106(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`<section class="card" id="delSet">`, `<input type="checkbox" id="delOn">`, `<input type="checkbox" id="delShares">`, `name="delN" value="2"><span>Ask twice (recommended)`,
		`delete_on: $("delOn").checked, delete_shares: $("delOn").checked && $("delShares").checked,`,
		`$("fbDel").hidden = true;`, `<dialog id="delDlg">`, `post("/api/delete", { folder, names })`,
		`if (copySet.delete_confirms !== 1 && delStepN === 1) {`, `$("delGo").disabled = share && !copySet.delete_shares;`,
		`e.key !== "Backspace"`, `function fbRange(p, list, name, on) {`, `if (e.shiftKey && fbRange(p, list, f.name, ck.checked)) return;`,
		`function waitLines(r) {`, `/^Waiting for LucidLink to finish uploading \((.+?) left\) (.+)$/`, `q(".dsub").textContent = v.state === "waiting" ? "" : sub;`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The page splits the server's LucidLink wait into lines: the two must keep the same shape.
	b, err := os.ReadFile("internal/copyjob/lucid.go")
	if err != nil || !strings.Contains(string(b), `"Waiting for LucidLink to finish uploading (" + humanBytes(n) + " left) " + why`) {
		t.Error("the LucidLink wait text changed: update waitLines in the page")
	}
}

func TestPageV0_2_107(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{`#done .job .grip { visibility:hidden !important; }`, `.job.verify.nested.open::before, .job.verify.nested.picked::before { display:none; }`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageV0_2_108(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{`return m ? m[1] + " left" : r;`, `[lucidUp(v.reason) ? "Waiting for LucidLink to upload" : "Waiting", "warn", waitShort(v.reason || "")]`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageV0_2_110(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`<div id="lucidBox" hidden>`, `id="lrCloud" value="cloud"`, `id="lrCache" value="cache"`, `if (!$("lucidBox").hidden) body.lucid_read = $("lrCache").checked ? "cache" : "cloud";`,
		`<section class="card" id="lucidSet">`, `lucid_read: (document.querySelector('input[name="lucidDef"]:checked') || { value: "cloud" }).value`,
		`const onLucid = path => sideDisks.some(d => d.share === "lucidlink"`, `if (v.dest_share === "lucidlink" && v.lucid_read === "cache")`,
		`const lucidUp = r => /^Waiting for LucidLink to (finish uploading \(|upload: )/.test(r || "");`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The State column reads LucidLink's two waits by their wording.
	b, _ := os.ReadFile("internal/copyjob/lucid.go")
	if !strings.Contains(string(b), `"Waiting for LucidLink to upload: " + humanBytes(n) + " waiting;`) {
		t.Error("the LucidLink copy-pause text changed: update lucidUp and waitShort in the page")
	}
}

func TestPageV0_2_111(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`.fbn { justify-self:start; max-width:100%; }`, `const out = o ? [] : (p.copies ? p.copies.places : [])`, `return d && d.kind !== "share" ? d.free : null;`,
		`$("strip").onclick = e => { if (e.target.closest("button, a, input")) return; showTab("status"); };`, `<label>Verify</label>`, `["Verify", fi.proof]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`<label>Proof</label>`, `["Proof", fi.proof]`, `make copies with proof`} {
		if strings.Contains(s, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

func TestPageV0_2_112(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`places = el("select", "fbsrc")`, `srv.value = "__servers"`, `p.title.textContent = fbPanes.length < 2 ? "Files" : p.i === 0 ? "Source" : "Destination";`,
		`const up = bt("↑ Up"`, `const root = bt("⌂ Root"`, `bt("Refresh"`, `bt("+ New folder"`, `bt("Delete…"`, `bar.after($("fbNewBox"))`,
		`function fbFoot(p) {`, `"Σ calculate folder size"`, `" available"`,
		`.fbt { flex:1 1 auto; min-height:0; overflow:auto; } .fbhd { position:sticky; top:0;`, `$("fbDel").hidden = true;`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`id="fbNewDir"`, `id="fbCalc"`, `Press Calculate sizes.`} {
		if strings.Contains(s, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

func TestPageV0_2_113(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`body.on-files .shell > main { display:flex; flex-direction:column; padding-bottom:12px; }`, `body.on-files #tabFiles { flex:1 1 auto; min-height:360px; }`,
		`(f.file ? f.size : p.sizes[f.name] ? p.sizes[f.name].bytes : 0)`, `" folders not sized yet"`, `fbDraw(p); fbTray(); // the ticked folders' total now has their sizes`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageV0_2_114(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{
		`id="fbMove"`, `id="fbAll">Copy</button>`, `post("/api/move", { folder, names, to })`, `if (pickFor === "moveTo") {`,
		`$("undo").textContent = "";`, `return fetch("/api/browse?files=1&path="`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{`id="fbMissing"`, `To undo the last update by hand`, `Starts Shuttle in the background, with its menu bar icon`} {
		if strings.Contains(s, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
}

func TestPageV0_2_115(t *testing.T) {
	s := string(IndexHTML)
	if h, v := strings.Index(s, `<section class="card" id="history" data-page="versions">`), strings.Index(s, `<section class="card" id="verSet" data-page="versions">`); h < 0 || v < 0 || h > v {
		t.Error("Versions: the version history must come first, the installed version and its changes below")
	}
}

func TestPageV0_2_116(t *testing.T) {
	s := string(IndexHTML)
	for _, want := range []string{`(r.records ? " The records of " + r.records`, `(r.records_partial ? " " + r.records_partial`} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
