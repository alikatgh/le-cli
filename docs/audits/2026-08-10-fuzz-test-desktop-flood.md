# The fuzz test that opened ~876 Terminal windows

**Date:** 2026-08-10 · **ID:** LE-CLI-016 · **Status:** fixed, verified

## What you saw

Two unrelated-looking symptoms, one afternoon:

1. **Hundreds of windows.** Empty `Terminal — login — 80×24` windows, hundreds
   of Finder windows all showing *Macintosh HD*, and dozens of Chrome tabs
   pointed at `localhost:3000`, `localhost:3004`, `localhost:4016`. Mission
   Control was a solid mosaic. The Finder Window menu was an endless list of
   identical "Macintosh HD" entries.
2. **Garbage in the shell.** The Ghostty prompt filling by itself with
   `;62;38M65;62;38M65;…`, growing whenever the mouse moved over the window.

They have different causes. Both live in `le`.

## Evidence

`last` counts tty logins per minute on 2026-08-10:

```
178  01:47      113  01:15       73  01:22
175  01:42      108  01:40       72  01:39
122  01:14       35  01:43
```

**876 login shells** in bursts, inside a window that ends at 01:48 — the exact
minute the working session on `le-cli` stopped writing to its transcript. Not a
cron job (`crontab -l` is empty), not a LaunchAgent, nothing in `.zshrc`, and
nothing in shell history. The bursts are `go test` runs.

## Cause 1 — the tests pressed the keys that shell out

`le`'s TUI binds three keys to actions that leave the process:

```go
// ui/ui.go
openURL        = func(url string) error  { exec.Command("open", url).Start() }         // key: o
revealPath     = func(path string) error { exec.Command("open", "-R", path).Start() }  // key: F
openTerminalAt = func(dir string) error  { exec.Command("open", "-a", "Terminal", dir).Start() } // key: T
```

They are package-level `var`s precisely so tests can swap them out. The targeted
tests in `ui_test.go` do exactly that — stub, assert on the captured argument,
restore.

The **randomised** tests do not. They press every key in the map, 120 times per
case, across 30 case combinations:

```go
// ui/invariants_test.go:128
keys := []string{
    "j", "k", "g", "G", "tab", "esc", "enter", "z", "Z", "f", "c", "x", "n",
    "1", "2", "3", "4", "5", "6", "7", "r", "?", "F", "T", "left", "right",
}
```

`F` and `T` are in that list. Unstubbed. So every `F` opened a real Finder
window and every `T` opened a real Terminal window — at machine speed.

Instrumented count for a single `go test ./ui/` run:

```
BLAST RADIUS: browser=19 finder=39 terminal=43 total=101
```

**101 real desktop launches per test run.** At ~43 Terminal windows a run,
the 876 observed logins is about 20 runs — which is what an afternoon of
iterating on a TUI looks like.

The bitter part: this exact shape is already in the bug journal as LE-CLI-015.
That fix stubbed `favorites` — the one side effect that had caused visible pain —
and left `o`/`F`/`T` live, in the same key list, with a comment above it
explaining why stubbing matters.

### Fix

Stop stubbing at the call site. Neuter the hooks for the entire test binary:

```go
// ui/launchers_guard_test.go — compiled ONLY into the test binary
func init() {
	openURL        = func(string) error { return nil }
	revealPath     = func(string) error { return nil }
	openTerminalAt = func(string) error { return nil }
}
```

`init()` in a `_test.go` file runs before any test in the package, costs the
shipped binary nothing, and a new test cannot fail to opt into it. Tests that
want to observe a launch still stub the hook they care about — they just start
from a no-op instead of from a live `exec.Command`.

## Cause 2 — the terminal was left in mouse-reporting mode

Unrelated to the tests. `Run` starts Bubble Tea like this:

```go
p := tea.NewProgram(New(opts), tea.WithAltScreen(), tea.WithMouseCellMotion())
```

