# CLI session review — round 2 (2026-07-02)

Loop-until-dry follow-up to the first review. Confirms the five fixes introduced no
regressions and extends coverage to intel classification, the TUI, JSON/table output,
deeper scan orchestration, and error/exit-code handling.

- Candidates raised: 6
- Survived adversarial verify: 6 (6 CONFIRMED, 0 PLAUSIBLE)

## Surviving findings (most severe first)

### 1. [HIGH] brewFormula() gates on a lowercased copy of cmd but extracts the formula name from the original-case cmd via cellarRe, so the returned formula string can retain uppercase characters instead of always being lowercase.
- **File:** `intel/intel.go` line 548 (finder: intel-classify)
- **Verdict:** CONFIRMED
- **Failure scenario:** Given a Cellar-style path whose formula segment isn't all-lowercase (e.g. a mixed-case tap/symlink or non-standard casing in argv, such as '/opt/homebrew/Cellar/REDIS/7.2.0/bin/redis-server'), brewFormula returns "REDIS" instead of "redis". Downstream this breaks two things: (1) in Make() (intel.go:227), env.BrewStarted[formula] misses because brewStarted() (intel.go:101-117) populates the map with brew's own lowercase formula names, so managedBrew is wrongly false and the profile's confidence/messaging degrades; (2) the literal comparisons `formula == "redis"`, `formula == "postgresql"`, `formula == "mysql"`, `formula == "mariadb"`, `formula == "ollama"` (lines 245-262) fail to match, dropping the process out of the high-confidence database/AI branch. Worse, if the branch is still entered via the `strings.Contains(text, ...)` fallback, `database()`/the ollama branch still sets `StopKind=StopBrew, StopArg=formula` with the wrong-case value, and kill.Stop's pre-stop re-check `intel.BrewServiceKnown(p.StopArg)` (kill.go:52, intel.go:215 `strings.Fields(line)[0] == formula`) does an exact case-sensitive string compare against brew's lowercase output — so the stop is refused with "brew formula %q is no longer known to brew services" even though the service is actually running and managed by brew.
- **Verifier:** Verified directly against /Users/s_avelova/Documents/Codex/2026-05-24/le-cli/intel/intel.go. brewFormula (lines 548-561) lowercases cmd only to test containment (`low := strings.ToLower(cmd)`, line 549) but runs `cellarRe.FindStringSubmatch(cmd)` (line 553) against the original-case `cmd`, so the captured group m[1] — and the returned formula — retains original casing. Empirically compiled and ran the exact function: for cmd = \"/opt/homebrew/Cellar/REDIS/7.2.0/bin/redis-server\", brewFormula returns \"REDIS\", not \"redis\".\n\nDownstream impact is real and unguarded — grepped the whole file and confirmed there is no `strings.ToLower(formula)` normalization anywhere before formula is used:\n1. Make() (intel.go:227): `managedBrew := formula != \"\" && env.BrewStarted[formula]` — BrewStarted is populated by brewStarted() (intel.go:101-117) using brew's own lowercase names (`m[f[0]] = true` from `brew services list` output), so the uppercase key misses and managedBrew is wrongly false.\n2. The literal case comparisons at intel.go:245-262 (`formula == \"redis\"`, `formula == \"postgresql\"`, `formula == \"mysql\"`, `formula == \"mariadb\"`) fail for uppercase formula, degrading confidence scoring (via `pick(formula == \"redis\", 96, 90)` etc.) even when the Contains(text, ...) fallback still enters the branch.\n3. If StopKind=StopBrew is set with the wrong-case formula (e.g. intel.go:268 or :309), kill.Stop (kill/kill.go:52) calls `intel.BrewServiceKnown(p.StopArg)`, which does `strings.Fields(line)[0] == formula` (intel.go:215) — an exact case-sensitive compare against brew's lowercase output — so it returns false and kill.Stop refuses with \"brew formula %q is no longer known to brew services\" (kill.go:53), even though the service is running and brew-managed.\n\nAll mechanisms in the finding check out against the real code with a concrete reproducible trigger; nothing refutes it.

