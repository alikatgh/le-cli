# Windows port — scoping (not started)

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
