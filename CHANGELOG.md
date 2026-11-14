# Changelog

## 0.2.42 — 13 November 2026

v0.2.41 was never installable (one test failed); 0.2.42 includes it.


- **No duplicate filters in a file list.** When every file is in the same group — all verified, say — the All / Verified buttons are replaced by one heading, **"Files verified:"**, above the list (likewise "Files in progress:", "Files not verified:", "Files with problems:"). The buttons come back as soon as the files differ.
- **Every re-read starts from Verify files.** The "Read destination now" and "Read source again now" buttons beside a copy's checks are gone; the ✓/✗ lines stay as the record. Verify files now opens with the right option already chosen from what the copy still lacks: both reads if neither is done, the copy read back if only that is missing, "Source read again only" if only the source is.
- Fixed: hiding a history entry right as its copy finished could be undone by the copy's own last save. A job's saves now take turns.
- **Hide from history is now an ✕** at the right end of a finished row, shown once you open the row. A hidden row shows ↺ there to bring it back.

## 0.2.40 — 13 November 2026

- **Queue header on one line:** "Queue", then the filter buttons and menus, then **New copy** at the right end. On a narrow screen such as the iPhone the filters move to their own line below.

## 0.2.39 — 13 November 2026

- **A copy shows its check as it happens.** While a Verify of a copy runs, the copy's own file list updates by itself: the file being read shows "Verifying", files already checked turn **"Destination verified"** (or **"Fully verified"** when the source was read again too), and files still to check read **"Copied · check queued"** under In progress. The copy's checks say "↻ Checking now: reading the destination back — 3 of 11 files done, all matching so far", and its summary line adds "checking now: 3 of 11 files".
- Clearer labels everywhere: **Destination verified** (the copy was read back and matches), **Fully verified** (the copy read back and the source read a second time, all matching), **Source verified**.
- **Copies and checks look different in the queue.** Copy rows are a little bigger. A Verify row is smaller, set in, with a pink edge, and says which copy it checks: "↳ Checks the copy TestSrc → TestDestFolder · 13 Nov, 21:35".

## 0.2.38 — 13 November 2026