### 2. [HIGH] Confirm dialog does not pin the row being confirmed; a background scan or tick landing while `m.confirm` is true can silently retarget the pending stop action to a different process.
- **File:** `le-cli/ui/ui.go` line 148 (finder: ui-tui)
- **Verdict:** CONFIRMED
- **Failure scenario:** User presses `x`/`s` on row R (e.g. PID 4821, 'node dev-server'), `m.confirm` becomes true and the footer shows 'Stop node dev-server? -> TERM'. The periodic tick (default 3s, `tickCmd`/`tickMsg` at ui.go:145-146) or a concurrent scan from an earlier `r` keypress or a prior stop's post-stop refresh completes and delivers `scannedMsg` — Update()'s `scannedMsg`/`tickMsg` cases (ui.go:124-146) run unconditionally with no `if m.confirm` guard anywhere in Update, unlike onKey which does guard. The handler replaces `m.all`, rebuilds+re-sorts `m.view` via `applyFilter()`/`sortView()`, and re-clamps `m.cursor` via `clamp()` (ui.go:138-143). If a listener appeared/disappeared or the active sort column re-ranks rows, the Row now sitting at index `m.cursor` is a different process than the one the user selected and is still described in the (now stale, unrendered-until-next-frame) footer text. Pressing `y`/`enter` then calls `m.selected()` (ui.go:177, using `m.cursor`) which returns this new Row and issues `stopCmd(r)` against it — `kill.Stop` only re-validates that the row's own captured PID/StartTime haven't been recycled, it has no notion of 'is this the listener the user actually picked', so a legitimate, unrelated, currently-running process can be SIGTERM'd/`brew services stop`ped/`docker stop`ped instead of the one the user intended to confirm.
- **Verifier:** Verified by reading le-cli/ui/ui.go directly. The mechanism is real and exploitable:

1. `onKey` (ui.go:171-185) guards `m.confirm` for keypresses only — pressing `x`/`s` sets `m.confirm = true` (line 243) but stores no snapshot of the target Row, only the bool flag and the unchanged `m.cursor`.
2. `Update()`'s `scannedMsg` case (ui.go:124-143) and `tickMsg` case (ui.go:145-146) have zero `if m.confirm` guard, unlike `onKey`. `tickMsg` unconditionally re-fires `scanCmd()` every `m.interval` (default 3s, ui.go:32/107-109) regardless of dialog state.
3. When a `scannedMsg` lands, `m.all` is replaced and `m.applyFilter()` (ui.go:141) rebuilds `m.view` from scratch via `sortView()` — a fresh OS scan (`scan.Scan()`, confirmed to build an all-new `[]Listener` with no identity tracking to prior state) can change row count/order (new listener appears, one disappears, ports change, sort re-ranks). `m.clamp()` (ui.go:142, defined 281-301) only bounds `m.cursor` to `[0, len(m.view)-1]` — it has no concept of "follow this specific row," so `m.cursor` can end up pointing at an entirely different Row.
4. `m.selected()` (ui.go:373-378) is `m.view[m.cursor]`, re-evaluated live both by `footerView()` (line 557, every render) and by the confirm-resolution branch in `onKey` (line 177, `case "y","Y","enter"`). Neither captures the Row at confirm-open time.
5. `stopCmd(r)` (line 179) is called with whatever `m.selected()` returns at the moment `y`/`enter` is pressed — i.e., the row now at `m.cursor`, which may differ from the row the user actually confirmed.
6. `kill.Stop` (kill/kill.go:42-72) only re-validates that the *given* Row's own PID/StartTime (and container ID for Docker) haven't been recycled — it has no notion of "is this what the user selected," so a legitimate different process can be SIGTERM'd/brew-stopped/docker-stopped.

Concrete trigger: user on row PID 4821 at cursor index 5 presses `x` → confirm dialog shown. Before pressing `y`, the periodic 3s tick (or a scan from an earlier `r` press, or the post-stop `scanCmd()` at line 154) delivers `scannedMsg`; a listener elsewhere appears/vanishes or the active sort re-ranks, so `m.view[5]` after `applyFilter()`+`sortView()` is now a different process. User presses `y` → `stopCmd` fires against the new row at index 5, stopping an unintended process. This is a genuine TOCTOU/no-pinning bug, not already guarded anywhere in the code."

