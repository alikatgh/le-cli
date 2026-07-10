# Changelog

All notable changes to `le` are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.1.15] - 2026-07-10

### Added
- Eight retro themes ported from the Localhost Explorer mac app, so one
  identity travels between the desktop and the terminal: `msdos` (CGA on
  Norton Commander blue — the cursor bar is the authentic cyan), `system7`
  (one-bit Macintosh), `gameboy` (four shades of DMG olive), `phosphor` and
  `amber` (CRT glow), `paper` (e-ink), `blueprint` (cyanotype), and
  `vaporwave`. `system7`, `gameboy`, and `paper` are dark-on-light — pair
  them with a light-background terminal. The canvas colour and the app's
  bundled raster fonts don't port: your terminal owns those.
- CPU% and memory for every listener. The table and `le list` gain a CPU
  column that escalates dim → amber → **red/bold** as a process heats up
  (thresholds: 50% = half a core, 200% = a runaway); `le list` also appends
  a `▲`/`●` glyph so the signal survives pipes and `NO_COLOR`. Sort by CPU
  with `7`. The detail pane shows exact CPU% + resident memory, and `--json`
  carries `cpu`/`rss`. Sourced from a separate `ps -o pid=,%cpu=,rss=` call
  so the PID-recycle guard's start-time parse stays byte-for-byte untouched;
  a missed reading degrades to 0 rather than dropping the row.

## [0.1.14] - 2026-07-04

### Added
- Themes: `default`, `light`, `nord`, `dracula`, `solarized`, and `mono`
  (colorblind-friendly — no risk colors, bold still marks high). `t` cycles
  them live in the TUI; `theme = <name>` in `~/.config/le/config` persists
  one. Unknown names warn before the TUI takes the screen and fall back to
  default. The `?` overlay gains a settings section: active theme, refresh
  cadence, and the config path.

### Changed
- README and `--help` now state the trust rule outright — no signal without
  proven identity; refusal over guessing — and the stop-strategy lists
  include the launchctl route. Prompted by a reviewer pointing out that
  people comparing le to `lsof | kill` will miss exactly this.


## [0.1.13] - 2026-07-03

### Added
- launchd awareness. A listener whose PID is a user-domain launchd job
  (`launchctl list`) is now owned by `launchd` in the table, and its stop is
  routed through the supervisor — `launchctl bootout gui/<uid>/<label>` —
  instead of a TERM that a KeepAlive agent would immediately undo by
  respawning. Brew-managed services keep the `brew services stop` route
  (brew is the launchd front-end for those), refused system/app rows stay
  refused but now NAME their launchd label, and `le stop` re-verifies the
  label still maps to the scanned PID immediately before the bootout — the
  same guard shape as Docker's container-ID re-check.

### Changed
- TUI: visual hierarchy pass. Every row leads with a risk-colored gutter
  rail; the RISK cell carries its color (bold above low) instead of tinting
  the port number; pid/dir/owner recede so identity and the stop command
  read first. The header shows a risk pulse (`2 high · 3 medium`) and the
  active filter query; the detail pane hints `(c copies)` on the stop line;
  the stop confirmation leads with `⚠`.
- TUI: table cells pad by display width, not rune count — CJK identities
  (8 columns in 4 runes) no longer shift every column after WHAT.

## [0.1.12] - 2026-07-03

### Added
- TUI: `o` opens the selected listener's port in your browser, and `c`
  copies its stop command to the clipboard via OSC 52 — which means the
  copy works even when `le` is running on a remote box over SSH.
- The TUI shows a DIR column too (on terminals at least 118 columns wide),
  sortable with `6` — folders at a glance in the live view, not just the
  detail pane.
- `le list` shows a DIR column — the listener's working directory,
  home-abbreviated (`~/code/app`) and truncated from the left so the
  project-identifying tail of the path survives. "Which project is this?"
  no longer requires `--json` or the TUI's detail pane.

### Internal
- The release pipeline moved to goreleaser (same tarball names/contents);
  with a `TAP_GITHUB_TOKEN` secret configured, the Homebrew tap updates
  automatically on release.

## [0.1.11] - 2026-07-02

### Added
- `le stop --json` emits per-listener outcomes (pid, identity, ports, action,
  ok, error) as a JSON array — and combines with `--dry-run` for a structured
  preview. Closes the `le list --json` / `le stop` scripting asymmetry the
  round-3 review flagged.

## [0.1.10] - 2026-07-02

### Internal
- Release tarballs now carry a signed build-provenance attestation, verifiable
  with `gh attestation verify <tarball> --repo alikatgh/le-cli`.
