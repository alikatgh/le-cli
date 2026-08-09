# Bug Journal — le (CLI)

Cheap to write, cheap to read, expensive to skip. Grep this before debugging
a new symptom; append an entry **in the same commit as the fix**.

The macOS sibling has its own, larger journal at
`../../localhostexplorer/le/docs/BUG_JOURNAL.md` (or the `le` repo) — many bugs
here have a twin there because the two share classification/stop logic. Check
both when a fix lands on shared behavior.

---

## Patterns to scan for FIRST

Generalized bug shapes. Grep here before reproducing anything.

- **A UI test that presses keys presses the ones with SIDE EFFECTS too.** A
  randomised key-sequence test included `f` (pin a port), which persists — so
  it wrote six synthetic ports into the developer's real
  `~/Library/Application Support/le/favorites`. The damage surfaced as three
  unrelated sort tests failing locally while CI stayed green, because a pinned
  row floats to the top of every sort. Stub every persistence path before
  driving keys, and when local tests fail but CI passes, suspect state on the
  machine rather than the code. (LE-CLI-015)
- **Making names human introduces collisions; uniqueness is a property of the
  LIST, not the row.** Naming processes after their bundle turned three rows
  into three identical "Antigravity IDE"s. Disambiguate across the whole
  filtered set — never the visible window, or a label changes as you scroll —
  and only where there IS a collision, so a unique name stays clean.
  (LE-CLI-014)
- **When the cursor indexes a DERIVED list, every index you stored is now
  wrong.** Adding group headers made screen lines ≠ rows, and the pin handler
  still did `for i, row := range m.view { m.cursor = i }` — putting the cursor
  on whatever line happened to share that index. Grep every `cursor =` when the
  list model changes, and re-find by identity (PID), never by remembered
  position. (LE-CLI-013)
- **A fold must summarise what it hides.** Collapsing eight rows behind
  "app · 8" moves the problem unless the header also names the ports inside —
  "what's on 42050?" must not require unfolding first. Same rule for any
  progressive-disclosure UI. (LE-CLI-013)
- **A test that asserts rendered ANSI escapes is a test of your terminal, not
  your code.** lipgloss strips styling when stdout is not a TTY, so an
  escape-matching assertion passed under a forced-colour run and failed under
  plain `go test`. Extract the decision (`whatStyleFor`, `riskStyleFor`) and
  assert `Style.GetBold()`. (LE-CLI-012)
- **Data you already collected but never render is the cheapest feature you
  will ever ship.** Eight rows read "App helper" while `/Applications/OneDrive.app`
  sat unread in the command line the whole time. Before adding a source, check
  what the existing one already knows. (LE-CLI-012)
- **Column arithmetic must be done in DISPLAY WIDTH, once, in one place.** The
  TUI learned this and `le list` did not, so the moment intel started naming
  apps — putting 企业微信 in the WHAT column — the plain table skewed 4 columns
  on that row while the TUI was fine. Two renderers, two implementations, one
  of them wrong. `internal/textw` is now the only one. Corollary: **size a
  column from the widest cell in the WHOLE filtered set, never the visible
  window**, or the columns twitch as the list scrolls. (LE-CLI-012)
- **Rank visual weight by what the user can DO, not by severity.** A screen of
  high-risk system helpers rendered nine bold red rails while the three
  processes the user actually controls receded — the UI shouted loudest where
  shouting is useless. Weight follows actionability; colour still carries risk.
  Do it in weight/glyph rather than hue so it survives mono themes and
  NO_COLOR. (LE-CLI-012)
- **A pane's height is a BUDGET, not a suggestion.** The layout reserves
  `detailHeight` lines for the detail box and sizes the table around it, so one
  extra line inside the pane pushed the whole view one row past the terminal
  height and scrolled the header off. Put per-state hints in the FOOTER (which
  already swaps content by mode) and assert `len(View()) <= h` in a test.
  (LE-CLI-011)
- **A scripted `.replace(..., 1)` patches the FIRST match, which may be code
  you added seconds earlier.** Inserting `m.paneIdx = 0` into "the j case"
  landed it inside the new pane-focus block instead of the table's, so the
  field cursor reset itself on every keypress and stuck at 1. When patching by
  pattern, anchor on surrounding lines unique to the target — or read the
  result back before believing it. (LE-CLI-011)