### 3. [HIGH] WaitListening (le ready) and WaitFree (le wait) rely on Free(port), which returns false for ANY bind failure — including an invalid/out-of-range port string — not just 'something is listening'. This makes 'le ready <bad-port>' falsely report success (exit 0) instantly, and 'le wait <bad-port>' hang forever (or time out with a misleading message) since the port can never be observed as bound/free.
- **File:** `tools/tools.go` line 74 (finder: errors-exit)
- **Verdict:** CONFIRMED
- **Failure scenario:** `le ready 99999` (or `le ready 70000`, or `le ready abc`) immediately prints "port 99999 is already listening" and exits 0, even though nothing is listening — net.Listen fails with 'invalid port' for any port outside 0-65535 or a non-numeric string, and Free() collapses that failure into the same boolean as 'port occupied'. A script doing `le ready $PORT && curl http://localhost:$PORT` with a miscomputed/typo'd $PORT will proceed as if the server were up. Symmetrically, `le wait <bad-port>` (no --timeout) blocks forever because Free() can never return true for that port string, and with --timeout it exits 1 with the misleading message 'timed out ... waiting for port to free' instead of reporting the real problem (invalid port argument).
- **Verifier:** Verified by reading tools/tools.go and reproducing at runtime. Free() at tools/tools.go:19-26 does:
```go
func Free(port string) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return false
	}
	...
}
```
Any net.Listen error — invalid/out-of-range port ("99999", "70000", "-1") or non-numeric port ("abc") — collapses to `false`, identical to the "something is bound there" case. Verified with a standalone Go program that `net.Listen` on 127.0.0.1 returns `listen tcp: address 99999: invalid port`, `address 70000: invalid port`, and `lookup tcp/abc: unknown port` respectively — none of these indicate the port is occupied.

cmd/cmd.go passes `args[0]` straight through with no validation: `waitCmd` (line 166) calls `tools.WaitFree(args[0], timeout)` and `readyCmd` (line 179) calls `tools.WaitListening(args[0], timeout)` directly from cobra args, with no port-range/numeric check anywhere in cmd.go.

WaitListening (tools/tools.go:74-85) checks `if !Free(port) { ...already listening...; return nil }` — since Free("99999") returns false, this fires immediately.

Built the binary and reproduced live:
- `le ready 99999` → prints "port 99999 is already listening" and exits 0 (confirmed, nothing was listening).
- `le ready abc` → prints "port abc is already listening" and exits 0.
- `le wait 99999 --timeout 1s` → blocks for the full timeout then prints "Error: timed out after 1s waiting for port 99999 to free" and exits 1 — the misleading message described in the finding, masking the real problem (invalid port argument).

All claimed behaviors reproduce exactly as described; the finding is accurate and the bug is not guarded anywhere in the call path.