- Status badges (CI / release / license) on the README; `.editorconfig` for
  the repo's non-Go files; the `brew services list` parse is now unit-tested.

## [0.1.9] - 2026-07-02

### Fixed
- `le list --json` could emit `"ports": null` / `"addrs": null` on a row whose
  address lsof couldn't resolve, inconsistent with every other row's `[]` —
  breaking `jq`/JSON consumers on that one row. Both are always arrays now.
- A Homebrew-managed Ollama reported its owner as "terminal" instead of
  "homebrew", contradicting its `brew services stop` action.
- A quoted config filter (`filter = "node"`) kept the quote characters and
  matched nothing; surrounding quotes are now stripped.
- A mouse click in the empty space below the last visible TUI row could select
  an off-screen listener.
- A failed `brew services stop` / `docker stop` could show an empty error
  message when the tool itself wasn't found; the underlying error is now
  surfaced.
- Clearer restart hints for a raw (non-Homebrew) database and an open wildcard
  listener.

## [0.1.8] - 2026-07-02

### Fixed
- The TUI confirm dialog could stop the wrong process: a background refresh
  landing while the dialog was open could move a different listener under the
  cursor before you pressed `y`. The dialog now pins the row you selected.
- `le` mis-handled a Homebrew formula whose Cellar path had non-lowercase
  casing — the profile degraded and `brew services stop` could be refused for
  a running service. The formula name is now normalized to lowercase.
- `le ready <invalid-port>` falsely reported success and `le wait
  <invalid-port>` hung; both now reject a non-numeric or out-of-range port.
- `le list <filter> --json` emitted `null` instead of `[]` when nothing
  matched, breaking `jq`/JSON consumers.
- Process classification: binaries like `python-config`/`rubygems` were
  misread as interpreters, and a root process with a daemon name anywhere in
  its path was misflagged as a system daemon. Both now match precisely.

## [0.1.7] - 2026-07-02

### Fixed
- `le stop --dir .` (and any relative path, or `--dir /`) matched zero
  listeners — the path wasn't absolutized before comparison. Found by an
  adversarial review of the v0.1.6 changes.
- `le wait`/`le ready` with a `--timeout` shorter than ~400ms always timed out
  even when the port was already in the target state.
- `le stop` could falsely refuse every process as "recycled" under a
  non-English locale (`LC_TIME`), because `ps` renders its start-time column
  differently; `ps`/`lsof` now run under `LC_ALL=C`.
- The recycle guard's no-start-time fallback compared only the executable
  basename, so a recycled PID sharing an interpreter (`node`, `python`) could
  be signalled; it now requires a full command match and refuses otherwise.

## [0.1.6] - 2026-07-02

### Added
- `le stop --dry-run` (`-n`) prints exactly what a stop would act on — process,
  pid, and strategy — without touching anything. Handy to preview a `--dir`
  sweep before running it for real.
- `le list [filter]` narrows the table to rows matching the filter text across
  port / name / command / folder / owner — the same match the TUI's `/` uses,
  now available one-shot for scripts (`le list node --json`).
- `le wait` / `le ready` take `--timeout` (`-t`): bound the wait and exit
  non-zero if it elapses, so a script can `le ready 5432 -t 30s || fail`
  instead of hanging forever.

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

[Unreleased]: https://github.com/alikatgh/le-cli/compare/v0.1.15...HEAD
[0.1.15]: https://github.com/alikatgh/le-cli/compare/v0.1.14...v0.1.15
[0.1.14]: https://github.com/alikatgh/le-cli/compare/v0.1.13...v0.1.14
[0.1.13]: https://github.com/alikatgh/le-cli/compare/v0.1.12...v0.1.13
[0.1.12]: https://github.com/alikatgh/le-cli/compare/v0.1.11...v0.1.12
[0.1.11]: https://github.com/alikatgh/le-cli/compare/v0.1.10...v0.1.11
[0.1.10]: https://github.com/alikatgh/le-cli/compare/v0.1.9...v0.1.10
[0.1.9]: https://github.com/alikatgh/le-cli/compare/v0.1.8...v0.1.9
[0.1.8]: https://github.com/alikatgh/le-cli/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/alikatgh/le-cli/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/alikatgh/le-cli/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/alikatgh/le-cli/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/alikatgh/le-cli/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/alikatgh/le-cli/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/alikatgh/le-cli/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/alikatgh/le-cli/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/alikatgh/le-cli/releases/tag/v0.1.0