- **Forcing `LC_ALL=C` to stabilize a PARSE also mangles the DISPLAY.** ps and
  lsof escape every byte >= 0x80 under the C locale, in two different
  notations (`M-dM-<M^A` vs `\xe4\xbc\x81`), so a Chinese/Cyrillic/emoji-named
  app rendered as line noise everywhere — TUI, `le list`, `--json`. Keep the
  locale (the lstart parse depends on it) and decode on the way in; guard the
  decode by accepting it only when the result is valid UTF-8 that gained a
  multi-byte rune, so ordinary ASCII argv containing "M-d" is never corrupted.
  General shape: **a locale/format you force for machine-readability is still
  reaching a human — decode before display.** (LE-CLI-009)
- **A subprocess that exits non-zero on a NORMAL result can't have its error
  blindly swallowed OR blindly surfaced.** `lsof -iTCP -sTCP:LISTEN` exits **1
  on an empty match** — so `out, _ := runCmd(...)` (swallow) hides a real lsof
  failure as "no listeners", but `if err != nil { return err }` (surface)
  turns the normal empty case into an error. Distinguish *couldn't run*
  (`*exec.Error` / not-found) from *ran and exited non-zero* (`*exec.ExitError`)
  via `errors.As`; only the former is a failure. (LE-CLI-002)
- **Classify on word boundaries, not substring — and remember the haystack
  includes the CWD.** `Make()` builds `text` from `Command + CommandLine + Cwd`,
  so `strings.Contains(text, "mongod")` tagged any project under
  `~/mongodb-dashboard` as MongoDB. Use `wordMatch` (bounded by `_`/`a-z`/`0-9`)
  — the real daemon is literally `mongod`, so it still matches. (LE-CLI-001)
- **Act on immutable IDs; re-verify only the mutable handles.** A recycle guard
  that confirms `name -> id` at check time is defeated by then running the
  action `by name` — the name can be freed and reassigned in the TOCTOU window.
  `docker stop` the **container ID**, not the name (fall back to name only when
  no ID was captured). (LE-060) Twin of the mac app's docker-restart fix.
- **Force `LC_ALL=C` on any subprocess whose output you PARSE by position.**
  `ps lstart` is rendered via `LC_TIME`; a non-English locale changes the token
  count/word order and breaks the fixed-offset parse feeding the recycle guard.
- **A generated artifact that is only regenerated by hand WILL go stale — put
  the check in CI or don't commit the artifact.** `man/` was documented as "run
  gendocs and commit the diff"; eleven commands shipped with no man page and
  `le-hold`'s described the pre-range behavior. The fix isn't diligence, it's a
  CI job. Corollary: **a drift check needs a deterministic generator** — cobra
  stamps `time.Now()` into the `.TH` header, so an unpinned date turns the check
  red on the 1st of every month and trains everyone to ignore it. Second
  corollary: **regenerate-and-diff only catches what the generator WRITES.**
  `GenManTree` overwrites but never deletes, so a removed command leaves a
  tracked, unmodified orphan that `git diff` calls clean — clear the output
  directory first and check `git status --porcelain` (M + ?? + D), not `git
  diff`. (LE-CLI-003)
- **A background refresher must not commit a result the world has moved past.**
  `CachedScheme` probes in a goroutine, so a probe outlives the state it was
  started for and lands on top of newer state (a cleared cache, a swapped
  prober) — which is exactly how a leftover goroutine made another package's
  test flaky. Capture a generation counter when the work starts; only write if
  it still matches. A mutex protects the map, not the ordering, so `-race` will
  never catch this. (LE-CLI-006)
- **A fallback that succeeds quietly is how a step goes missing for a month.**
  release.yml skipped the Homebrew tap bump with one log line and a green
  check when `TAP_GITHUB_TOKEN` was absent — so v0.1.16 published and brew
  users sat on v0.1.15 until someone happened to look. Once a capability is
  expected, its absence is a failure, not a branch: the job now fails (after
  publishing and attesting, so nothing is lost). Applies to any
  "configured? then do the extra thing" step. (LE-CLI-008)
