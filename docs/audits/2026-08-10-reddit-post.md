# Reddit post — draft

**Best fit:** r/golang (Bubble Tea + Go testing, the audience gets it instantly).
**Also works:** r/programming, r/commandline, r/macapps.

**Title options**
1. My fuzz test opened 876 Terminal windows because it pressed the keys that shell out
2. TIL a randomised TUI test will happily press the key bound to `exec.Command("open", …)` — 101 times per run
3. Two bugs, one afternoon: a fuzz test that flooded my desktop, and a TUI that left my terminal in mouse-reporting mode

**Self-promo note:** r/golang tolerates "here's a bug from my project" posts but
not launch posts. Lead with the bug, keep the tool name to one mention, no link
in the body — put the repo link in a comment if people ask.

---

## Body

My laptop started opening windows on its own. Hundreds of empty Terminal
windows. Hundreds of Finder windows, all showing "Macintosh HD". Dozens of
Chrome tabs on random localhost ports. Mission Control looked like a mosaic
tiled with black rectangles.

Separately, my shell prompt kept filling up by itself with this:

```
;62;38M65;62;38M65;62;38M65;62;38M65;62;38M65;62;38M…
```

growing every time I moved the mouse.

Two different bugs. Both mine. Here's the postmortem.

### Finding the window

No cron job. No LaunchAgent. Nothing in `.zshrc`. Nothing in shell history.

What cracked it was `last`, counting tty logins per minute:

```
178  01:47      113  01:15       73  01:22
175  01:42      108  01:40       72  01:39
122  01:14       35  01:43
```

**876 login shells**, in bursts, ending at 01:48 — the exact minute I stopped
working on my Go TUI. Bursts of 100-180 a minute is not a human. That's a
`go test` loop.

### Bug 1: the fuzz test pressed the keys that shell out

The TUI has three keys that leave the process:

```go
openURL        = func(url string) error  { exec.Command("open", url).Start() }                    // o
revealPath     = func(path string) error { exec.Command("open", "-R", path).Start() }             // F
openTerminalAt = func(dir string) error  { exec.Command("open", "-a", "Terminal", dir).Start() }  // T
```

They're package-level `var`s specifically so tests can swap them out, and the
targeted tests do exactly that — stub, assert on the captured arg, restore.

Then I wrote a randomised invariant test. It presses every key in the map, 120
times per case, over 30 case combinations, checking the cursor never lands
somewhere illegal. Great test. Caught two planted regressions immediately.

Here's the key list:

```go
keys := []string{
    "j", "k", "g", "G", "tab", "esc", "enter", "z", "Z", "f", "c", "x", "n",
    "1", "2", "3", "4", "5", "6", "7", "r", "?", "F", "T", "left", "right",
}
```

`F` and `T` are right there. Unstubbed. Every `F` opened a real Finder window.
Every `T` opened a real Terminal window. At machine speed, in a loop, for as
long as the test ran.

I instrumented it to get the actual number for one `go test ./ui/`:

```
BLAST RADIUS: browser=19 finder=39 terminal=43 total=101
```

**101 real desktop launches per test run.** ~43 Terminal windows a run × ~20
runs in an afternoon ≈ the 876 shells `last` recorded.

The genuinely humbling part: I had already hit this exact bug shape two weeks
earlier. The same test presses `f`, which pins a port to a config file, and it
wrote six junk entries into my real config. I fixed it by stubbing the config
dir and wrote a note in the bug journal titled *"a UI test that presses keys
presses the ones with SIDE EFFECTS too."*

Then I left `o`, `F` and `T` in the same list. Directly below that comment.

### The fix that actually holds

The mistake wasn't forgetting one stub. It's that "remember to stub it" was the
mechanism at all. So I moved the guard to the binary boundary:

```go
// ui/launchers_guard_test.go — compiled ONLY into the test binary
func init() {
	openURL        = func(string) error { return nil }
	revealPath     = func(string) error { return nil }
	openTerminalAt = func(string) error { return nil }
}
```

`init()` in a `_test.go` file runs before every test in the package, ships in
nothing, and a test I write next year can't fail to opt in — it doesn't have to
know the file exists. Tests that want to observe a launch still stub the
specific hook; they just start from a no-op instead of from a live
`exec.Command`.

Terminal windows opened by a full test run: **43 → 0**. The random tests still
press `o`/`F`/`T`, which is the whole point of a fuzz test.

### Bug 2: the terminal left in mouse-reporting mode

Different cause, same afternoon. The program starts like this:

```go
tea.NewProgram(New(opts), tea.WithAltScreen(), tea.WithMouseCellMotion())
```

`WithMouseCellMotion` doesn't put *your program* into mouse mode — it puts **the
terminal** into it. Bubble Tea turns it back off when `Run` returns or panics.
It cannot turn it off if the process is killed without unwinding.

After that, the terminal reports every mouse *move* as input, forever, to
whatever reads that tty next. That's the `;62;38M65;` flood — SGR mouse reports
(`ESC[<35;62;38M`) landing on a shell prompt.

And it's not just cosmetic. In the byte-encoded mouse modes, a coordinate is
transmitted as `32 + value` — so screen positions decode to **printable
ASCII**. Column 47 is `O`. Column 52 is `T`. Column 38 is `F`. A mouse sweep
across a terminal stuck in that mode is typing letters into whatever is reading
stdin. Which, given bug #1, is a fantastically stupid thing to have running on
the same machine.

Fix is one line, unconditional, on the way out:

```go
defer fmt.Fprint(os.Stderr, "\x1b[?1006l\x1b[?1015l\x1b[?1003l\x1b[?1002l\x1b[?1000l")
```

**If your shell is stuck like this right now:**

```sh
printf '\e[?1000l\e[?1002l\e[?1003l\e[?1006l\e[?1015l'   # or just: reset
```

### Takeaways

- A fuzz test inherits every side effect of every key it presses. Enumerate what
  your state machine can do to the world *before* handing it random input.
- Stub the world **once at the binary boundary**, not per call site. A per-test
  stub only protects the tests that remembered it.
- A TUI borrows global terminal state. Everything you switch on — mouse modes,
  alt screen, bracketed paste — gets switched off in a `defer`, not just on the
  happy path.
- `last` is an underrated debugger. Login bursts per minute pinned this to a
  minute-accurate window and eliminated cron, LaunchAgents and rc files before I
  read a line of code.

If you have a TUI with an "open in browser" or "reveal in Finder" action and a
randomised test anywhere near it, go check your key list. Right now.
