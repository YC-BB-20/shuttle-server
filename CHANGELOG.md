# Changelog

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
