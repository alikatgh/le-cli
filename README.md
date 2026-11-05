<div align="center">

<img src="docs/assets/logo.svg" width="88" alt="le logo — 127, the loopback address">

# le — Localhost Explorer for the terminal

**See what's listening. Stop it the right way.**

[![ci](https://github.com/alikatgh/le-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/alikatgh/le-cli/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/alikatgh/le-cli?sort=semver)](https://github.com/alikatgh/le-cli/releases/latest)
[![license](https://img.shields.io/github/license/alikatgh/le-cli)](LICENSE)

`brew install alikatgh/tap/le` · [full tour](https://localhostexplorer.com/cli.html)

</div>

A fast, keyboard-driven TUI for seeing what's listening on your machine and
stopping it the right way. The terminal sibling of the
[Localhost Explorer](https://localhostexplorer.com) menu bar app — same
intelligence (it knows when `kill -9` won't stick), in a single static binary
that also runs on Linux servers over SSH.

<img src="docs/assets/le-tui.svg" width="100%" alt="le running in a terminal: a live table of listeners with port, process, directory, risk, owner, and the stop command that works; a detail pane explains the selected Flask dev server">

## Why

`lsof -i :3000` tells you a PID. It doesn't tell you that the PID belongs to a
Homebrew service that launchd will respawn the moment you `kill -9` it. `le`
identifies the owner and picks the stop strategy that actually works:

- plain process → `SIGTERM`
- Homebrew service → `brew services stop <formula>`
- Docker/container networking helpers → manual inspection; automatic container
  commands and helper signals require verified local engine and process ownership
- launchd user agent → `launchctl bootout gui/<uid>/<label>` (a KeepAlive
  agent respawns if you kill the PID — the supervisor is the thing to stop)
- system / app helpers → flagged with their launchd label, and it refuses
  to auto-kill them

**The rule underneath all of it: no signal without proven identity.** This is
the difference from `lsof | kill` — the point isn't sending the signal, it's
refusing to send one when the process behind a PID can no longer be proven to
be the process you scanned. Every stop re-verifies the PID's start time
immediately before supported actions (and a launchd bootout re-verifies the
label→PID mapping). Docker published ports, names and IDs do not prove a local
engine owns a listener, so Docker actions are refused with a manual inspection
reason. An ambient Docker context may select a remote engine.
Fast-recycling PIDs are exactly the
boring edge case where naive port-killers hit the wrong process.

Every stop re-checks the PID's start time first, so a recycled PID is never the
one that gets signalled.

## Install

No Go toolchain needed — every path below ships a prebuilt binary
(macOS, Linux and Windows; amd64 + arm64).

One line, no package manager — downloads the latest release, verifies its
checksum, installs it ([read the script](install.sh) first if you sensibly
distrust `curl | sh`):

```sh
curl -fsSL https://localhostexplorer.com/install.sh | sh
```

Or by hand from the [Releases page](https://github.com/alikatgh/le-cli/releases):

```sh
tar -xzf le_*.tar.gz && sudo mv le /usr/local/bin/
```

Each release tarball carries a signed [build-provenance
attestation](https://docs.github.com/actions/security-guides/using-artifact-attestations-to-establish-provenance-for-builds)
— verify it came from this repo's CI before trusting it:

```sh
gh attestation verify le_*.tar.gz --repo alikatgh/le-cli
```

Homebrew (also installs man pages and shell completions):

```sh
brew install alikatgh/tap/le
```

Windows — grab `le_<version>_windows_amd64.zip` (or `_arm64`) from the
[Releases page](https://github.com/alikatgh/le-cli/releases), unzip, and put
`le.exe` somewhere on your `PATH`. It runs in Windows Terminal, PowerShell
and cmd; nothing to install alongside it. Two things work differently there,
both on purpose — the DIR column is empty and a windowless console server
can't be stopped gracefully — see
[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md#platforms) for the why.
The TUI Terminal action treats folder names with spaces and shell punctuation
literally when a folder is available.

From source (Go 1.27.3 recommended; module minimum Go 1.26.0):

```sh
go install github.com/alikatgh/le-cli@latest   # installs as `le-cli`; rename to `le` if you like
# or:
git clone https://github.com/alikatgh/le-cli && cd le-cli && go build -o le .
```

Binaries built with Go 1.27.3 require macOS 13 (Ventura) or later, per the
[Go platform requirements](https://go.dev/doc/go1.27#darwin). Existing release
artifacts retain the requirements of their original build toolchain.

Needs `lsof` and `ps` on macOS and Linux (preinstalled on macOS; available
on every Linux), and `netstat` + PowerShell on Windows (both built in).
Homebrew and launchd detection are best-effort — missing tools are skipped.
Docker engines are not queried; recognized container helpers require manual handling.

## Use

```sh
le                 # launch the live TUI (default)
le list            # print a one-shot table and exit (alias: le ls)
le list node       # …filtered to rows matching "node" (port/name/cmd/dir/owner)
le list --json     # structured output for scripts / jq
le stop 3000       # stop whatever is on port 3000, the smart way
le stop 1183       # …or by PID
le stop --dir .    # stop every listener whose working dir is under this path
le stop 3000 --json  # …with per-listener results as JSON (works with -n too)
le stop --dir . -n # dry run: show what --dir would stop, without stopping it
le hold 3000       # squat a port so nothing else can grab it (Ctrl-C frees)
le wait 5432       # block until a port frees up
le ready 8080      # block until something starts listening (open-when-ready)
le ready 8080 -t 30s  # …but give up after 30s and exit 124 (for scripts)
le companion       # optional foreground connection for the Mac app (macOS)
```

`le --help` groups the full command set by what it's for — see what's
listening / stop it the right way / wait on a port / macOS housekeeping — and
every command has its own help: `le <command> --help`.

### Optional Mac app companion

Run `le companion` explicitly when you want the Mac app to inspect host processes
and request supported actions. Select its private connection file in the app;
keep the Terminal command running and press Ctrl-C to disconnect. No background
agent is installed. See the [protocol and limits](docs/COMPANION.md). The Mac
app's standalone tools and ordinary CLI commands work without this connection.

### Inspect a stable snapshot

Press `p` to pause or resume. The header shows live/paused state and snapshot
age. `r` takes one fresh snapshot while staying paused; an already-running
live scan cannot replace the snapshot after pausing. Refresh requests share
one scan at a time, with at most one pending refresh.

Selection follows the PID **and start time** through refresh and sorting. If
the process disappears or its PID is reused, choose a row again before acting.
When the start time is unavailable, selection survives only with an unchanged
full command line; a short process-name fallback does not establish identity.

The detail pane distinguishes **observed**, **inferred** and **unavailable**
evidence for listener, command, directory, start time, classification and owner.
The same provenance is available as additive `profile.evidence` in
`le list --json`. Identity scores remain heuristic rule scores, not measured
probabilities. Container helper names are hints; their engine/container ownership
remains unavailable. Missing or default-zero resource readings
do not establish a successful measurement.

This inspection approach takes inspiration from the evidence and snapshot
workflow in [gputaskman](https://github.com/shanselman/gputaskman) (MIT); `le`
keeps its existing collectors and stop-safety checks.

### In scripts

```sh
le ready 5173 && open http://localhost:5173   # open the browser the moment Vite is up
le wait 5432 && pg_ctl start                   # restart Postgres once the port clears
le ready 5432 -t 30s || echo "db never came up"  # bounded wait, non-zero on timeout
```

Exit codes are part of the contract, so a script can tell "not yet" from "no":

| code | meaning |
|-----:|---------|
| `0` | success |
| `1` | failure — nothing listening, stop refused, scan failed |
| `2` | usage error — unknown command/flag, bad args |
| `124` | a `--timeout` elapsed (same convention as `timeout(1)`) |

```sh
le wait 3000 -t 30s || { [ $? -eq 124 ] && echo "still busy"; exit 1; }
```

The exit codes and the `--json` field names are the two surfaces le promises
not to break silently — both are documented in
[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) and pinned by golden tests.

### Shell completions

The Homebrew install sets these up for you. Installing another way, wire them
up manually:

```sh
le completion zsh  > "${fpath[1]}/_le"                     # zsh
le completion bash | sudo tee /etc/bash_completion.d/le   # bash
le completion fish > ~/.config/fish/completions/le.fish   # fish
```

### Config

Optional `~/.config/le/config` (or `$XDG_CONFIG_HOME/le/config`):

```
interval = 2      # TUI refresh seconds (default 3)
filter   = node   # initial TUI filter (default none)
group    = true   # start grouped by owner (default false)
theme    = nord   # default / light / nord / dracula / solarized / mono /
                  # msdos / system7 / gameboy / phosphor / amber / paper /
                  # blueprint / vaporwave
```

Themes restyle the whole TUI — `light` for light-background terminals,
`mono` for colorblind-friendly no-color output (bold still marks high
risk). The retro block ports the mac app's native themes, so the same
identity travels between surfaces: `msdos` is CGA-on-Norton-Commander,
`system7` one-bit Macintosh, `gameboy` four shades of DMG olive,
`phosphor`/`amber` CRT glow, `paper` e-ink, `blueprint` cyanotype,
`vaporwave` Miami neon. (`system7`, `gameboy`, and `paper` are
dark-on-light — pair them with a light terminal.) `t` cycles them live;
the config line makes it stick. The current
theme, refresh cadence, and config path live in the `?` overlay's
settings section.

### Keys (TUI)

| key       | action                                   |
|-----------|------------------------------------------|
| `j` / `k` | move down / up                           |
| `g` / `G` | jump to top / bottom                     |
| `/`       | filter by port, name, folder (`esc` clears) |
| `1`-`6`   | sort by port / pid / what / risk / owner / dir (press again to reverse) |
| `x`       | stop the selected listener (asks first)  |
| `o`       | open `http://localhost:<port>/` in the browser |
| `c`       | copy the stop command — OSC 52, so it works over SSH |
| `r`       | refresh now                              |
| `t`       | cycle theme (persist with `theme = <name>` in config) |
| `?`       | help                                     |
| `q`       | quit                                     |

Wheel scrolls, left-click selects. The list auto-refreshes.

### If your terminal is left in a weird state

```sh
le fix-terminal
```

The TUI turns on mouse reporting, and turns it back off on every exit it can
observe. A `kill -9` gives it no chance to — and a terminal left in
mouse-reporting mode treats every mouse *move* as input, so your prompt fills
with `;62;38M65;…` whenever you move the mouse. `fix-terminal` also restores a
hidden cursor, bracketed paste and the alt screen. Everything it sends is
idempotent, so it is safe on a healthy terminal, and unlike `reset` it leaves
your scrollback alone.

## Layout

```
scan/    enumerate TCP listeners via lsof + ps (pid, cmd, cwd, start time)
intel/   identify the process + pick the stop strategy + risk level
kill/    execute the strategy after a PID-recycle re-check
tools/   hold / wait / ready port helpers
ui/      the Bubble Tea TUI
cmd/     the Cobra command tree (per-command help + completions)
config/  optional ~/.config/le/config loader
main.go  entry point
```

## Want it always-on?

`le` is the terminal view. If you'd rather have it live in your menu bar with
one click before you kill, that's the companion app: https://localhostexplorer.com

## License

MIT — see [LICENSE](LICENSE).
