# Changelog

## 0.1.1 — 11 November 2026

- An icon in the Mac's menu bar while Shuttle Server runs: arrows when all is well, a warning triangle when something is waiting, a stop sign in safe mode. Its menu shows the current status, opens the page, and quits Shuttle Server.
- The icon is run by macOS's own scripting, so nothing is installed. It disappears when Shuttle Server stops, and quits by itself if Shuttle Server is gone. Turn it off with `"menu_bar_icon": false` in `config.json`.

## 0.1.0 — 11 November 2026

- First release: the skeleton. Nothing moves files yet.
- Runs as a plain program on this computer only (localhost), with one data folder holding settings, state, logs, records and temp.
- Settings file `config.json`, written with defaults on first start.
- Built-in limits so a fault can never freeze the computer: one fixed memory buffer budget, a memory watchdog, half the cores at a lower priority, page updates coalesced to a few a second, and safe mode after three crashed starts in five minutes.
- The page shows the version, what it is doing, the limits in use, free space against the floor, and this changelog in About.
