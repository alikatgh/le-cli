# Compatibility — what scripts can rely on

`le` is pre-1.0, but "pre-1.0" is not a licence to break the parts people
automate against. This file says exactly which parts are stable, so a script
you write today keeps working — and so a change that would break it is caught
in review instead of in your CI.

Two surfaces are covered: **exit codes** and **`--json` output**. Everything
else — table layout, TUI keys, log wording, colour — is presentation and may
change in any release.

---

## Exit codes

| Code | Meaning | Typical cause |
|-----:|---------|---------------|
| `0` | Success | The thing you asked for happened |
| `1` | Failure | Nothing listening on that port, a stop was refused or failed, a scan couldn't run |
| `2` | Usage error | Unknown command, unknown flag, wrong number of args, malformed port or duration |
| `124` | Timeout | A `--timeout` deadline elapsed. Matches `timeout(1)`, so it composes with existing retry idioms |

The distinction that matters: **124 means "not yet", 1 means "no".** A CI
script retries the first and aborts on the second.

```sh
# wait up to 30s for the dev server's port to free, then start
if le wait 3000 --timeout 30s; then
  npm run dev
else
  case $? in
    124) echo "port 3000 still busy after 30s"; exit 1 ;;
    2)   echo "bad invocation — fix the script"; exit 2 ;;
    *)   echo "le failed"; exit 1 ;;
  esac
fi
```

Commands that can exit 124: `wait`, `ready`, `watch`, `open` — every command
that takes `--timeout`.

Pinned by `cmd/exit_test.go`. Changing a code is a breaking change.

---

## JSON output

`le list --json` and `le stop --json` (including `--dry-run --json`) emit a
**JSON array**, always — `[]` when nothing matches, never `null`. Keys are
lowerCamelCase throughout.

### Guarantees

- **Keys are never renamed or removed** without a major-version bump and an
  entry in this file.
- **New keys may be added at any time.** Parse defensively: don't assume a
  fixed key set, and don't fail on unknown keys.
- **Field types are stable.** A string stays a string.
- **`--json` output goes to stdout, alone.** Warnings, progress, and errors go
  to stderr, so `le list --json | jq` is safe without redirection.
- **Empty is `[]`.** No output mode ever emits a bare `null`.

### `le list --json`

One object per listener:

| Key | Type | Notes |
|-----|------|-------|
| `pid` | number | |
| `command` | string | Short name from lsof |
| `commandLine` | string | Full argv from ps |
| `user` | string | |
| `startTime` | string | `ps lstart` — the recycle key le re-verifies before any signal |
| `cwd` | string | Working directory; `""` when unavailable |
| `cpu` | number | `ps %cpu`; may exceed 100 on multicore |
| `rss` | number | Resident set size, KB |
| `addrs` | array of string | e.g. `127.0.0.1:3000`, `*:5000`, `[::1]:8080` |
| `ports` | array of string | |
| `profile` | object | See below |

`profile` — le's identification of what the process actually is:

| Key | Type | Notes |
|-----|------|-------|
| `identity` | string | Human name, e.g. "Postgres (Homebrew)" |
| `kind` | string | |
| `source` | string | How it was started: terminal, brew, docker, launchd, … |
| `confidence` | number | |
| `risk` | string | |
| `stopKind` | string | Which strategy applies; `avoid` means le refuses to stop it |
| `stopArg` | string | Brew formula or container name |
| `stopArgID` | string | Container short ID — the immutable handle docker stops act on |
| `stopLabel` | string | Human description of the stop action |
| `restart` | string | |
| `note` | string | |
| `warning` | string | |
| `explain` | string | |

### `le stop --json`

One object per targeted listener:

| Key | Type | Notes |
|-----|------|-------|
| `pid` | number | |
| `identity` | string | |
| `ports` | array of string | |
| `action` | string | What ran — or what *would* run, under `--dry-run` |
| `dryRun` | boolean | |
| `ok` | boolean | |
| `error` | string | **Omitted when `ok` is true** |

`--dry-run --json` reports `ok: false` for a listener le would refuse to stop
(`stopKind: "avoid"`), so a preview never promises something a real stop won't
do.

The exit code still applies alongside the JSON: a partial failure prints the
full array **and** exits 1.

Pinned by `cmd/json_contract_test.go` — the key sets are golden, so a struct-tag
change three packages away fails the build here rather than in your pipeline.

---

## What is *not* stable

- The `le list` **table** — column set, widths, glyphs. Use `--json`.
- TUI keybindings, themes, layout.
- Wording of human-facing messages on stdout/stderr. Branch on exit codes, not
  on text.
- Anything in the macOS-housekeeping group (`flush-dns`, `restart-dock`,
  `restart-finder`, `sleep-display`, `keep-awake`): thin wrappers, no output
  contract.

## Changing a contract

1. Update this file in the same commit.
2. Update the golden test (`cmd/json_contract_test.go` or `cmd/exit_test.go`).
3. Note it in the changelog under a **Breaking** heading.

A key rename with no entry here is a bug, not a release.