- **A test stub for shared state must tolerate being called by goroutines it
  didn't start.** The stub for LE-CLI-006 did `close(started)` on every call;
  a leftover `CachedScheme` goroutine from an earlier test called it a second
  time and panicked the whole binary with "close of closed channel" — only at
  `-count=50`, so a single run looked fine. Guard one-shot signals with
  `sync.Once`. Corollary: **a test for a background-goroutine bug is itself
  exposed to background goroutines** — stress it (`-count=50`), don't trust one
  green run. (LE-CLI-007)
- **Anything a script branches on is a contract; if it isn't pinned by a test
  it will drift.** Exit codes and `--json` key names are read by machines, and
  both can be broken by an edit that looks local (a struct tag three packages
  away, an `os.Exit(1)`). Golden tests + `docs/COMPATIBILITY.md`, or don't
  promise. (LE-CLI-004)
- **One exit code for every failure makes a waiting command unscriptable.**
  `Execute` exited 1 for everything, so `le wait -t 30s` couldn't tell a caller
  "still busy, retry" from "you typo'd a flag". Separate *misuse* (2) from
  *ran-and-failed* (1) from *deadline elapsed* (124, per `timeout(1)`) — and
  carry the distinction as a wrapped sentinel (`tools.ErrTimeout`) placed as the
  message's FIRST verb, so `%w` preserves the human text verbatim. (LE-CLI-005)

---

## Chronological log
### 2026-08-09 — randomised TUI invariant test, and the config it trashed (LE-CLI-015)
- **Where:** `ui/invariants_test.go` (new).
- **Why:** three cursor mechanics landed in one day (pane focus, grouping with folded headers, content-sized columns) and they all mutate the same state. Unit tests cover each alone; this drives them together with random key sequences, background scans, and resizes, asserting cursor range, `selected()`/`items[cursor]` agreement, `viewIdx` range, pane focus only on rows, and a non-empty render.
- **Proved non-vacuous** by planting two regressions: deleting the pane-focus normalisation in `clamp()` and making `selected()` ignore headers. Both were caught, with the exact key trail printed.
- **Self-inflicted:** the key list includes `f`, which persists a pin. Unstubbed, the test wrote six synthetic ports into the real favorites file; pinned rows float to the top of every sort, so three long-standing sort tests started failing locally while CI stayed green. Deleted the file, added `stubFavoritesDir(t)`.
- **Lesson:** "passes here, passes in CI" and "fails here, passes in CI" are both signals about the MACHINE. Before blaming a diff, ask what the test wrote outside its temp dir.

### 2026-08-09 — three rows named "Antigravity IDE" (LE-CLI-014)
- **Where:** `internal/label` (new), `ui/group.go` (`rowLabels`, `listItem.viewIdx`), `ui/ui.go` (table + header), `cmd/cmd.go` (printTable).
- **Symptom:** the app-naming work (LE-CLI-012) replaced "Editor language service" ×3 with "Antigravity IDE" ×3 — more meaningful, equally indistinguishable.
- **Fix:** append the helper binary only when a listing actually collides AND the helpers differ; two rows running the same binary stay clean because the port column already separates them. Platform noise (`_macos_arm`) is trimmed so the suffix fits the column.
- **Plumbing note:** grouping had already made line index ≠ row index, so labels computed across `m.view` must be looked up via `listItem.viewIdx`, not the line number. Second time that distinction has bitten in one day.
- **Also:** the header now counts stoppable listeners alongside the risk pulse — and omits the count when everything is stoppable, since restating `len(view)` is not information.

### 2026-08-09 — group the list by owner (LE-CLI-013)
- **Where:** `ui/group.go` (new), `ui/ui.go` (the cursor now indexes `m.items`, not `m.view`), `config/config.go` (`group` key). Tests: `ui/group_test.go`, `config/config_test.go`.
- **Why:** 15 listeners, 12 of them background helpers the user is told not to touch. Grouped, the same machine reads as 9 lines with all three stoppable processes visible.
- **Risk and its guards:** folding HIDES ports, so grouping is opt-in (`z`, or `group = true`); only all-unactionable groups of 3+ fold by default; an active filter expands everything (a search that hides a match is a lie); a pinned port keeps its group open; and folded headers list their ports.
- **The refactor's sharp edge:** once headers exist, a screen line is not a row. `selected()` returns false on a header (so every row action refuses instead of hitting a neighbour), and the pin handler's `m.cursor = i` over `m.view` had to become a re-find by PID over `m.items`.
- **Lesson:** a test asserting "focus drops when you move onto a header" was unwritable as stated — while the pane is focused, j/k move between FIELDS, so the cursor cannot walk onto a header at all. The real path is a REBUILD changing what a line index means. Write the test for the mechanism that actually exists.