### 4. [MEDIUM] isInterpreter() uses strings.HasPrefix on the basename instead of an exact/word-boundary match, so non-interpreter binaries whose name merely starts with "python" or "ruby" (e.g. python-config, python-build, rubygems, rubocop) are misidentified as interpreters.
- **File:** `intel/intel.go` line 431 (finder: intel-classify)
- **Verdict:** CONFIRMED
- **Failure scenario:** A command line containing a binary like `python-config` or `rubocop` as the first interpreter-like token, followed later in argv by `-m <module>` or a `.py`/`.rb`-suffixed argument (plausible for lint/build wrapper tools that take a target path), causes interpreterIdentity() to treat it as a real Python/Ruby interpreter run and fabricate a plausible-but-wrong Identity/Kind/restart command, unlike every other classification in this file which uses the word-boundary-safe wordMatch helper.
- **Verifier:** Confirmed by extracting and executing the actual logic from intel/intel.go. isInterpreter() (line 431-435) uses strings.HasPrefix(b, \"python\") and strings.HasPrefix(b, \"ruby\") on the basename, unlike wordMatch (line 490) used elsewhere in the file. Concrete trigger: cmd = \"python-config -m http.server 8000\" — isInterpreter(\"python-config\") returns true (HasPrefix matches), interpreterIdentity() then calls canonicalInterp which forces interp=\"python3\", and returns id.restart=\"python3 -m http.server 8000\", id.title=\"http.server\" — a fabricated, wrong identity/restart command for a process that was never python3. Same for \"python-build --target build.py\" -> restart=\"python3 build.py\". Also \"rubygems update mygem.rb\" -> restart=\"ruby mygem.rb\", since rubygems starts with \"ruby\". These are real-world binary names (python-config ships with Python dev headers, python-build is a pyenv component, rubygems is a real gem). One inaccuracy in the candidate: rubocop does NOT trigger this since strings.HasPrefix(\"rubocop\",\"ruby\") is false (rubo != ruby) — verified programmatically. But the core mechanism and at least two of the claimed example binaries (python-config, python-build) plus rubygems do trigger real misclassification, so the finding is confirmed with a minor correction on one example.

### 5. [MEDIUM] isSystem()'s root-daemon branch substring-matches daemon names (rapportd, sharingd, controlce, launchd, mdnsresponder) against the entire lowercased command line, excluding only /cellar/ and /users/ paths, so any root-run process whose path or arguments merely contain one of those substrings elsewhere (not just as the binary name) is misclassified as a genuine macOS system daemon.
- **File:** `intel/intel.go` line 578 (finder: intel-classify)
- **Verdict:** CONFIRMED
- **Failure scenario:** A root-run script or listener at a path like `/private/tmp/launchd-test/server` or `/opt/scripts/mdnsresponder_helper.py` (not under /Cellar/ or /Users/) is classified as isSystem()==true purely because "launchd"/"mdnsresponder" appears as a substring in the full command line, forcing StopKind=StopAvoid and Risk=High on what is actually an ordinary user-run root process — the opposite of the intended narrow daemon allow-list.
- **Verifier:** Verified in /Users/s_avelova/Documents/Codex/2026-05-24/le-cli/intel/intel.go lines 572-587. The isSystem() root-daemon branch is:

```go
c := strings.ToLower(l.CommandLine)
...
if l.User == "root" && !strings.Contains(c, "/cellar/") && !strings.Contains(c, "/users/") {
	switch {
	case strings.Contains(c, "rapportd"), strings.Contains(c, "sharingd"),
		strings.Contains(c, "controlce"), strings.Contains(c, "launchd"),
		strings.Contains(c, "mdnsresponder"):
		return true
	}
}
```

`c` is the full lowercased command line (binary path + all arguments), and each check is an unanchored `strings.Contains`, not a match against just the executable's basename. The only exclusions are `/cellar/` and `/users/` substrings — nothing scopes the match to the binary name.

Concrete trigger: a root-owned process at a path such as `/private/tmp/launchd-test/server` (no `/Cellar/` or `/Users/` in the path) would have "launchd" as a substring of its command line, satisfy `l.User == "root"`, pass both exclusion checks, and hit `strings.Contains(c, "launchd")` → `isSystem()` returns `true`. Likewise a root process invoked with an argument containing "mdnsresponder" (e.g., `/opt/scripts/helper --tag=mdnsresponder_proxy`) would also be misclassified. This forces the ordinary root-run process into the genuine-macOS-daemon path (used elsewhere to set StopKind/Risk more conservatively), which is the wrong classification for what is actually a user-controlled process — matching the candidate finding's claimed failure exactly."

### 6. [MEDIUM] filterRows returns a nil slice when no rows match, so `le list <filter> --json` emits JSON `null` instead of `[]`, inconsistent with the unfiltered zero-listener case which correctly emits `[]`
- **File:** `cmd/cmd.go` line 100 (finder: output-scan-depth)
- **Verdict:** CONFIRMED
- **Failure scenario:** Run `le list nonexistent-filter --json`; `filterRows` starts with `var out []row` and never appends, returning nil. `printJSON` -> `json.NewEncoder.Encode(nil-slice)` writes the literal 4 bytes `null`. Any script or `jq` pipeline expecting an array (e.g. `le list foo --json | jq '.[]'` or `JSON.parse(out).length`) breaks: `jq` errors with "Cannot iterate over null (null)" (exit 5), and JS `.length`/`.map()` throws on null. By contrast `le list --json` with zero total listeners returns `[]` (because `gather()` uses `make([]row, len(listeners))`, which is non-nil even when `listeners` is nil), so the two "empty result" paths behave differently for the same CLI in JSON mode. Fix: initialize `out := make([]row, 0, len(rows))` in filterRows instead of `var out []row`.
- **Verifier:** Verified by reading cmd/cmd.go directly. filterRows (line 100-115) declares `var out []row` (line 105) and only appends inside the loop on a match; if zero rows match, out remains nil and is returned unchanged (no reinitialization). printJSON (line 214-218) calls json.NewEncoder(w).Encode(rows) with no nil-check. Confirmed via a standalone Go repro that json.Encoder.Encode on a nil slice writes the literal `null`, not `[]`. This is asymmetric with the unfiltered zero-listener path: gather() (line 201-212) always does `rows := make([]row, len(listeners))`, which is a non-nil (empty) slice even when listeners is nil, so `le list --json` with zero listeners correctly emits `[]`. Concrete trigger: `le list nonexistent-filter --json` where no row's port/identity/command/commandline/cwd/source contains the substring "nonexistent-filter" — filterRows returns nil, printJSON emits `null`, breaking `jq '.[]'` (errors "Cannot iterate over null") or JS `JSON.parse(out).length` (throws). The suggested fix (`out := make([]row, 0, len(rows))`) directly addresses the bug. No guard exists anywhere in the code path that would prevent this.

## Refuted candidates

_none_
