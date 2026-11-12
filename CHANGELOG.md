# Changelog

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