`WithMouseCellMotion` switches **the terminal** into mouse-reporting mode.
Bubble Tea restores it when `Run` returns or panics — but not when the process
is killed without unwinding. The terminal then keeps reporting every mouse
*move* as input, forever, into whatever reads that tty next. That is the
`;62;38M65;` flood: SGR mouse reports (`ESC[<35;62;38M`) landing on a shell
prompt with the escape prefix eaten by the renderer.

It is worse than cosmetic. In the byte-encoded mouse modes a coordinate is sent
as `32 + value`, so screen positions decode to **printable ASCII**: column 47
is `O`, column 52 is `T`, column 38 is `F`. A mouse sweep over a terminal stuck
in that mode can "type" real letters into whatever is reading stdin.

### Fix — and an honest accounting of what it covers

The first version of this fix was a `defer` in `Run` with a comment claiming it
covered "SIGKILL, SIGHUP, a crash that takes the group down". That comment was
wrong, and wrong in the direction that matters: **a `defer` does not run on
SIGKILL, and it does not run on Go's default SIGHUP death either.** It cannot
cover the case it claimed to.

What the exits actually look like, checked against bubbletea v1.3.10:

| exit | restored by |
|---|---|
| normal return, panic | Bubble Tea |
| SIGINT, SIGTERM | Bubble Tea's own handler (`tea.go:286`) |
| early error return from `Run` | **the new `defer`** |
| SIGKILL, default SIGHUP | nothing in-process — impossible |

So the `defer` stays, because it is one idempotent write and it closes the
error-return path, but it is not the interesting half:

```go
defer fmt.Fprint(os.Stderr, ResetTerminal)
```

The `kill -9` case is unfixable from inside the dying process, so recovery has
to come from a **second** process. That is the new command:

```
le fix-terminal
```

It writes mouse-off (1000/1002/1003/1006/1015), bracketed-paste-off, cursor-on
and leave-alt-screen — every one idempotent, so it is a no-op on a healthy
terminal. `reset` also works and always has; `fix-terminal` exists because it
is discoverable from the tool that caused the problem, and because `reset`
additionally clears the scrollback, which people are reasonably unwilling to do
mid-debug.

`cmd/fix_terminal_test.go` asserts the escape bytes literally rather than
against the constant the implementation uses — a typo in an escape sequence
raises no error and produces a command that silently fails at its only job.

### If a shell is stuck right now

```sh
le fix-terminal
# or, without le:
printf '\e[?1000l\e[?1002l\e[?1003l\e[?1006l\e[?1015l\e[?2004l\e[?25h\e[?1049l'
# or, if you don't mind losing scrollback:
reset
```

## Verification

- `go build ./...` clean, `go vet ./ui/` clean, `go test ./...` all packages ok.
- Terminal window count before and after a full `go test ./ui/` run: **0 → 0**
  (was 43 per run).
- The randomised tests still press `o`/`F`/`T` — that coverage is the point;
  it is now absorbed by the guard.

## Lessons

1. **Stub the world once at the binary boundary, not per call site.** A
   per-test stub only protects the tests that remembered it. A test-binary
   `init()` protects the ones nobody has written yet.
2. **A fuzz test inherits every side effect of every key it presses.** Before
   handing random input to a state machine, enumerate what that machine can do
   to things outside the process.
3. **A TUI borrows global state that outlives the process.** Anything you
   switch on — mouse modes, alt screen, bracketed paste — needs a restore path
   on every exit you can observe, and an out-of-process recovery for the exits
   you cannot (`kill -9`). "It's in a defer" is not the same as "it's covered".
4. **Verify the failure mode you name in a comment.** The first version of this
   fix claimed a `defer` handled SIGKILL. Reading bubbletea's source produced a
   correct table of who restores what, and turned a half-true one-liner into a
   real fix plus a real recovery command.
4. **`last` is a debugger.** Bursts of tty logins per minute pinned the cause to
   a minute-accurate window and ruled out cron, LaunchAgents and shell rc files
   before a line of code was read.
