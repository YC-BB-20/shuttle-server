# Shuttle Server — working rules for agents

The design lives in the claude.ai project (`GOALS.md`, `DESIGN.md`, `COMPATIBILITY.md`, `PROTOCOL.md`, `STATE.md`), not here. This file is how to work in the code.

## Rules

- **Go, standard library only, no cgo** (`CGO_ENABLED=0`). The Mac builds cross-compile from Linux. A new dependency must earn its place and be noted in the commit.
- **Edit only the lines that need it.** Grep; do not read whole files. Add a test for every new feature.
- **Nothing unbounded** (DESIGN.md section 18): file data in memory comes from `limits.Budget`; work runs on fixed worker pools, never a goroutine per file; nothing held in memory grows with the number of files; every request, read and outside program has a timeout. Schedulers ask `Watchdog.AcceptingNewWork()` before starting anything.
- **Every state write goes through `fsx.WriteFileAtomic`** (temp file, real flush — `File.Sync` is `F_FULLFSYNC` on macOS — rename, folder flush). Never edit a state file in place.
- **Never delete or overwrite a user's file as a side effect.** Deleting lives only in the Delete function. Our own temp files are the only thing code may remove. Damaged state is renamed aside, never deleted.
- **Never a secret** in the page, the API, a log line, a state file or a command line.
- **The page:** no sideways scrolling on iPhone (`overflow-x:clip` on html/body, `min-width:0` on grid/flex children, `overflow-wrap:anywhere` on paths), centred pop-ups, every wait says why. Only localhost until the design says otherwise.
- **Plain language** in anything the owner reads: the page, log messages, errors.

## Layout

| Path | What |
| --- | --- |
| `cmd/shuttle-server/` | `main`: flags `-data`, `-clear-safe-mode`, `-version`; commands `hash`, `install-launcher`, `soak` |
| `embed.go`, `web/index.html`, `CHANGELOG.md` | Built-in page and the changelog shown in About |
| `internal/app` | Wires everything; `Run` returns an exit code (0 ok, 1 start-up, 2 panic, 3 memory, 4 restart: `main` then `exec`s `RestartPath`) |
| `internal/config` | Host config `config.json`; 0 means "automatic for this machine" |
| `internal/limits` | Buffer budget, memory watchdog, CPU share and priority |
| `internal/safemode` | Start-up crash counter (3 within 5 min → safe mode) |
| `internal/events` | Coalesced server-sent updates, slow pages dropped |
| `internal/server` | HTTP handlers, localhost Host check, timeouts; `browse.go` is the picker (places, path buttons, files) |
| `internal/xxh3` | XXH128 in plain Go, saveable state; `testdata/` holds the official vectors |
| `internal/diskio` | All user-disk I/O: read-only opens, cache bypass, chunk retries, per-disk limit and stall timer, volume ids, `VolumeMounted` |
| `internal/location` | The source boundary (section 17): today only `Folder`, optionally limited to chosen names (`Only`) |
| `internal/copyjob` | Copy jobs: scan → manifest, copy hashed as it is read then D1 and S2 at once (default) or S1/copy/D1 then S2, journal with grouped flush, checkpoints, manager and views (progress, speed, time left), paged file list (`Files`), file info (`FileInfo`) and history |
| `internal/update` | One-click updates from the newest green CI run, version history (green builds + kept versions, install any) (GitHub Actions API, key in `secrets/github-update-key`, 0600, never logged); backups in `bin/backups`; rollback |
| `internal/launcher` | Writes `~/Applications/Shuttle Server.app` |
| `internal/soak` | Long-run resource test |
| `internal/fsx` | Atomic writes, free space |
| `internal/sysinfo` | Total RAM, open-file limit |
| `internal/remote` | File servers (DESIGN.md section 19): `Remote` with S3 (SigV4, multipart pieces from the budget, resume via ListParts) and WebDAV (PUT, MOVE without overwrite); `Store` keeps logins in `secrets/servers/<id>.json` (0600), the page sees `Public` only |
| `internal/menubar` | macOS menu bar icon: `menubar.js` run by `osascript`, fed by a status file in `temp.noindex`, own process group |
| `internal/logx` | `logs/YYYY-MM/shuttle.log`, appended, never trimmed |
| `internal/version` | `Version` constant; CI sets `Commit` |

Data folder (default `~/Shuttle-Server-Data`): `config.json`, `state/` (`startup.json`, `jobs/<id>/` with `job.json`, `manifest.jsonl`, `journal.log`, `skipped.jsonl`), `logs/`, `records/`, `secrets/` (never backed up, never logged), `temp.noindex/` (the `.noindex` suffix keeps Spotlight out).

## Commands

```sh
gofmt -l .                         # must print nothing
go vet ./... && GOOS=darwin GOARCH=arm64 go vet ./...
go test ./...                      # includes a 13 s soak; -short skips it
CGO_ENABLED=1 go test -race ./internal/...   # locally, when touching concurrency
go run ./cmd/shuttle-server -data /tmp/ssdata   # page at http://localhost:8470
```

## Release

1. Bump `Version` in `internal/version/version.go` and add a `CHANGELOG.md` entry.
2. One commit to `main`, message `vX.Y.Z: <what changed>`, then push.
3. `gh run list -L 1` until green. CI publishes `shuttle-server-<os>-<arch>` artifacts (a `.tar.gz` inside). Only a green run gets installed.
4. Update `STATE.md` in the project, once.
