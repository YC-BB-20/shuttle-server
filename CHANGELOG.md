# Changelog

## 0.2.112 — 17 November 2026

- **Files looks like the router's:** each pane has its title (Source and Destination with two panes) and, on the right, a drop-down of every drive and share plus "Add or edit servers…"; then the path; then Up, Root, Refresh, New folder and Delete; and a footer with the counts, "Σ calculate folder size" and the space available. New folder opens inside its pane.
- **Big folders scroll inside their pane:** the panes fill the window, the list scrolls, and its column headings stay in view.
- **A share that macOS reconnects at another place** ("Share-1", or one folder deeper for LucidLink) is followed: a copy or Verify waiting for it carries on there, and the log says where it moved. Only the same server and share counts.

## 0.2.111 — 17 November 2026

- **The Mac stays awake while the queue has work** (running, waiting for a disk or for LucidLink's upload, or next in line), using macOS's own caffeinate; it may sleep again once the queue is idle. Closing a laptop's lid still sleeps it.
- **Files, two panes:** clicking beside a name ticks the row, as in one pane (the name is only as wide as its text); copy goes to the other pane only, without the boxes for earlier copies' places.
- **Copy-to boxes** name the folder and its drive, and give no "free after" for LucidLink or a share (LucidLink reports 1024 TB).
- **Clicking the status bar** at the bottom opens the queue.
- "Proof" is called "Verify" in the page.

## 0.2.110 — 17 November 2026

- **Copies to LucidLink: choose how the copy is read back.** In New copy (shown when a destination is on LucidLink), with a default in Settings → Copies to LucidLink:
  - **Download it from LucidLink once uploaded** (the default, as before): proves the copy in the cloud, but downloads it all once.
  - **Read LucidLink's cache on this Mac, then wait for the upload:** no download. The copy is read back at once from the cache, and the copy or Verify finishes only when LucidLink says nothing is left to upload. The copy's notes say plainly that the cloud copy itself was not read.
- **The State column names LucidLink's wait:** "Waiting for LucidLink to upload", with how much is left under it.
- **The top box keeps each drive's speed** steady: it is measured over the last 15 seconds, so a slow LucidLink upload no longer makes it disappear and come back.

## 0.2.109 — 17 November 2026

- **A Verify that has not started reading yet** (for example while it waits for LucidLink's upload) now shows, for each file, the hash the copy made of the source when it was copied. It said "No hash was stored for this file", which was wrong.

## 0.2.108 — 17 November 2026

- Under "Waiting" in the State column, a copy to LucidLink shows how much is left to upload ("2.5 GB left to upload").

## 0.2.107 — 17 November 2026

- A selected Verify under its copy has one purple edge, not two.
- History never shows the drag dots (only the queue can be reordered).

## 0.2.106 — 17 November 2026

- **Delete, in Files.** Off until you turn it on in Settings → Delete. Tick items and press Delete… (or ⌘⌫). They go to the Trash of their disk, so you can get them back. On LucidLink and SMB shares there is no Trash, so it deletes for good, and needs its own second switch in Settings. Shuttle asks once or twice first (Settings; twice by default), says how much will go, and warns in red when it knows of no proven copy elsewhere. Nothing a copy in the queue still uses, no whole disk, no home folder and nothing of Shuttle's own can be deleted, and if any one item may not go, none is touched. Every delete is written to the log.
- **Shift-click** ticks or unticks everything between the last item clicked and this one, as in the Finder. Clicking a row (not its name) ticks it too.
- **A wait is told once,** at the top of the details, in short lines: "Waiting for LucidLink · 2.5 GB left to upload · before reading the copies back".

## 0.2.105 — 17 November 2026

- **Files: New folder.** Makes a folder in the folder shown (in the pane you last clicked). A name already there is refused; nothing is replaced.
- **Files: keys 1 and 2** switch between one and two panes (not while typing).
- **Each layout and each pane keeps its own view.** One pane and two panes remember their own folders, columns, widths and sort, and in two panes the left and right are separate: a change on one side never changes the other. All of it is saved as it changes, so switching or reloading brings it back as you left it.
- "This folder is empty." lines up with the Name column.

## 0.2.104 — 17 November 2026

- **New copy is wider and does not scroll:** where from and to on the left, proof and folder layout on the right. On a phone it stays one column.
- Fewer words in New copy: the "Strongest…", "The source is only read…" and "System files … are left out" lines are gone. A note still appears if system files are set to be copied.

## 0.2.103 — 17 November 2026

- **LucidLink copies can now be checked.** LucidLink's command on a Mac prints a small table and a new row every 2 seconds without stopping; Shuttle waited for it to finish and gave up. It now reads the first row ("0B" means everything is uploaded) and stops the command. It never takes a timestamp or anything unclear for the amount.
- **Copies onto LucidLink or an SMB share show real progress.** macOS took the whole file into memory at once (100% straight away) and sent it on only at the end, with nothing shown. Shuttle now sends it on every 64 MB, so the bar follows what has really arrived.
- **LucidLink no longer flashes in the list of disks.** While it is busy it can take more than a second to answer; Shuttle shows it as it last was (for up to two minutes) instead of dropping it. A disk that stops answering still shows "Not responding" at once.

## 0.2.102 — 17 November 2026

- **LucidLink filespaces now show up.** The LucidLink app on a Mac mounts a filespace one folder deeper than a disk (/Volumes/NAME/FILESPACE), as a share served by the Mac itself. Shuttle now finds it in Files and Disks and treats it as LucidLink, whatever the filespace is called, so copies to it wait for the upload before reading back.
- A copy to or from a filespace no longer counts it as "not connected" because of that extra folder.
- **The details panel is hidden until you click a copy** (or a file in Files), so the list has the full width. ✕, Esc, the X key or clicking the copy again closes it.
- **Click the heading area left of Name to go back to the queue order** after sorting by a column. A ⇅ shows there while the list is sorted another way.
- **A Verify under its copy, when selected, has its purple edge at the list's left edge** like every other row, not set in.
- Add link: more room between "Paste a link anywhere…" and Cancel.
- **Settings → System** now holds "How Shuttle is using this computer" (memory, buffers, free space, processor, open files); Copy settings no longer shows it.

## 0.2.101 — 17 November 2026

- **Files: a Kind column** (Folder, MOV file, …) that sorts like the others.
- **Right-click a column heading to choose the columns** (hold a heading on a phone). The Columns button is gone.
- **Columns resize** by dragging a heading's right edge; double-click the edge, or "Usual widths" in the menu, to go back. Widths are remembered.
- **Folder sizes.** Worked out by themselves where it costs little: on a solid-state disk that no copy is using, up to 100,000 entries or 4 seconds. On a spinning disk, a network share or the cloud, or while a copy runs on that disk, press **Calculate sizes** (up to 12 seconds). One count at a time, never onto another disk, links not followed; a count cut short shows "≥". Sort by size includes folders once their sizes are known.
- **The All / Missing on a chosen drive filter is gone.** "Copy only what is missing" in the tick tray still copies only what a drive lacks.
- **Settings → System** (was Updates): Updates, then Stop or restart in its own card, Start at login and the Data folder.
- **"On set: copies needed" is hidden** until the On set view is redesigned; the number already saved (2 unless changed) still applies.

## 0.2.100 — 17 November 2026

- **A card above the queue, always shown.** How many items are in the queue, the job running now with its percent and time left, and the time left for the whole queue. Under it, one line for each disk the queued jobs use: its read or write speed and what it is doing (copying, reading back, waiting, not connected…).
- **Top bar:** New copy, then Add link, then the search field, next to the logo; the General / On set switch is at the far right.
- **Queue:** the filter chips scroll sideways when they do not fit (some were hidden off the left edge). A Verify's purple line runs through the centre of its copy's icon. A finished job's status shows each part on its own line (the copy speed, the read-back, the second source read, a queued check). "Proof" is now "Status"; a copy without checks says "Copied, hash generated".
- **Files:** the "Proven copies" column is gone. Click a heading to sort by name, size, changed or created; Columns chooses which show, with a new Created date (macOS). The pane switch comes first; the name filter and All / Missing sit on the right. With no file open, the list takes the full width. A file's panel shows its Source first, then "Backups (n)", which folds, grouped by drive or cloud. The file record no longer has the three Verify notes.
- **Disks:** one under another, each with an Eject button (refused while a job in the queue uses the disk). A slowed-down drive's name is orange; click it for what the drive can do and what holds it back. "(its USB controller)" is gone.
- **Settings** in three pages: Copy settings, Updates, Versions (the installed version, what changed, and older versions).
- **LucidLink as a source (owner's decision):** the second read of the source reads LucidLink's cache as it is, never dropping it or downloading again; the page says so. A local source is always read a second time from the disk.
- On a phone, the sections bar sits in one row again.

## 0.2.99 — 17 November 2026

- **A copy that ends with failed files asks what to do.** It no longer slips into the history: it stays in the queue as "Needs your answer", says how many files failed and the most common reason, and offers "Try the failed files again" or "Leave them failed". Only that copy waits; the rest of the queue carries on, overnight too. Its Verify item waits for the answer. Nothing on disk changes either way, and the question survives a restart.
- **Safe and Fully verified.** A card or copy whose copies are read back shows "Safe" with a short note that the card (or source) has not been read a second time yet; it does not stop you unplugging. Once it has been read again and matched, it shows "Fully verified" (it said "Verified").

## 0.2.98 — 17 November 2026

- **Copies to LucidLink.** The LucidLink mount can now be a copy's destination, for example from a CTERA share. Every file is copied and hashed first. The copies are read back only once LucidLink says nothing written on this Mac is still waiting to upload, and after its cache is cleared, so the read-back comes from LucidLink's storage and not from this Mac. Until then they count as written, not proven; a read-back from this Mac's cache would prove nothing about the upload.
- While copying, Shuttle asks LucidLink how much is waiting to upload and pauses between files when it passes 4 GB (setting `lucid_max_waiting_gb`), saying "Waiting for LucidLink to upload".
- If Shuttle cannot find LucidLink's command, or cannot read its answer, the copying goes on but the check waits and says why; it never guesses that the upload is done. The command is looked for as `lucid3` or `lucid`, in the usual places and inside the LucidLink app; `lucid_command` in the config can name it.
- A Verify of a place on LucidLink waits the same way.
- A running job that is held now shows as waiting, with the reason.
- Releases: signing the Mac builds tries again when Apple's time-stamp server does not answer.

## 0.2.97 — 17 November 2026

- **Copies with network shares.** An SMB share mounted in Finder (Go → Connect to Server, `smb://…`), such as a CTERA Edge Filer, can now be a copy's source or destination, and so can the LucidLink mount as a source. The copy works as between disks: hashed as it is read, flushed, read back from the share and the source read a second time, a cut-off copy continues where it stopped, nothing is ever overwritten.
- What the check proves is said plainly in the copy's Proof tab: the copy was read back from the share. A CTERA Edge Filer uploads to the CTERA cloud by itself afterwards, which Shuttle cannot see, so check the filer's sync status before deleting the source. A second read of a LucidLink source may come from LucidLink's cache on this Mac.
- Names an SMB server may refuse (`: * ? " < > |`) and names that differ only by case are counted and asked about before the copy starts, as for exFAT.
- A share that macOS reconnects is still recognised as the same place, so a copy waiting for it carries on. Other network servers (NFS, AFP, WebDAV mounts) are still not used for copies.
- Copies to LucidLink come in the next version.

## 0.2.96 — 17 November 2026

- **Servers, the first step toward copies between LucidLink and CTERA.** Settings → Servers adds a cloud server such as CTERA, reached with S3 keys (a file cut off part-way continues where it stopped) or with WebDAV and your user name and password (a cut-off file starts again). Test looks at the top folder without changing anything and says, in plain words, whether it connected and what it found, or why not: wrong login, unknown bucket, an address that is not https, a certificate this Mac does not trust, no answer.
- The login is kept only on this Mac, in a file only you can read. The page never gets the password or secret key back; it shows only the user name, or the last four characters of an S3 key. Changing a server's name keeps the saved login.
- Underneath, the parts the copies will use are built and tested: uploads in pieces with each piece checked by the server, continuing after a cut-off from the pieces the server confirms it has, reading back from any point, never replacing a file already on the server, a time limit on every request, and a transfer cut off when no data moves for two minutes. Copies to and from servers come next.

## 0.2.95 — 17 November 2026

- **A download says where it comes from.** The Kind column shows Frame.io, Vimeo, SoundCloud or the site's name instead of just "Link", and so does the small text after the name (it wrongly said "this Mac"). The panel's From line names it too. The part after the arrow is, as for copies, the folder the file is saved into and its disk: a folder called "Vimeo" shows as "→ Vimeo" whatever the download is.

## 0.2.94 — 17 November 2026

- **Select several jobs in the queue.** ⇧-click selects every row from the last one clicked to this one; ⌘-click adds or removes one row. A bar above the queue then pauses, resumes or stops them all, or moves them: up or down one place, to the top or to the end. Esc or Clear ends the selection. Stop asks twice, with counts only, as for one job.
- **⌥↑ / ⌥↓ work every time.** Pressing them again right after a move did nothing, because the row lost the keyboard focus when the list redrew and a quick second press used the old order. The row now keeps its focus, the new order shows at once, and the moves are sent in turn. With rows selected, ⌥↑ / ⌥↓ move them all.
- Add link no longer refuses a long paste: a batch of signed Frame.io links (over a kilobyte each) was larger than any request was allowed to be.
- A stopped download keeps its name in the history instead of "Download".

## 0.2.93 — 17 November 2026

- **Several links are one download.** Pasting a Frame.io folder's links (or any several links) now adds one job to the queue instead of one per file. Before adding, Add link shows the files as the folders they will go into inside the chosen folder. The job downloads them one after another in that order, each checked for size, read back and hashed before it gets its name, exactly as before.
- One file that cannot be downloaded (an expired link, say) no longer stops the others: the job carries on and ends saying how many failed and why.
- **A link job has a Files tab,** with its folders open: each file shows Waiting, Downloading, Reading back, Downloaded · checked, or Could not download with the reason.
- **Sort by clicking a column heading** in the queue and the history: Name, Kind, Size, Written · proven or State. A second click reverses the order, a third goes back to the queue order (the only order rows can be dragged in). The same choices are in the sort list.
- Add link takes up to 200 links at once and looks them up a few at a time, so a long list is ready sooner; a look-up that takes longer than 15 seconds no longer loses its answer.

## 0.2.92 — 17 November 2026

- **Try failed files again.** A finished copy with failed files has a button under Problems, and a failed file has one in its own panel. The files go back to the end of the queue in the same copy and are copied with the same proof. Files already proven are not touched, and nothing at the destination is overwritten: a file in the way still fails without being touched. Each file's story keeps the failure and the retry.
- **The destination's format is checked before a copy is added.** If the destination disk is FAT32, files over 4 GB are counted and fail at once when their turn comes, instead of after 4 GB has been written. Names with characters FAT32, exFAT or NTFS may refuse, and names that differ only by capital letters on a disk that treats them as one, are counted too. You choose Copy the rest or Cancel. Counts only, never a list.
- **A full destination disk says what to do:** which disk, how much the next file needs and how much is free. The copy waits, and continues by itself once there is room; nothing copied is lost. The free-space check before each file now also works for files in folders not yet made at the destination.
- A name the destination's format refuses now fails with that reason in plain words.

## 0.2.91 — 17 November 2026

- **Vimeo videos in Add link.** Paste a Vimeo link (public, unlisted or with a password) and choose the quality: each picture size is listed with its frame rate and about how big it is, best first. Shuttle downloads the picture and the sound in small pieces and joins them as they arrive into one ordinary MP4 with its table of contents at the front, so it opens at once in any player or editor. No extra program is needed. A piece that does not arrive is tried again, with the video's addresses asked for afresh, and a paused or interrupted download continues from the last pieces kept, not from the start.
- **SoundCloud tracks in Add link.** The artist's original file when they allow downloads, otherwise the best stream (AAC as .m4a, or MP3), with the title, artist, date, genre, link and cover picture written into the file. Playlists and 30-second previews are refused with the reason.
- A Vimeo password is kept like the link: only in the private file on this computer, used to open the video, never shown, logged or put in the queue's records, and removed with the link. Every download is still read back from the disk and hashed before it gets its name.
- **Quick views over the queue:** All, Active, Done today and Only problems, each with its count. The finer filters (completed, skipped, stopped, failed, paused) are one click away under Filters.
- **On set: copies needed** is now a setting (1 to 4, 2 by default): how many read-back copies make a card SAFE TO UNPLUG, and VERIFIED once the card is read again.

## 0.2.90 — 17 November 2026

- **Add link:** download files from links into a folder, in the same queue as copies (same order, Run next, pause, stop, waiting, details). Paste one link or up to 50 (one per line), or paste a link anywhere on the page. Each link is looked up first: name, size, site, and whether it can continue after a break. Choose the folder and whether the downloads go at the end of the queue or next.
- Plain https links to a file are supported, such as Frame.io's "Download in Browser" links (the folder and share name added by the Frame.io links bookmark are kept). Frame.io share pages, links to this computer or the home network, Vimeo and SoundCloud links are refused with the reason; Vimeo and SoundCloud come later.
- **Each link is kept like a password:** only in a private file on this computer, never in the queue's records, the log, the page or a command line, and removed once its file has arrived or the download ends.
- **Each download is proven:** it grows as a temporary file next to its place and continues from where it stopped after a pause, a lost connection or a restart; a connection silent for a minute is closed and tried again. The size is checked against what the site said, the file is flushed, read back from the disk in full and hashed with XXH128 (compared with the bytes as they arrived when it ran in one go), and only then gets its name, never over a file already there ("name (2).mov"). An expired link fails at once with the reason.

## 0.2.89 — 17 November 2026

- **Files:** a new section to browse any disk. Every file and folder shows its **proven copies**, one small mark per drive (filled: read back and matching; half: on its way; dashed: no copy; red: a problem) and "all 3", "2 of 3" or "none yet". It works from the card and from any of the backup drives: either way you see all the copies of that original. A folder holding cards shows each card's copies per drive.
- **Tick files and drives, then copy only what is missing:** the tray at the bottom lists the drives that already hold this folder, with the space left after; "Copy only what is missing" makes one copy per drive holding just the files that drive lacks, each read back and the source read again. "Copy all" copies everything ticked; "Copy to another place…" opens New copy with the ticked files. ⌘A ticks everything shown.
- **Missing on a chosen drive** shows only what still needs copying. **Two panes** shows two folders side by side; the other pane's folder is offered as a destination.
- **Click a file** for its panel: the original and every copy with its state and path, its XXH128 hash with a Copy button, "Read this file again", the hash list, and each copy's full record.

## 0.2.88 — 17 November 2026

- **Offload a card to several drives in one step.** New copy (called **Offload a card** on set) takes up to four drives: "+ Add another drive" adds a line with its own Choose button. Start makes one copy per drive, each read back and proven against the source; the card's file list is made once and used by all of them. A drive that may not fit asks on its own; drives not added stay in the pop-up with the reason. New copy opens with the last card's drives already filled in.
- **A card's panel on set:** click a card's verdict to see, for each drive, where the copy is, the three reads (hashed while copying, read back, source read a second time) and how far each is, with a way into that copy's own details.
- **Wall view:** only the cards, very large, full screen, for a screen across the room. Esc or "Exit wall view" goes back.

## 0.2.87 — 17 November 2026

- **The details panel has tabs:** Overview (proven and written in large numbers, from and to, files, hash, proof, start and working time, the speed of each disk with the slower one marked as the limit, and the buttons, Pause and Stop included), Files (the file list with its filters), Proof (what is read for this copy and how far each read is), Problems (counts of failed, deleted, changed, added and source warnings, then each file and what happened) and Log (this copy's own lines from Shuttle's log, with the full log one click away).
- **The strip along the bottom** says the state, the whole queue's time, the speed and what limits it. **System** opens a pop-up with memory, buffers, processor, open files and free space, the full log and updates.
- **Disks in the sidebar,** with free space and what each disk is doing now (reading, writing, idle, or slowed down by its cable).
- **Search or run a command (⌘K):** find any copy, disk or action by typing a few letters.
- The running copy shows the file it is on under its name. The panel above the queue is gone; its numbers are in the strip.

## 0.2.86 — 17 November 2026

- **The queue and history are now a table,** as in the agreed design: one line per copy with its name and route, kind, size, the written and proven bar, and a short state ("Verified", "Safe · read back", "1 changed", "62% · 412 MB/s · 8:12 left") with the time under it. Columns line up row to row.
- **Everything about a copy opens in a panel on the right:** click a row for its full summary, paths, proof, problems, buttons (hash list, verify, check source again) and its files with their hashes. Click the row again, press ✕ or Esc to close it. On a wide screen the panel stays beside the list; on a laptop screen it slides over it. Clicking a drive on the On set cards opens the same panel.

## 0.2.85 — 17 November 2026

- **A new look.** One dark frame on every screen: the top bar holds the name, the General / On set switch and New copy; the sections (Queue, Disks, Settings, About) are on the left; a strip along the bottom always says what Shuttle is doing, with a System button for memory, processor and open files. Nothing that was on the page before is gone; it is restyled and moved.
- **Change the queue order.** A copy or verify that has not started can be dragged by its handle, sent to the front with **Run next**, or moved with ⌥↑ / ⌥↓ on a focused row. The running copy is never interrupted; the new order counts from the next one. The order is saved and survives a restart.
- **On set.** The switch in the top bar shows the copies as camera cards: one tile per source folder, one chip per destination drive. A card says **SAFE TO UNPLUG** once 2 copies are read back and match, and **VERIFIED** once the source has also been read a second time and matched for 2 copies; otherwise **KEEP CONNECTED** or **CHECK PROBLEMS**, with the reason under it. Pick the shoot day; show the cards as tiles or as a table. Clicking a drive opens that copy in the queue with its files and proof.
- **Fix:** two copies added within the same thousandth of a second (one card to several drives) could share one record, the second replacing the first. Each now always gets its own.

## 0.2.84 — 17 November 2026

- **Updates no longer ask for drive permission again.** Every release of the Mac programs is now signed with the same certificate (kept as a secret in GitHub), so macOS recognises the new version as the same app. Until the certificate is added, builds are signed the old way and macOS still asks after an update. The first update to a signed version asks one last time.

## 0.2.83 — 16 November 2026

- **The hash list beside the media folder is named by the time it is written:** `hashes-20261116-2145.txt` (year, month, day, hour, minute), in the wrapper next to the media folder. It never replaces an earlier list; two in the same minute get `-2`.

## 0.2.82 — 16 November 2026

- **A copy copies exactly the list made when it was created.** The hourly re-list and its Settings entry are gone. A change never stops a copy; each one is reported in plain words, and a copy with any of them ends as "Finished with problems" with the counts (failed, deleted, changed, added).
  - A file **deleted or moved** before its turn is not copied and is listed as such.
  - A file **changed** since it was listed (size or modified time) is copied as it is now and flagged, with the old and new size and date.
  - After the copy the **source is listed again** (names only, nothing read) and files added since are reported, never copied by themselves. If the source disk is not connected it says so. "Show added files", **"Copy these too"** (a separate new copy of just those files, same destination and settings) and **"Check source again"** are on every finished copy.
  - Older settings files that still hold the re-list time are read without trouble; the entry is ignored.

## 0.2.81 — 16 November 2026 (shipped inside 0.2.82)

- **Folder layout.** A new Settings section holds the defaults and New copy can change them per copy: put the copy inside a wrapper folder (`destination/wrapper/source folder`), named after the source folder, after its disk, or typed. The media folder stays an exact copy and nothing is ever added inside it. Optionally the hash list is written one level above: `destination/A001/A001-hashes.txt` beside `destination/A001/A001/`. A second copy into the same wrapper never replaces an earlier list (`-hashes-2.txt`).

## 0.2.80 — 16 November 2026 (shipped inside 0.2.82)

- **Hash list export.** On any copy, "Hash list…" (and one on each folder row and in each file's info) builds a list from the copy's own records: nothing is read from a disk again and nothing is written to the destination. Choose the xxhsum list (`hash  path`, checkable by hand with `xxhsum -c`), the CSV report (path, size, modified time, hash, proof level, date), or both; for copies that made both hashes, XXH128 or XXH64. Streamed, so a list of hundreds of thousands of files is never held in memory.

## 0.2.79 — 16 November 2026

- **Space warning before Start.** In New copy, once the size is counted and a destination is chosen, a line under the destination says if the copy may not fit there, counting the copies already queued to the same disk (and the free-space floor on the internal disk). It only warns; Start still asks as before. Files already at the destination are not counted when the copy starts, so the real need can be lower.

## 0.2.78 — 16 November 2026

- **Select all in the folder picker.** ⌘A (Ctrl+A) ticks every folder and file in the folder you are looking at; ⌘⇧A (Ctrl+Shift+A) unticks them all. Only in "Choose what to copy"; typing in a field is not affected. Page only.

## 0.2.77 — 16 November 2026

- **The size count no longer jumps sideways or back to zero.** In the folder picker and New copy it is now two lines: the label on top ("Counting…", "The 2 chosen", "This whole folder"), the size and file count alone under it. Removing a chosen folder keeps the last numbers on screen until the new count is done, instead of dropping to zero. Page only.

## 0.2.76 — 16 November 2026

- **Menu bar icon: "Check for updates" is level with the other items.** Its text started about 8 points to the left of "Open Shuttle", "Open at login" and the rest, so it stuck out. It now starts at the same place.

## 0.2.75 — 16 November 2026

- **Fixes the page not loading in v0.2.74** (it stayed on "Loading…" and "Connecting"). The new size-count code reused a name the page already used, which stopped the whole page script. Renamed, and a test now fails the build if any name is declared twice. Do not install v0.2.74.

## 0.2.74 — 16 November 2026

- **The size count never drops back to zero when you add a folder.** Choosing one more folder to copy now keeps the total you already had on screen and keeps adding to it as the new count catches up, instead of restarting from 0. If you take something away, or choose a different folder, the count starts fresh, since the old total would be too high.

## 0.2.73 — 16 November 2026

- **Opening a copy shows its folders closed.** The first folders no longer open by themselves (v0.2.70), and a single top folder no longer opens either. Open the ones you want; a folder you open stays open.

## 0.2.72 — 16 November 2026

- **A folder is listed once, and the list becomes the copy's file list.** The count made while you choose the folder (names and sizes only, no file data) is also written as the copy's list. When you press Start, the copy adopts it, so it is sized exactly the moment it enters the queue and does not list the folder again. The free-space check uses the same list, which also means a big folder is no longer skipped by the 8-second limit. The whole-queue time left now has a size for every item.
- **Press Start before the count has finished** and a "Preparing: listing the files, N so far" line shows its progress; the copy is added when the list is complete. Closing the pop-up cancels it. If the folder cannot be counted, the copy lists it itself when it starts, as before.
- **Settings → "Listing a waiting copy's files again":** if a copy waits in the queue longer than this before it begins, its folder is listed again first, so files added in the meantime are not missed. Choices: never, 15 or 30 minutes, 1 (default), 2, 4, 12 or 24 hours. A copy that has begun is never listed again. With "never", it copies exactly the list made when it was added.

## 0.2.71 — 15 November 2026

- **A copy is sized as soon as it is added.** The size the folder picker already counted (same walk, same rules as the copy's scan) now goes with the copy into the queue, so the whole-queue total and time left include it at once instead of "not sized yet". The copy's own scan still runs to list the files and replaces the estimate with the real size. A copy added before the picker had finished counting still shows "not sized yet" until its scan starts.

## 0.2.70 — 15 November 2026

- **"Time left for the whole queue" is now an estimate for everything left,** not for the file being copied. It uses the running job's average speed over its whole run (pauses left out), applied to all remaining work, jobs not started yet included. A job's own time left uses the same average, so it no longer jumps with each big file.
- **A job item opens with its folders open.** Big jobs used to open as closed folders. Now the first folders (up to 10 in a folder, 40 in a job) open at once, going down through folders that hold only folders; a folder with files of its own keeps its subfolders closed, and a folder you close stays closed.

## 0.2.69 — 15 November 2026

- **The program file is now named "Shuttle Server"** (Shuttle renames it once, at start, and leaves "shuttle-server" as a shortcut so the old start command still works). macOS lists the start-at-login item by the program file's name, so Login Items & Extensions should now read "Shuttle Server", with the logo set in 0.2.68.

## 0.2.68 — 15 November 2026

- **The program file carries the Shuttle logo** so System Settings → Login Items & Extensions can show it. 0.2.67's link inside the app is gone (macOS showed it with a shortcut arrow and still read the file's own name). macOS lists an item by its file name, which Shuttle cannot change without an Apple developer signature, so the entry still reads "shuttle-server".

## 0.2.67 — 15 November 2026

- **The start-at-login item runs Shuttle from inside the "Shuttle Server" app** (a link to the program in the app's folder), so macOS can list it as "Shuttle Server" with the logo instead of "shuttle-server". The link follows the program when it is updated. macOS may still call an app that is not signed by an Apple developer "unidentified developer".

## 0.2.66 — 15 November 2026

- **Shuttle Server shows under its own name and logo in System Settings → Login Items & Extensions,** not as "shuttle-server" with a plain icon. The start-at-login item is now tied to the "Shuttle Server" app in Applications, which has the page's logo (purple, up and down arrows, redrawn to match it exactly). An existing login item is updated the next time Shuttle starts.

## 0.2.65 — 15 November 2026

- **Back to the plain menu bar helper.** 0.2.64's "Shuttle Server" helper app crashed with "method types do not match the superclass method types" and macOS flagged it as an Intel-only component. It is gone, and Shuttle removes the folder it made. The launcher app in Applications keeps the new icon.

## 0.2.64 — 15 November 2026

- **Shuttle has its own name and icon in macOS.** The menu bar icon is now run by a small app called "Shuttle Server" (purple, with an up and a down arrow) that Shuttle makes on your Mac, so System Settings shows that name and icon instead of "osascript". The "Shuttle Server" launcher app in Applications has the same icon. If the app cannot be made or quits at once, Shuttle uses the plain helper from before, and says so in the log.

## 0.2.63 — 15 November 2026

- **Start at login.** Settings has a new "Start at login" switch, and the menu bar icon has "Open at login" with a tick. Either one turns it on or off; the other follows within seconds. On macOS it is a launchd agent in ~/Library/LaunchAgents that only starts Shuttle (no automatic restart, so Stop Shuttle stays stopped until the next login). Turning it on does not start a second copy now. An update keeps the agent pointing at the program.

## 0.2.62 — 15 November 2026

- **USB names read the same everywhere: the current name, then the old one in brackets,** for example "USB 3.2 Gen 2 (USB 3.1 Gen 2)", the name printed on a T5. "Drive can do" now reads speed first, like the link line: "10 Gb/s · USB 3.2 Gen 2 (USB 3.1 Gen 2)".

## 0.2.61 — 15 November 2026

- **"Drive can do" is read from the drive.** A USB drive declares the USB version it supports (3.1 means it can run at 10 Gb/s); Shuttle reads that from the system's device tree, so it works for any drive. The list of known models (Samsung T3, T5, T7, T9) is only a fallback when the device tree has no version.

## 0.2.60 — 15 November 2026

- **Storage shows the link and what the drive can do.** A USB disk now reads "USB link: 5 Gb/s · USB 3.2 Gen 1 (USB 3.0)", then "Drive can do: USB 3.2 Gen 2 · 10 Gb/s" for drives Shuttle knows (Samsung T3, T5, T7, T9; other drives show no line, since the system does not report it).
- **A yellow "Slowed down" line** appears when the disk runs below what it could: a link the system calls limited (cable, adapter, hub or port), or USB 2.

## 0.2.59 — 15 November 2026

- **Queue item tidied.** A copy is named after its folder; when files or folders were picked one by one, after the one picked, or "2 folders", "3 files", "2 folders, 1 file". The %, GB and time left line is larger, and the source and destination disk speeds sit right under it. The second progress bar is gone. Opened, the "Now:" line sits below the file list.
- **Source and destination as disk → path:** for example "Source: This computer → Desktop/TESTFOLDER" and "Destination: BB - backups → /TestDest/TESTSQ13". The full path shows when you hover.
- Fixed: a running copy showed a stray dot in its top-left corner, and its details used the small grey text of the "Live" badge.

## 0.2.58 — 15 November 2026

- **Choose the hash copies use** (Settings → Hash for copies): XXH128 (the default, as before), XXH64, or both. Every read of a copy makes the chosen hash — the source as it is copied, the copy read back, and the source read again — and they must match. With both, each read makes both, and both must match. A copy keeps the hash it started with; a Verify reads each file with the hash its copy made, so older copies still verify after the setting changes.
- **The file info shows the hashes made**, each on its own line labelled XXH128 or XXH64, and which hash the copy was set to. Hashes from Verifies and "Read again" are shown the same way.
- **XXH64 list files are no longer made.** Writing checksum lists beside the destination will come back later as its own function. The 0.2.57 setting is gone; copies it made keep their lists, and their XXH64s still show in each file's info.

## 0.2.57 — 15 November 2026

- **XXH64 lists for other tools** (Settings → Hashes for other tools, off by default). With it on, every file also gets an XXH64, the hash most offload tools and checksum lists use, made while the copy is read back. When the copy finishes, the list is saved **next to the destination folder, not inside it**: for a destination "Day 1" it is "Day 1.xxh64" beside it, in the format of `xxhsum -H1`, so `xxhsum -c "Day 1.xxh64"` run there checks the copy. Only files whose copy is proven are listed. An existing list is never replaced (the next one is "Day 1 2.xxh64"). A copy that is checked by its own Verify item gets its list from that Verify; a copy with no check gets none. The job says where the list went.
- **XXH128 stays the proof.** Nothing about how copies are checked has changed; the XXH64 is extra, for other tools.
- `shuttle-server hash -H1 FILE` prints a file's XXH64, as `xxhsum -H1` does. Every release now checks Shuttle's XXH64 against the official `xxhsum` too, as it already did for XXH128.

## 0.2.56 — 14 November 2026

- **Every finished job says how long it took, as minutes and seconds** (for example "took 12:34", or "1:05:09" from an hour). This is the job's own working time: time paused, waiting for a disk or waiting in the queue is left out, so it compares directly with other offload tools. It shows on the history line, on the job card, in the Status headline once nothing is running ("last copy took 12:34"), in the menu bar and in the log. The average speed on the history line is now worked out from that same time. A stopped job says how long it ran before you stopped it.
- Jobs finished before this version keep their old rounded time, since their working time was not recorded.

## 0.2.55 — 14 November 2026

- **Faster copying.** Each disk is now asked for 16 MB at a time instead of 4 MB, and the source can read up to four pieces ahead while the destination writes, so neither disk waits for the other. Small files get small buffers, so they no longer cost a large piece of memory each.
- **Faster checking.** While a copy is read back, and while the source is read a second time, the next piece is read while the last one is hashed, so the disks no longer pause for the hash.
- **Speeds in the same units as other apps.** Every speed now counts 1 MB as 1,000,000 bytes, like the speed test, the disk makers and other offload tools. The copy speed used to count 1 MB as 1,048,576 bytes, so it read about 5% lower than the same speed in those apps.
- **History keeps the copying speed.** A finished copy's line now shows its average speed while copying, beside the overall average, so two runs (or two apps) are easy to compare.

## 0.2.54 — 14 November 2026

- **One bar, two colours, like the offload apps.** A copy's bar grows in purple as each file is written, then pink fills the same stretch as that file is read back and matches. While the next file copies, purple moves ahead again and pink catches up behind it. On each copy in the queue and on the Status bar (where a Verify job counts as pink).

## 0.2.53 — 14 November 2026

- **One progress bar on the Status panel.** The running copy's line (name, %, time left) now sits above "X GB of Y GB", and its speed is at the right end of the big % line, in the same large type. The second bar is gone, since it showed the same thing.

## 0.2.52 — 14 November 2026

- **Status panel slimmed down:** it shows only the job that is running. Its speed now sits at the right end of its bar, in line with it, and the bar is shorter to make room.
- **The queue count ("2 copies, 1 verify") sits beside the Queue title** instead of in the Status panel.
- **No file lines on the Status panel:** "Files" and "This file" are gone; a file's details are in the queue and the history.
- **The line "Includes what later Verify jobs of this copy found." under a copy's file list is gone.**

## 0.2.51 — 14 November 2026

- **Verifying bars match the Verifying chip:** the copy's purple is the track, and the progress is drawn over it in the chip's pink. On a file's row, under "Now:", on the Status page's "This file" and on a Verify job's bars. A copy's overall bar stays as it was.
- **"Now:" and the file's row move together.** The file being worked on takes its chip and bar from the same update as "Now:", instead of waiting for the file list to refresh.

## 0.2.50 — 14 November 2026

- **Choose several folders at once.** In "Choose what to copy", ⌘-click or Ctrl-click a folder or file to tick it without opening it; Shift-click ticks everything from the last one ticked. The tick boxes work as before.
- **Queue sits under New copy;** the filter buttons are centred on New copy's line.
- **Speed beside the pause button:** a copy's average copy speed, a Verify's speed, instead of in the line under the bar and a big line in the details.
- **"Now:" says what is happening**, for example "Now: Copying (the source is hashed as it is read), 2.4 GB of 9.2 GB", without the file name (the file list shows the file).
- **No repeated counts while copying:** "Copied 7 of 11 files, not read back" is gone; what it added (read twice, already there, failed…) joins the "Copied … · Verified …" line.
- **From and To on one line** when they fit.
- **Verify files at the left of the line above the files,** beside the filters or the heading.

## 0.2.49 — 14 November 2026

- **Files always sit under their folder's name,** in a row that closes and opens, even when there is only one folder or only a few files. With up to 20 files every folder starts open; with more, folders start closed as before.
- **Closing a folder no longer closes the whole copy.** The list redrew under the click, and the copy's card took it for a tap outside.
- **Verifying is purple on light purple.** While a file is read back or compared, its bar, the copy's bars and the Status bar are purple on a light purple track; a Verify job's bars are purple too, no longer pink.

## 0.2.48 — 14 November 2026

- **Copy size while choosing the source.** The picker shows how much a copy of the open folder would be, for example "This whole folder: 412.3 GB in 18,204 files"; tick files or folders and it shows the size of just those ("The 3 chosen: …"). New copy shows "Copy size: …" under the source.
- Counted the way the copy itself counts: system files left out as set in Settings, links not counted. A big card shows "Counting… so far" while it adds up. Only names and sizes are read, one count at a time, and a count nobody is looking at stops by itself.

## 0.2.47 — 14 November 2026

- **Storage is live.** While the Storage tab is open it updates itself every 3 seconds: disks plugged in or out, and free space, show without pressing Refresh. Refresh now also reads the hardware again from scratch.
- **Each disk shows where it is attached:** the Mac's port and its Thunderbolt/USB4 bus (the same names as System Information), and any hub or dock in between.
- **Disks sharing a port are named,** for example "Shares this port with T5-B: used at the same time, they split its bandwidth".
- **USB speeds are found on macOS 26,** whose hardware report no longer names the disk; Shuttle now matches the drive by its USB place. A drive running slower than it can ("Limited to 5 Gb/s") says the cable, adapter, hub or port is holding it back.
- **A disk whose Trash has items says so:** files deleted in Finder keep their space until the Trash is emptied.

## 0.2.46 — 13 November 2026

- **Files shown as folders.** A copy or Verify with more than 20 files lists its folders closed; click one to open it. Each folder says how many files it holds and how they stand, for example "72 files · Destination verified", or "3 errors", "40 of 72 done", "Not verified". A folder that holds nothing but one folder opens by itself.
- **Show more files / Show fewer files** now sit beside Verify files; a long folder has its own "Show more in …".
- **Verify files only on a copy.** A Verify's row just shows what it found; its files open the same way.
- **A Verify's file info shows the three reads too:** the hash stored when the file was copied, what this Verify read, and an empty "Source — second full read" line with a Verify… button when the source was not read again.
- **File info states what later Verify jobs found**, as the file list does (it said "Copied · not verified" for a file a Verify had since read back).

## 0.2.45 — 13 November 2026

- **A Verify sits under the copy it checks, not under whatever is above it.** Each Verify row is placed directly under its own copy, set in with the pink edge. A Verify whose copy is not in the same list (filtered out, still in the queue while the copy is in the history, or checking several copies) is shown on its own, not set in, so it never looks attached to an unrelated copy.

## 0.2.44 — 13 November 2026

- **"Verifying", not "Copying", while a file is checked.** When a file already at the destination is read in full and compared, or a copy is read back, or the source read again, the file's chip, the status line and the menu bar say Verifying.
- **Files already there count as verified, not copied.** The counts read, for example, "Copied 0 of 1 · Verified 2 of 3 · 2 already there (verified, not copied)" instead of "Copied 2 of 3, not verified".
- **File info shows all three reads:** the source hashed while it was copied, the destination read back, and the source read a second time, each with where it came from (the copy itself or a Verify and its date). A read that was never made says so, with a Verify… button that opens Verify files with that check already chosen. Earlier Verify results for the file are listed below.

## 0.2.43 — 13 November 2026

- **New copy moved to the front** of the Queue header: New copy, then "Queue", then the filters, all on one line. On a narrow screen such as the iPhone the filters still move to their own line below.

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
