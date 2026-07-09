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

---

## Chronological log
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