- **Files already at the destination are now named as such.** In a copy's file list they read **"Already there · hash checked"** (or "· read twice" once the source was read again), instead of plain "Verified". File info says the file was not copied, that both files were read in full and that their hashes match. The copy's line reads "N already there, not copied (full read, hashes match)", and while it works it says "Already at the destination: reading it in full to compare".
- **The folder picker keeps one size.** It no longer grows, shrinks or shifts when you open another folder, press New folder or pick a different disk; the buttons at the bottom stay where they are (New folder keeps its place even where it can't be used).
- **The page no longer jumps while a copy runs.** The "Now:" line, the progress line and the disk speeds each keep one line (long names end in "…"; hover for the full text). In the file list every row is one line high: long names end in "…", the status label keeps one width, and the file being copied shows its progress as a thin bar along the row's bottom edge and a percentage in its label ("Copying 29%") instead of an extra line.

## 0.2.37 — 13 November 2026

- **New copy** now sits on the left of the Queue title, a little bigger.
- An empty queue now just says "Nothing in the queue."

## 0.2.36 — 13 November 2026

- **Copy speed is now the average of the whole copy,** shown as "Average copy speed": everything copied so far divided by the time spent copying. It no longer stays on "measuring…" on jobs with many files; it says that only for the first couple of seconds of copying. Time spent reading back or comparing files already there is left out, so it neither lowers the average nor resets it.
- Copy-only jobs no longer show "Copied NaN of 11".
- **File info is wider:** on a wide screen it uses the full width of the app, and it is taller, so long names and paths wrap less. Unchanged on the iPhone.
- New copy: "As each file is copied" now reads **"After each file is copied"**.

## 0.2.35 — 13 November 2026

- **New folder** when choosing where to copy to. In the destination picker, press **New folder**, type a name and press **Make folder**; the picker opens the new folder, ready for **Use this folder**. It only ever makes a new, empty folder: a name that is already there is refused, never replaced. It is not offered on read-only disks or network servers, and Shuttle refuses to make one on a camera card.

## 0.2.34 — 13 November 2026

- New copy: **"Check in a separate queue item"** is shown only when **"After all files are copied"** is chosen. With "As each file is copied" it is hidden and off, since that check happens during the copy itself.

## 0.2.33 — 13 November 2026

- **Disk speed test,** in the spirit of Blackmagic's Disk Speed Test. On the Storage page, **Test speed** on any disk opens two gauges, Write and Read, in MB/s. Shuttle writes one test file of 1, 2, 5 or 10 GB at the top of the disk (data that cannot be compressed), makes sure the disk has really stored it, reads it back with the Mac's cache bypassed, and repeats until you press Stop (at most 10 minutes). The test file is always removed. It shows the best so far and what the speed means: how long 1 TB takes to write and to read.
- **A camera card or read-only disk is only read:** its test reads files already on it and writes nothing.
- A disk Shuttle is copying to or from is not tested until that job is paused. The last result of each disk is kept and shown on its Storage card.

## 0.2.32 — 13 November 2026

- **Storage page.** A new Storage tab lists this Mac's own disk and every connected disk: how it is connected (USB with its speed and USB generation, Thunderbolt, internal, SD card slot), the drive's name, file system, solid state or spinning, free space, read-only disks, and whether it looks like a camera card. It shows what Shuttle is doing with each disk right now, and has **Copy from here** and **Copy to here** buttons that open New copy with the disk filled in. A disk on a slow USB 2 link is marked. The page only reads; nothing is written to any disk.

## 0.2.31 — 13 November 2026

- **A copy keeps up with later Verify jobs.** A copy made without checks, then verified later, now says so on its own row: "destination read back ✓ · source not read again", and once a source check has run too, "source read twice ✓". Its file list changes with it: files go from "Copied · not verified" to "Verified", then "Verified · read twice"; a file a later check found damaged shows as Error.
- **A Verify says which kind it is:** "Destination verification", "Source verification" or "Destination + source verification", and while it runs, what it is reading now.
- **Disk names on every row,** beside each folder name ("TEST2 · BB2"; "this Mac" for the internal disk).

## 0.2.30 — 13 November 2026

- **More room around the Queue filters.** Space above the filter row and a little more between the buttons.
- **Hide entries from the history.** Open a finished or stopped entry and press Hide from history. Hiding only tidies the list: the copy, its files and its records are untouched, and Show in history brings it back.
- **Seeing hidden entries:** a Hidden button with a count appears among the filters, and Settings → History has the same switch, "Show hidden entries in the history". Hidden entries shown that way are dimmed and marked "hidden".

## 0.2.29 — 13 November 2026

- **Verify from a row checks that copy only.** It used to take in every copy ever made into the same folder, so a folder that had received copies from different sources refused to read "the source" again. Now Verify files, Read destination now and Read source again now on a copy check just that copy, against its own source. A Verify item's "Verify files again" checks the same copies it did before.
- **Filters with nothing in them are hidden** in a job's file list (All always shows, and so does the one you have chosen).

## 0.2.28 — 13 November 2026

- **The Verify pop-up is easier to read.** Each choice has a larger name with a short explanation under it in smaller grey text. The texts are now: **Destinations only** (read every copy back from the destination and compare it with the source file's hash), **Destinations, then the source read again** (catches a bad read from the card or cable; the source disk must be connected), **Source read again only**, and **Add job as paused**.
- **The pop-up is titled "Verify files again"** once a copy has had any check; before that it reads "Verify files".

## 0.2.27 — 13 November 2026

- **The row button is now "Verify files"**, on copies and on Verify items alike. Its pop-up adds a third choice: **Source read again only**, which compares the source with the hashes stored when it was copied, without reading the copies.
- **A finished copy says what has been checked.** Its details show two lines: whether the destination was read back, and whether the source was read a second time, each with how many files match and when — counting the copy itself and every Verify run over it since. A check that is missing or incomplete has a button beside it to do it now (**Read destination now**, **Read source again now**). While a check of the copy is in the queue, the buttons wait.

## 0.2.26 — 13 November 2026

- **"Verified" in a file list counts only files whose copy was read back.** A copy-only job used to show every file under Verified while each row said "Copied · not verified". Those files now sit under a new **Not verified** filter, shown only when there are some.
- **Verify copy.** A finished copy that was never read back now has **Verify copy** on its row instead of "Verify again". Both open a pop-up: check the **destinations only**, or the destinations **then the source read again after the whole job**, and optionally **Add paused**.
- **A Verify with a missing folder stops and shows failed,** naming the source or destination folder that is missing. Unlike a copy it does not wait; nothing is changed, and you can verify again once the folder is back.

## 0.2.25 — 13 November 2026

- **Purple is the main colour.** Buttons, chosen filters, bars and links that were blue are now purple, in light and dark mode, and so is the page's icon.
- **"Showing 2 of 20" has more room** below the filter buttons.
- **The Verify… button above the queue is gone.** To check a finished copy again, use **Verify again** on its row.

## 0.2.24 — 13 November 2026

- **Free space is checked before a copy.** When you add a copy, Shuttle counts what it will write (files already at the destination with the same size are left out) and adds what every unfinished copy already queued to the same disk still has to write: running, waiting, queued or paused. If the new copy does not fit on its own, or fits only without the queue ahead of it, a pop-up gives the numbers and asks: **Continue anyway** adds it, **Cancel job** adds nothing. The check runs again when each copy starts, since the disk may have filled up while it waited; a copy that no longer fits is paused before writing anything, with the same question on screen and on its row. A very large folder that takes more than a few seconds to count is checked when it starts instead. On the internal disk the free-space floor is kept free too.

## 0.2.23 — 13 November 2026

- **The menu bar icon no longer disappears after the Mac sleeps.** On waking, the icon could think Shuttle had gone and quit; three sleeps within ten minutes made Shuttle stop restarting it until Shuttle itself was restarted. The icon now quits only when Shuttle has really stopped, and an icon that ran for a while before quitting is always started again.

## 0.2.22 — 13 November 2026

- **System files can be left out.** A new "System files" card in Settings: "Don't copy system files" (on by default) leaves out .DS_Store, .Spotlight-V100, .Trashes, .fseventsd, .TemporaryItems, .DocumentRevisions-V100, Thumbs.db and desktop.ini. Finder rewrites .DS_Store whenever a folder is opened, so a copy of one could never stay verified. A second switch, "Also leave out ._ files" (off by default), leaves out the "._" files macOS makes on non-Mac disks. Left-out files are counted on the copy ("system files left out: N"), never hidden. The choice applies to new copies and new verifies, so a Verify of an older copy now passes over a .DS_Store that Finder changed. The New copy window says which rule is on.

## 0.2.21 — 13 November 2026

- **Queue and History have filters, like Shuttle Router.** Buttons with counts show or hide Completed, Skipped (every file was already there), Stopped, Failed and Paused items; "Active only" switches them all off and "Show all" back on. A time range (last hour, today, yesterday, last 7 days) narrows the history, and a sort menu orders both lists (queue order, newest, oldest, name, size, status, time left, destination). Running and waiting items always show. "Showing N of M" says when something is hidden. The choices are remembered in this browser.

## 0.2.20 — 13 November 2026

- **Copies and verifies look different in the queue.** A copy is purple and a verify is pink while it runs or waits its turn: its icon, its progress bar and the file chips ("Copying" purple, "Verifying" pink). A verify's icon is ↻. When an item finishes it turns green; a problem turns it red (finishing with failures was orange before).

## 0.2.19 — 13 November 2026

- **The menu bar menu stays open when you click "Check for updates" or "Install update".** Only the item's text changes ("Checking for updates…", "Installing update…"); the menu no longer closes and opens again.
- **File info of a Verify item that reads the source has a "Read the source again" button** under the source's fingerprint, like "Read the copy again".

## 0.2.18 — 13 November 2026

- **Stopping a copy stops its Verify item too.** It had read nothing yet, so nothing is lost.
- **An open page reloads itself after an update,** so it never shows the old page with the new Shuttle behind it (it waits while a pop-up is open). Earlier fixes, such as the always-visible Start copy and Cancel buttons, only appeared after reloading the page by hand.
- **The queue line counts copies and verifies separately:** "1 copy, 1 verify".
- **A Verify item that has not started yet counts with its copy's size** in the whole-queue total, instead of "(+1 not measured yet)". Any item still listing its files now says "(+1 item not sized yet: still listing its files)".
- The panel says "This verify" while a Verify item runs.

## 0.2.17 — 12 November 2026

- **A file being checked shows an orange "Verifying" chip** instead of "Working"; while it is being copied the chip says "Copying" (green).
- **Plain words for what is being read:** "Reading back the destination and the source", "Reading back the destination", "Reading back the source". The D1, S1 and S2 codes are gone from these lines.

## 0.2.16 — 12 November 2026

- **Files get their real names as soon as they are copied.** A file is "clip.mov.shuttle-part" only while it is being written; the moment its copy is written and flushed it is "clip.mov", whichever check you chose, and the check follows. Shuttle's file list still says which files are verified. If a check fails, that copy loses the real name again, is copied once more, and if it still fails it is kept aside as "clip.mov.shuttle-failed-copy". Nothing is deleted, and Stop keeps copied files under their names (marked not verified).
- **The separate Verify item is in the queue from the start,** right under its copy, saying "Starts when the copy above has finished" (or paused, if you chose that). It never runs beside its copy. Stopping the copy pauses its Verify item, so you can still check the files copied so far.
- **The status line shows copied and verified:** "Copying — 2 of 3 files copied, 1 verified" (in the menu bar too).
- **New copy: Start copy and Cancel are always visible;** only the options above them scroll.
- **Menu bar: "Check for updates" looks like the other items,** in its own section. After a click the menu opens again by itself, so "Checking for updates…" shows in place.

## 0.2.15 — 12 November 2026

- **Simpler proof choices in New copy: three boxes, each adding to the one above.** "Copy, hashing the source as it is read" (always on); "Verify the destination: read each copy back"; "Read the source again: catches a bad read from the card or cable". All three are ticked the first time; after that the form remembers your last choice. A line under the boxes says in plain words what the choice means. Copy only is the fastest, but nothing checks the copy, so it is not for cards you will erase.
- **When to check:** "As each file is copied" (the default) or "After all files are copied", or **"Check in a separate queue item"**: the copy finishes first and its files get their real names straight away, then a Verify item reads the copies back (and the source again, if ticked). That item can be **added paused**, to start when you choose. If the source reads differently from what was copied, the Verify item marks the file as an error ("the copy may hold a bad read") and changes nothing.
- The option to read the source on its own before the copy is no longer offered for new copies. Copies already made with it keep working and show as before.
- **The update button sits next to the title** ("Update to 0.2.16"): one click installs, and it says "Checking…" or "Installing…" in place while it works. The line under the status panel is gone.

## 0.2.14 — 12 November 2026

- **Read any fingerprint again.** In a file's info, each fingerprint line has a button: "Read the source again" or "Read the copy again". Shuttle reads that file in full once more and adds the new fingerprint below with its date, marked "same as the first" or "differs" (and says so if the source has changed since it was copied). It shows its progress, waits for a running copy to finish so a disk is never asked for two things at once, and only reads: nothing is changed.
- **Memory, buffer budget, free space, processor and open files moved to Settings,** under "How Shuttle is using this computer". The Status page now shows only the copies. On an iPhone the tiles sit two to a row.

## 0.2.13 — 12 November 2026

- **You can see a copy arrive.** A file being copied used to be hidden (its name started with a dot) until it was proven, so the destination folders looked empty in Finder while a copy ran. It is now visible as "clip.mov.shuttle-part" while it is written and checked, and gets its real name, "clip.mov", the moment it passes. An unproven file still never has its real name. A copy paused under an earlier version picks up its hidden part file and carries on.

## 0.2.12 — 12 November 2026

- **Copy speed, in big type.** The progress panel and a running copy's details now show the speed of the copy itself ("Copy speed 379 MB/s"), counting only the time spent copying, instead of an average over copying and checking. The source and destination disk readings are unchanged.
- **File counts:** "Copied X of N · Checked X of N · Completed X of N". Copied: the copy pass is done. Checked: the copy has been read back and the source read again. Completed: passed its check and has its real name; files already at the destination that were compared and match count too.
- Time left still counts the checking, so it stays honest.

## 0.2.11 — 12 November 2026

- **Menu bar: the menu stays open when you press Check for updates or Install update.** The item changes in place to "Checking for updates…", "Downloading update…" or "Installing update…", then to "Shuttle is up to date", the update found, or what went wrong, while you watch. "Set up updates…" still closes the menu, since it opens Shuttle.

## 0.2.10 — 12 November 2026

- **File info names the reads correctly for files that were already at the destination.** Those files are not copied: the source is read in full and compared with the file already there, and the fingerprints now say so instead of "before the copy". For copies made with "Read the source on its own first", the line now reads "Source — full read on its own, before the copy".

## 0.2.9 — 12 November 2026

- **Verify, any time.** The new Verify… button (next to New copy) checks any folder or single file Shuttle copied to: open the folder and press Use this folder, or tap one file. Each file is read again in full and compared with the hash stored when it was copied. It is its own item in the queue, with pause, resume and stop, and waits by itself if the disk is unplugged. It only reads: nothing is changed, renamed or deleted.
- **Verify again** on every finished copy and verify in History.
- **Clear reasons when a file does not match:** "missing", "wrong size" or "differs". A difference is read a second time before it is believed. Files the copy never verified are counted as "no stored hash", not checked.
- **File info is easier to read.** The hashes are now "fingerprints", each line says which full read it came from (the source as it was copied, the destination copy, the source read again after the copy as a safety check), and each one says "same as the first" or "differs".
- **A copy with one file shows no filters,** just the file.
- **Menu bar:** pressing Check for updates shows "Checking for updates…", then "Shuttle is up to date" or the update found; pressing Install shows "Installing update…" (and "Downloading update…" while it downloads). A failed check or install says so.

## 0.2.8 — 12 November 2026

- **A missing disk is an error you can see.** If the source or destination disk is unplugged (or its folder disappears) during a copy, the top of the page turns red and names it: "Source disk not connected: CARD_A001". The copy waits, no file is marked failed, and it carries on by itself once the disk is back. The menu bar icon turns red too. Shuttle now notices within about two seconds, even between files.
- **One progress panel on top.** A big bar for the whole queue, with how much is done and the time left for all of it, plus lines for the copy running now and the file in it, each with its own bar and time left.
- **Queue and History look alike, like the Files app.** Each copy is one thin row: an icon for its state, source → destination (both names the same size), a thin bar and one status line, with pause and stop as small round buttons. Tap a row for its details and files. History rows show when, how long, the size and the average speed.
- **File filters with counts:** All, In progress, Verified and Error, each with how many files it holds, for example "In progress (32)".
- **New copy remembers the last one.** It opens with the last source, destination and proof choice, so Choose… starts where you were.
- **Macintosh HD shows your home folder,** not the system's folders above it. The separate Home button is gone.
- **New proof choice: copy everything first, then check.** Every file is copied, hashed as it is read, and keeps a temporary name; then a second pass reads each copy back and the source again, and names each copy once all three agree. A copy that does not match is copied again once. Stopping removes the copies that were never checked.
- **"Proven" is now "Verified"** everywhere you read it.
- **Menu bar:** Open Shuttle, Restart Shuttle, Stop Shuttle. The page has an icon in the browser tab.

## 0.2.7 — 12 November 2026

- **Progress, speed and time left.** Every copy shows how far it is as a percentage with a large bar, the time left, and its speed: overall, and what the source and destination disks are doing right now. The percentage counts all the work, the copy and the reading back, so it moves steadily rather than jumping when a big file is proven. Finished copies show their size and average speed.
- **Tap a copy to see its files.** The Files button is gone: tapping a copy opens its file list with All, Still to do, Done and Problems; tapping again closes it.
- **File info.** Tapping a file opens a window with everything about it: name, folder, size, modified date, where it came from and where it went, whether it is at the destination, its proof, every hash (read for the copy, read back, source read again) with a tick where they match, and what happened to it, when, and how long it took. While a copy runs, the window updates by itself.
- Each file's record now also keeps how long it took and whether it had to be copied twice.

## 0.2.6 — 12 November 2026

- **Picker: places on top.** Home, this Mac's own disk, and every disk and card that is connected right now, each with its free space. A folder left behind by an unplugged disk is not shown. Network servers appear there too, greyed: copies to or from a server come later.
- **Picker: the path is buttons.** Tap any folder in the path (Macintosh HD › Users › bb › Desktop) to jump straight there. On a disk, the path starts at the disk's name.
- **Copy only some files.** When choosing what to copy, files are listed too. Tick any files and folders in a folder to copy only those; or press Use this folder to copy all of it.
- **Queue and History.** Running, waiting and paused copies are under Queue; finished and stopped ones under History, with when they finished and how long they took. Show older copies loads more.
- **Every file of a copy.** Press Files on any copy to see each file with its size and state: waiting, being worked on (with its own bar), proven, proven twice, failed (with the reason) or a source warning. Filters: All, Still to do, Done, Problems. The list updates by itself while the copy runs.

## 0.2.5 — 12 November 2026

- **Settings tab.** The page now has Status and Settings at the top. Settings holds the data folder, updates (with Restart and Quit), and the new version history. When an update is available or an update step is running, Status shows a one-line note with a button to Settings.
- **Version history.** Lists the recent green builds and every version kept on this Mac. Any of them can be installed with one click. It is checked exactly like an update: it must start and name its build before it replaces anything, and the version it replaces is kept. Versions kept on this Mac install without downloading, and are listed even without the GitHub key or a connection.
- The five status cards (memory, buffers, free space, processor, open files) sit in one row. On a phone the row scrolls sideways by itself; the page does not.

## 0.2.4 — 12 November 2026

- Fixed: **Stop did nothing** after its two questions. The second question was answered "no" by the first one closing a moment late. Each question now waits for the one before to close.
- Fixed: stopping a copy that was paused, or waiting for a disk, never finished. It now stops straight away, keeps every proven file, and removes the half-written copy of the file in progress, also after Shuttle was restarted in between.

## 0.2.3 — 12 November 2026

- **Hash while copying, now the default.** The bytes read from the source for the copy are hashed at the same moment they are written, instead of in a separate read before. Then the copy is read back from the destination while the source is read a second time, both at once, one on each disk. A file gets its real name only when all three agree. The source is read twice instead of three times, and the whole job takes about two passes instead of three, with the same strongest proof.
- Because a faulty cable or card could make the copy and its hash wrong in the same way, the second source read is always on in this mode. If it disagrees, the file is copied again; if it still disagrees, the copy is kept aside as `.shuttle-failed-copy` and the file is reported, never given its real name.
- If the source itself changed after the copy (not a copy error), the copy keeps its name as a proven copy of what was read, with a warning.
- The older scheme (read the source on its own first) is still offered in New copy, and copies started before 0.2.3 carry on with it.
- Pausing, power cuts and crashes during the copy keep the hash's place too, so a resumed copy is still hashed from where it stopped.

## 0.2.2 — 12 November 2026

- Fixed: the menu bar icon did not appear when Shuttle was started from the Shuttle Server app. The app started it with `nohup`, which on macOS moves a program out of your login session, where it cannot show icons. The app now starts it normally, and Shuttle rewrites the app in Applications by itself when the app is out of date, so this fix arrives with the update.
- **Quit Shuttle** button on the page, next to Restart, for when the menu bar is not available.

## 0.2.1 — 12 November 2026

- **One-click updates.** The menu bar icon and the page's Updates card check GitHub for the newest green build, download it, make sure it starts and reports the right commit, keep the current version as a backup, swap the new one in and restart. If a copy is running, the update waits until it finishes (or installs now if you choose; the copy pauses and continues). Checks run every six hours.
- Needs a read-only GitHub access key, added once on the page. It is stored only on this Mac in a file only you can read, sent only to GitHub, and never shown, logged or returned.
- **Go back to the previous version** with one button; every replaced version is kept in `bin/backups`. The page also shows a one-line Terminal command that undoes the last update by hand.
- **Restart Shuttle Server** in the menu bar and on the page. Shuttle stops cleanly (copies save their place) and starts again in the same process; copies continue where they were.
- Quitting now waits for running copies to save their place before the program ends.

## 0.2.0 — 11 November 2026

- **Disk-to-disk copy with proof.** New copy on the page: pick a source folder (a card, a drive) and a destination. Every file is read and hashed first (S1), copied at disk speed with no hashing in the copy, flushed to the drive, read back and hashed (D1). Only when S1 equals D1 does the copy get its real name; until then it has a hidden temporary name. Optionally the source is read a third time at the end (S2) for double evidence.
- While one file is read back from the destination, the next is already being read from the source, so neither disk idles.
- The source is only ever opened read-only. Files already at the destination are compared and never replaced; a different file with the same name is reported and left alone.
- A copy that does not match is read again, then copied once more; if it still differs, the file is marked failed and the bad copy is kept beside it as `.shuttle-failed-copy`. Nothing is deleted.
- **Pause is immediate**: a copy keeps what it wrote and continues from there; a hash saves its place (the hasher's state) and continues mid-file. Stop asks twice and keeps everything already proven.
- **Survives crashes and power cuts**: an append-only journal, flushed in groups, plus checkpoints every gigabyte. Tested by killing the program mid-copy three times.
- A disk that stops answering for 60 seconds is marked not responding and no more work is sent to it; an unplugged disk makes the job wait and carry on when it is back. Shuttle never creates folders under `/Volumes` for a disk that is not connected.
- XXH128 written in plain Go, checked against all 4,161 official test vectors and against `xxhsum -H2` on every release.
- `shuttle-server hash FILE...` prints the same hashes as `xxhsum -H2`, so you can check any file by hand.
- `shuttle-server install-launcher` makes "Shuttle Server.app" in your Applications folder: double-click to start Shuttle in the background and open the page.
- `shuttle-server soak -dir FOLDER` runs copies for an hour (or longer) and records memory, threads and open files.

## 0.1.1 — 11 November 2026

- An icon in the Mac's menu bar while Shuttle Server runs: arrows when all is well, a warning triangle when something is waiting, a stop sign in safe mode. Its menu shows the current status, opens the page, and quits Shuttle Server.
- The icon is run by macOS's own scripting, so nothing is installed. It disappears when Shuttle Server stops, and quits by itself if Shuttle Server is gone. Turn it off with `"menu_bar_icon": false` in `config.json`.

## 0.1.0 — 11 November 2026

- First release: the skeleton. Nothing moves files yet.
- Runs as a plain program on this computer only (localhost), with one data folder holding settings, state, logs, records and temp.
- Settings file `config.json`, written with defaults on first start.
- Built-in limits so a fault can never freeze the computer: one fixed memory buffer budget, a memory watchdog, half the cores at a lower priority, page updates coalesced to a few a second, and safe mode after three crashed starts in five minutes.
- The page shows the version, what it is doing, the limits in use, free space against the floor, and this changelog in About.