### 2026-08-09 — the table said "App helper" eight times (LE-CLI-012)
- **Where:** `intel/appname.go` (new) + the app/editor branches of `intel/intel.go`; `internal/textw` (new); `cmd/cmd.go` printTable; `ui/ui.go` table widths, rails, weights.
- **Symptom:** on a real machine, 8 of 15 rows read "App helper" and 3 read "Editor language service" — the table could not distinguish OneDrive from BlueStacks from 企业微信, and STOP repeated "avoid — inspec…" on 12 rows in the widest column.
- **Cause:** the product name was in the command line all along (`/Applications/<Product>.app/…`, or the `Application Support/<Vendor>` segment) and was simply never read.
- **Two bugs the fix exposed:** `le list` padded with fmt's `%-Ns` (rune count), so a CJK app name skewed every column right of WHAT by 4; and its 7-wide PORT column overflowed on `44950 +1`. Both now go through `internal/textw`, shared with the TUI so the two renderers cannot disagree again.
- **Lesson:** verify a display change against REAL data — the CJK skew and the port overflow were both invisible in fixtures and obvious in one `le list` on the machine that reported the problem.

### 2026-08-09 — detail pane gains field focus (LE-CLI-011)
- **Where:** `ui/panefocus.go` (new), `ui/ui.go` (model fields, key routing, pane render, footer). Tests: `ui/ui_test.go`.
- **Why:** row-level actions could only ever pick ONE reveal target per row and could never reach a row's second port — the `+1`/`+2` extras in the table were unreachable. Tab now focuses the pane, j/k step fields, Enter acts.
- **Height bug I introduced and caught:** the "what Enter does" hint started as a line INSIDE the pane, which pushed the view one row past the terminal height at every size (main fit exactly; mine overflowed by 1). Moved to the footer; `TestViewFitsTerminalHeight` now guards it.
- **Cursor bug I introduced and caught:** a scripted patch put `m.paneIdx = 0` in the pane block's own `j` case rather than the table's, so the field cursor reset on every press and never advanced past 1. Found by printing the field list and cursor per keystroke instead of guessing.
- **Lesson:** focus state must be visible WITHOUT colour and must not change geometry — a reserved 2-column gutter (a caret plus a space, versus two spaces) does both, and a width-equality test across focus states keeps it honest.

### 2026-08-09 — non-ASCII process names rendered as line noise; detail pane had no actions (LE-CLI-009/010)
- **Where:** `scan/unescape.go` (new), `scan/scan.go` (parsePSCommandLines + the lsof `c` field), `ui/ui.go` (F/T keys, copy picker i/d/a, revealHint). Tests: `scan/unescape_test.go` (incl. fuzz), `ui/ui_test.go`.
- **LE-CLI-009 symptom/cause:** a WeCom listener showed `cmd /Applications/M-dM-<M^AM-dM-8M^Z…` in the pane, `le list`, and `--json`. Our own `LC_ALL=C` (needed for the lstart parse behind the recycle guard) makes ps escape non-ASCII in vis meta notation and lsof as `\xHH`. Decoded both on the way in; verified against the live process.
- **LE-CLI-009 gotcha:** `M^A` (no dash) is what ps actually emits for meta-control; my first decoder handled only `M-^A`, and the valid-UTF-8 guard then silently returned the ORIGINAL — a correct-looking no-op. A guard that falls back to the input hides decoder bugs: assert decoded values against real captured strings.
- **LE-CLI-010:** the pane stated facts with no way to act on them, unlike the app's clickable rows — worst on an "avoid — inspect first" row, which advised inspection while offering none. Added `F` reveal (binary for a helper with a useless container cwd, else the folder), `T` new terminal, and picker entries `i` context inspect / `d` cd / `a` one-liner.
- **Lesson:** an affordance the UI names ("inspect first") must exist as a keystroke, or the advice is decoration. Also: sample rows are SORTED before display — a test that picks a row by index tests whichever row the sort put there, not the one you meant. Select by identity.

