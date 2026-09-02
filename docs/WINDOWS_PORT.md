# Windows port

**Status (2026-08-30): shipped in the first cut.** `le.exe` builds for
windows-amd64 and windows-arm64, CI runs the suite on windows-latest, and the
release workflow publishes zips. What landed, item by item against the plan
below: CI (1) ✓ · scan split (2) ✓ · stop path (3) ✓ · intel (4) ✓ · tools (5)
✓ · OSC (6) ✓ as predicted · release (7) ✓.

How it was built without a Windows box, which is the reusable part: every
parser is **untagged and pure** (scan/netstat.go, kill/taskkill.go,
intel/windows.go) so it is tested on the macOS dev machine against captured
output; only the process-spawning glue carries `//go:build windows`. A
`GOOS=windows go vet ./...` on ubuntu type-checks all of it — tests included —
in seconds, and three untagged live tests (scan finds its own listener,
stillSame accepts its own PID, WatchPID probes its own PID) run the real chain
on windows-latest.

## Still open

- **cwd.** Windows keeps another process's working directory in its PEB;
  reading it needs `NtQueryInformationProcess` + `ReadProcessMemory`, and is
  refused for elevated processes. The DIR column is empty and `le stop --dir`
  matches nothing. Worth doing via x/sys/windows if Windows users ask.
- **Distribution.** Zips on the release only. A winget manifest and a scoop
  bucket are the natural next step; both want the zip name template that is
  already in place.
- **Avoid-list tuning.** The Windows system-process set in intel/windows.go
  was written from knowledge, not from a survey of real machines the way the
  macOS one was. Expect to add names.
- **Localised `taskkill`.** The "needs /F" hint keys on English text. A
  non-English Windows still refuses (the safety property holds) but with
  taskkill's own message instead of the `taskkill /F /PID n` hint.

---

## Original scoping

Goal: ship `le.exe` (windows-amd64 + windows-arm64) so le competes with
PortKiller's Windows build. The TUI (bubbletea/lipgloss) is already
cross-platform; the work is the platform layer underneath it.

## Constraints discovered on macOS-dev machines

- No local Go toolchain and no Windows box: every step validates via CI only.
  Add a `windows-latest` job (build + unit tests with stubs) FIRST, so each
  subsequent change gets signal.
- `gofmt`/lint run on ubuntu; keep build-tagged files formatted from the start.

## Work plan (ordered)

1. **CI first**: add `windows-latest` to the test matrix, `GOOS=windows
   go build ./...` cross-compile check on ubuntu (fast signal without a
   Windows runner queue).
2. **Split the scan backend** behind build tags:
   - `scan/scan_darwin.go` + `scan_linux.go` (current lsof path; lsof exists on
     linux too — verify flags) — mostly a rename of today's code.
   - `scan/scan_windows.go`: listeners via `netstat -ano -p tcp` (PID in last
     column) or PowerShell `Get-NetTCPConnection -State Listen | ...`;
     process name/command line via `Get-CimInstance Win32_Process` (or
     `tasklist /fo csv`). CPU/mem via the same CIM query.
3. **Stop path** (`kill` package): build-tag split —
   - windows: no SIGTERM semantics; graceful = `taskkill /PID n`, force =
     `taskkill /F /PID n`. The PID-recycling start-time re-verify maps to
     Win32_Process CreationDate.
   - `syscall.Kill(pid, 0)` probes (WatchPID, ExitWatcher parity) → windows:
     OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION) or `tasklist /FI "PID eq n"`.
4. **intel**: brew/launchd/docker branches no-op on windows (docker works —
   keep it); system-process avoid-list needs a windows set (svchost, System,
   wininit…). kubectl/cloudflared branches work as-is.
5. **tools**: open (start), flush-dns (ipconfig /flushdns) map cleanly; hold
   (net.Listen) and check/scheme/qr are pure Go ✓; keep-awake / sleep-display /
   restart-dock/finder are mac-only — hide via build tags + runtime GOOS guard
   in cmd registration.
6. **OSC 52 / OSC 8**: Windows Terminal supports both ✓ (older conhost
   doesn't — degrade silently, already the behavior).
7. **Release**: goreleaser or extend the release workflow with
   windows/amd64+arm64 zips; update README install section.

## Deliberately out of scope

- The Swift app stays macOS-only.
- No WSL special-casing in v1 (netstat inside WSL sees its own namespace).

Est: ~1–2 focused sessions, dominated by CI round-trips for the netstat parser.
