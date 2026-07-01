# Contributing

## Building and testing

```sh
go build ./...
go vet ./...
gofmt -l .            # should print nothing
go test -race ./...
```

`ci.yml` runs exactly these four on every push and PR, on both Linux and
macOS — a PR that fails one of them won't merge cleanly. There's no
separate linter config right now, so `go vet` + `gofmt` are the bar.

## Project layout

```
scan/    enumerate TCP listeners via lsof + ps (pid, cmd, cwd, start time)
intel/   identify the process + pick the stop strategy + risk level
kill/    execute the strategy after a PID-recycle re-check
tools/   hold / wait / ready port helpers
ui/      the Bubble Tea TUI
cmd/     the Cobra command tree (list/stop/hold/wait/ready/version)
config/  ~/.config/le/config loading
```

## Before opening a PR

- Add a test for the behavior you're changing — most packages here are
  pure functions (parsing, matching, formatting) with no I/O, so this is
  usually straightforward; see the existing `*_test.go` files for the
  pattern (e.g. build the input by hand rather than shelling out to real
  `ps`/`lsof`).
- If you're touching the PID-recycle guard, the Docker/Homebrew
  re-verification in `kill/`, or anything that decides what gets signalled,
  say so explicitly in the PR description — that's the code where a subtle
  regression is most expensive.
- Keep `gofmt` happy; CI will fail otherwise.

## Reporting bugs

Open a GitHub issue with your OS, `le --version`, and steps to reproduce.
For anything security-related, see [SECURITY.md](SECURITY.md) instead.