### 2026-08-09 — the four gaps a "is this world class?" audit surfaced (LE-CLI-003/004/005)
- **Where:** `cmd/exit.go` (new), `cmd/cmd.go` (Execute, `usageArgs`, groups), `tools/tools.go` + `tools/exit_open.go` (sentinels), `internal/gendocs/main.go`, `.github/workflows/ci.yml`, `docs/COMPATIBILITY.md` (new). Tests: `cmd/exit_test.go`, `cmd/json_contract_test.go`.
- **LE-CLI-003 (man drift):** `man/` held 7 pages for 18 commands and goreleaser ships that directory — the manual regen step was skipped for a year. CI now runs gendocs and fails on a dirty `man/`; gendocs pins its date so the check can't false-positive on a month rollover.
- **LE-CLI-005 (exit codes):** every error exited 1. Now 0/1/2/124, mapped in `exitCodeFor` from `tools.ErrTimeout`, `tools.ErrInvalidPort`, and a `usageError` wrapper; `usageArgs` covers arg-count AND unknown-command (cobra's `NoArgs` is what emits the latter), `SetFlagErrorFunc` covers bad flags. Verified against the built binary, not just unit tests.
- **LE-CLI-006 (flaky test, found while verifying the above):** `ui`'s `TestOpenKeyDetectsHTTPS` passed alone and failed under `go test ./...` — a `CachedScheme` goroutine from an earlier test finished after `SetTLSProbeForTesting` cleared the cache and wrote `http` over it, so the stubbed https prober was never consulted. `scan/scheme.go` now guards the commit with a generation counter; `TestStaleProbeDoesNotClobberCache` fails without the guard (verified by reverting it).
- **LE-CLI-004 (contract):** `docs/COMPATIBILITY.md` states what's stable; golden tests pin the JSON key sets and the codes. Command sprawl (18, flat) addressed by cobra groups — nothing removed, but the four core commands are no longer buried; `TestEveryCommandIsGrouped` keeps new ones from silently landing in "Additional Commands".

### 2026-07-05 — adding ps columns near the recycle-guard parse (CPU/mem feature)
- **Where:** `scan/scan.go` readPS.
- **Lesson:** when adding non-safety-critical fields (%cpu, rss) to ps output, use a SEPARATE ps call, not a reorder of the `pid,lstart,user` call — lstart feeds the PID-recycle guard and its parse depends on exact field offsets around the 5-token date. A separate call for two fixed single-token numerics keeps that parse byte-for-byte untouched and lets the new fields degrade to 0 (decorative) instead of joining the row's all-or-nothing drop rule.


Newest first. 5 lines max each. File:line, symptom, cause, fix, lesson.

### 2026-07-05 — P1 sprint from the full-repo audit: 3 CLI fixes (LE-060, LE-CLI-001/002)
- **Where:** `kill/kill.go` (`StopDocker` branch), `intel/intel.go:330` (mongo case), `scan/scan.go:49` (`Scan()` error handling). Tests: `kill/kill_docker_id_test.go`, `intel/mongo_wordmatch_test.go`, `scan/scan_error_test.go`.
- **LE-060:** `docker stop` ran `p.StopArg` (name) even though the guard verified `p.StopArgID` (id) — a name freed/reused between scan and stop bounces the wrong container. Now stops by the immutable ID (name fallback only when no ID captured); still reports the friendly name.
- **LE-CLI-001:** `strings.Contains(text, "mongod")` false-positived on any cwd like `~/mongodb-dashboard` because `text` includes `Cwd`. Switched to `word("mongod")`; real `mongod` still matches, `mongodb-*` folders don't.
- **LE-CLI-002:** `out, _ := runCmd("lsof", …)` swallowed the error, so an lsof that can't run looked identical to "no listeners" (and `le stop` said "nothing listening"). Now surfaces a real exec failure while preserving the empty case — `errors.As(err, *exec.ExitError)` distinguishes exit-1-on-empty from can't-run.
- **Lesson:** the audit's own proposed LE-CLI-002 patch (`if err != nil && out == "" { return err }`) was wrong — lsof exits 1 on empty, so it would have errored on every clean "nothing listening". Verify the fix, not just the finding. Go toolchain wasn't on the machine; correctness rests on review + CI (`go test -race`, macOS+ubuntu). Full report: `docs/audits/` and `../../audits/2026-07-05-localhostexplorer/`.
