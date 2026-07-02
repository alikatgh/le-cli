# le — Localhost Explorer for the terminal

A fast, keyboard-driven TUI for seeing what's listening on your machine and
stopping it the right way. The terminal sibling of the
[Localhost Explorer](https://localhostexplorer.com) menu bar app — same
intelligence (it knows when `kill -9` won't stick), in a single static binary
that also runs on Linux servers over SSH.

```
PORT    PID     WHAT                RISK    OWNER     STOP WITH
3000    38814   juice-shop          medium  container  docker stop juice-shop
8001    43138   Django dev server   low     terminal   TERM
27017   1183    MongoDB             high    homebrew   brew services stop mongodb-community
```

## Why

`lsof -i :3000` tells you a PID. It doesn't tell you that the PID belongs to a
Homebrew service that launchd will respawn the moment you `kill -9` it. `le`
identifies the owner and picks the stop strategy that actually works:

- plain process → `SIGTERM`
- Homebrew service → `brew services stop <formula>`
- Docker container → `docker stop <name>`
- system / app helpers → flagged, and it refuses to auto-kill them

Every stop re-checks the PID's start time first, so a recycled PID is never the
one that gets signalled.

## Install

Prebuilt binaries (macOS + Linux, amd64 + arm64) are attached to each
[release](https://github.com/alikatgh/le-cli/releases):

```sh
# download the tarball for your platform, then:
tar -xzf le_*.tar.gz && sudo mv le /usr/local/bin/
```

Homebrew (also installs man pages and shell completions):

```sh
brew install alikatgh/tap/le
```

From source (Go 1.24+):

```sh
go install github.com/alikatgh/le-cli@latest   # installs as `le-cli`; rename to `le` if you like
# or:
git clone https://github.com/alikatgh/le-cli && cd le-cli && go build -o le .
```

Needs `lsof` and `ps` (preinstalled on macOS; available on every Linux).
Homebrew and Docker detection are best-effort — missing tools are skipped.

## Use

```sh
le                 # launch the live TUI (default)
le list            # print a one-shot table and exit (alias: le ls)
le list --json     # structured output for scripts / jq
le stop 3000       # stop whatever is on port 3000, the smart way
le stop 1183       # …or by PID
le stop --dir .    # stop every listener whose working dir is under this path
le stop --dir . -n # dry run: show what --dir would stop, without stopping it
le hold 3000       # squat a port so nothing else can grab it (Ctrl-C frees)
le wait 5432       # block until a port frees up
le ready 8080      # block until something starts listening (open-when-ready)
```

`wait` and `ready` are handy in scripts:

```sh
le ready 5173 && open http://localhost:5173   # open the browser the moment Vite is up
le wait 5432 && pg_ctl start                   # restart Postgres once the port clears
```

Every command has its own help: `le <command> --help`.

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
```

### Keys (TUI)

| key       | action                                   |
|-----------|------------------------------------------|
| `j` / `k` | move down / up                           |
| `g` / `G` | jump to top / bottom                     |
| `/`       | filter by port, name, folder (`esc` clears) |
| `1`-`5`   | sort by port / pid / what / risk / owner (press again to reverse) |
| `x`       | stop the selected listener (asks first)  |
| `r`       | refresh now                              |
| `?`       | help                                     |
| `q`       | quit                                     |

Wheel scrolls, left-click selects. The list auto-refreshes.

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
