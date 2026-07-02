# Changelog

All notable changes to `le` are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.1.5] - 2026-07-02

### Added
- `le stop --dir <path>` stops every listener whose working directory is that
  path or nested under it — the terminal equivalent of the macOS app's
  folder-stop, for clearing out everything a project spun up.

### Fixed
- `le stop` (by port, pid, or `--dir`) silently refused every process as
  "recycled" on days 1–9 of the month: `ps` pads single-digit days in its
  start-time output with a second space, and the scan and re-verify paths
  normalized that whitespace differently, so the recycle guard never matched.

### Internal
- Continuous fuzzing (`fuzz.yml`) runs the ps/lsof/docker parser harnesses
  nightly instead of only replaying their seed corpus.
- Added gosec to the lint gate.
- Test coverage raised across the core packages via a command-runner
  dependency-injection refactor: `scan` 24→94%, `kill` 20→83%, `intel`
  14→86%, `tools` 12→67%, `cmd` 0→64%.

## [0.1.4] - 2026-07-01

### Added
- Per-column sorting in the TUI: number keys `1`-`5` sort by port / pid /
  what / risk / owner, press again to reverse. Risk sorts by severity
  (low < medium < high), not alphabetically. The active column shows a
  `^`/`v` indicator in the header.
- Man pages (`man le`, `man le-stop`, …), generated from the command tree
  and bundled into each release tarball.

### Internal
- CI now runs on every push and PR (previously only on a release tag),
  on both macOS and Linux, with `go vet`, a `gofmt` check, `go test -race`,
  and `golangci-lint`.
- Added fuzz tests for the `ps`/`lsof`/`docker` output parsers.
- `cmd/` package test coverage: 0% → 53.5%.
- Added `CHANGELOG.md`, `SECURITY.md`, `CONTRIBUTING.md`, issue templates,
  and Dependabot for Go modules and GitHub Actions.

## [0.1.3] - 2026-07-01

### Fixed
- `truncate()` in the TUI now clips by actual terminal display width
  instead of rune count — a CJK or emoji identity/container name could
  previously render at up to ~2x the intended column width.
- The TUI's live table could occasionally roll back to stale data:
  concurrent scans (the refresh timer, a manual `r`, and the automatic
  refresh after a stop) can complete out of order, and an older result
  landing after a newer one used to overwrite it. Results now apply in
  timestamp order.
- A listener whose `ps` output had a legitimately empty command (e.g. a
  zombie process with no argv) could be dropped from the table entirely,
  as if only half of `le`'s two `ps` calls had succeeded for it.

## [0.1.2] - 2026-07-01

### Fixed
- Docker container names containing regex metacharacters (routine in
  docker-compose-generated names, e.g. `api.web`) could match the wrong
  running container when `le` re-verified a container before stopping it.
- A config file read error was being printed right before the TUI's
  alt-screen switch hid it — an invisible warning for a typo'd config.
- `brew services stop` now re-checks that the formula is still known to
  Homebrew immediately before acting, mirroring the existing Docker
  container re-check.
- Table and detail-pane text could corrupt UTF-8 (a lone byte of a
  multi-byte character) when truncating a long identity, path, or command
  line for display.
- `le`'s two separate `ps` calls (split out to fix a v0.1.1 field-shift
  bug) could disagree on which PIDs they'd both seen, silently downgrading
  the PID-recycle guard for the affected listener.

## [0.1.1] - 2026-07-01

### Fixed
- The Homebrew tap formula shipped with placeholder checksums — `brew
  install alikatgh/tap/le` failed for everyone until this was corrected.
- A listener's Docker container could be misattributed when multiple
  containers published overlapping ports.
- `docker stop` was never re-verifying that a container name still
  pointed at the same container between scan and stop — a freed and
  reassigned name could get the wrong container stopped.
- `ps` output parsing broke on account names containing a space (routine
  for directory-joined accounts), shifting every field after it.

## [0.1.0] - 2026-06-30

Initial release: live TUI (`le`), one-shot listing (`le list`, `--json`),
smart stop (`le stop <port|pid>` — TERM, `brew services stop`, or
`docker stop`, whichever fits), plus `le hold` / `le wait` / `le ready`
for scripting against a port's lifecycle. macOS and Linux.

[Unreleased]: https://github.com/alikatgh/le-cli/compare/v0.1.5...HEAD
[0.1.5]: https://github.com/alikatgh/le-cli/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/alikatgh/le-cli/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/alikatgh/le-cli/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/alikatgh/le-cli/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/alikatgh/le-cli/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/alikatgh/le-cli/releases/tag/v0.1.0
