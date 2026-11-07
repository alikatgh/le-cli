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
be the process you scanned. Before supported actions it rechecks the captured
start time, or requires an exact full command line when start time is unavailable
(and a launchd bootout re-verifies the
label→PID mapping). Docker published ports, names and IDs do not prove a local
engine owns a listener, so Docker actions are refused with a manual inspection
reason. An ambient Docker context may select a remote engine.
Fast-recycling PIDs are exactly the
boring edge case where naive port-killers hit the wrong process.

Missing or changed identity evidence prevents the action.

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
git clone https://github.com/alikatgh/le-cli.git
cd le-cli
go build -o le .
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

### Portable text tools

The native Mac app's 14 text utilities also have portable CLI commands in
**le 0.1.29 or later**. They read UTF-8 from stdin and write to stdout; they do
not read or replace your clipboard. Input and output are each limited to 1 MiB.
Invalid input produces no partial result. JSON nesting is limited to 128.

```sh
printf '{"ready":true}' | le text json
printf 'hello' | le text base64-encode
printf '%s' 'aGVsbG8=' | le text base64-decode
printf '%s' '/path with spaces+plus' | le text url-encode
printf '%s' '/path%20with%20spaces+plus' | le text url-decode
printf 'one two\n' | le text count
printf 'abc' | le text sha256
le text epoch-to-date --seconds=-1
```

All commands: `uuid`, `json`, `epoch`, `epoch-to-date`, `plain`,
`base64-encode`, `base64-decode`, `url-encode`, `url-decode`, `count`,
`sha256`, `lorem`, `gitignore`, and `password`, under `le text`.
Generated passwords use 24 cryptographically random bytes; send the output only
to your intended private destination. Plain and decoded text retain exact
content. Scalar values and reports end in LF. Base64 input is canonical, allowing
ASCII whitespace; URL decoding preserves a literal `+`. Zero and negative whole
Unix seconds are supported within years 0001 through 9999.

`le check PORT` retains HTTP/HTTPS and IPv4/IPv6 loopback support, while refusing
redirects, DNS redirection, proxy settings and cookies. It applies one five-second
budget across detection and the request, caps aggregate response headers at
32 KiB and eight interim responses, and does not retain response bodies.
Self-signed HTTPS is a loopback development diagnostic, not remote TLS trust.

### Datacenter evidence

CLI 0.1.30 adds read-only views over your existing
[Prometheus](https://prometheus.io/docs/prometheus/latest/querying/api/),
[NVIDIA DCGM Exporter](https://github.com/NVIDIA/dcgm-exporter) and
[NetBox](https://netboxlabs.com/docs/netbox/integrations/rest-api/) installations.
These remain the sources of truth; no replacement monitoring stack or background
agent is installed. Start with invented offline data:

```sh
le datacenter summary --demo
le datacenter fleet --demo --json
le datacenter gpu --demo --site demo-east
le datacenter inventory --demo --details --json
```

Live collection requires an explicitly approved `--prometheus` and/or `--netbox`
URL, or `LOCALHOST_PROMETHEUS_URL` / `LOCALHOST_NETBOX_URL`. Tokens are
environment-only: `LOCALHOST_PROMETHEUS_TOKEN` and `LOCALHOST_NETBOX_TOKEN`.
NetBox requires a read-only token restricted to approved devices. Run through
your existing Varlock integration, for example
`npx -y varlock@1.23.0 run --path .env.schema --inject vars --filter 'LOCALHOST_*' -- le datacenter summary --json`; the committed
`.env.schema` contains names only. Do not place tokens in URLs, shell history,
source files or chat.

Use `--site`, `--rack` and `--host` for exact filters. Fleet reports CPU, RAM,
root filesystem and network observations; GPU reports DCGM utilization,
framebuffer, temperature and power. Source `up` and sample age accompany the
values. Missing/stale observations remain explicit; zero utilization does not
prove reclaimable capacity. Host falls back to exporter `instance` when no host
label exists. Names do not automatically join Prometheus and NetBox identities.
Inventory imports only approved operational context and returns aggregate counts
unless `--details` explicitly requests device/site/rack/owner names.

HTTPS is required except for literal loopback HTTP. Redirects and proxy settings
are ignored. Each view has a 1 MiB response budget; a summary collects up to
three views. NetBox source pagination
stops at 512 records, before local filtering. Narrow the server-side approved
cohort for larger inventories. The whole command has a maximum 30-second
deadline. Summary may return partial source status with exit 1; an individual
failed view emits no partial data. Exporter label conventions and upstream
query/resource limits remain the operator's responsibility.

### Database tools

`le db query`, `le db schema` and `le db backup` support the explicit engine
names `postgres`, `mysql`, `mariadb`, `mongodb` and `redis`. Query/schema run on
macOS, Linux and Windows; vendor backups currently run on macOS and Linux.
They require an explicit single `--address host:port` and `--database NAME`
(or `LOCALHOST_DB_ADDRESS` and `LOCALHOST_DB_DATABASE`). Remote connections use
verified TLS automatically; `--tls` or `--ca /path/to/ca.crt` also enable it for
literal loopback. Credentials use `LOCALHOST_DB_USERNAME` and
`LOCALHOST_DB_PASSWORD` through your existing secret loader, never argv.

```sh
le db schema postgres --demo --json
le db query mongodb --demo
printf 'SELECT id FROM records LIMIT 20' | le db query postgres --address 127.0.0.1:5432 --database app
printf '{"active":true}' | le db query mongodb --address 127.0.0.1:27017 --database app --collection records --json
printf '["GET","example"]' | le db query redis --address 127.0.0.1:6379 --database 0 --json
```

Only stdin carries a query (64 KiB UTF-8 maximum). SQL uses a single prepared
read statement in a read-only transaction. Mongo accepts Find filters, excluding
server scripts/aggregation/writes. Redis accepts a bounded read-command allowlist;
there are no scripts, writes or automatic key scans. Schema returns SQL column
metadata, Mongo collection names/types, or Redis keyspace metadata, respectively.
Results stop at 1,000 rows / 1 MiB. Driver operations have five-second deadlines,
and SQL metadata is limited to 256 columns. Mongo SCRAM authentication rejects
server work factors above 100,000 iterations before key derivation.
The maximum whole operation budget is ten seconds; reading stdin requires EOF
and precedes that budget. There are no operation retries or host discovery.
Use least-privilege read-only accounts: privileged SQL functions, filesystem
access and external server side effects are **not sandboxed**. Failed/oversized
reads produce no partial stdout. `--demo` is invented, makes no requests and
ignores environment credentials and stdin.

Backups invoke independently installed `pg_dump`, `mysqldump`, `mariadb-dump`,
`mongodump` or `redis-cli`. They create an absolute **new** owner-private file;
existing files are never replaced. The defaults are 64 MiB and one minute,
with hard maxima of 256 MiB and five minutes. Failure preserves the partial file
and reports `complete:false`; never treat it as a usable backup. A trusted
`--binary /absolute/vendor/path` can select the utility. MySQL/MariaDB and Mongo
require an **existing** owner-private `--credentials-file` in the vendor's
format. The CLI validates its scope and never writes a credentials profile.
PostgreSQL/Redis passwords use the child environment. Remote Redis backups
require a client supporting `--tls-expected-peer-name`; older clients fail
closed. Windows backups fail closed pending process-tree containment support.

```sh
le db backup postgres --address 127.0.0.1:5432 --database app --output /absolute/new/app.dump --json
```

Redis RDB snapshots include **all logical databases**, even when `--database`
selects one index, and replication may trigger server BGSAVE. Backups can create
server load and expose sensitive data; use an explicitly approved target and
appropriate backup role. Timeouts stop the owned vendor process group on Unix,
but cannot bound OS-uninterruptible filesystem I/O. Vendor consistency guarantees,
permissions, utility compatibility and a separate restore test remain necessary.

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

Source builds use a checkout so the small, source-visible driver safety patches
in [third_party](third_party/README.md) are included. `go install ...@version`
cannot apply local module replacements; use Homebrew, release archives or the
checkout build above.

Dependency copyright and license texts are included in
[THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt), also shipped in every archive.
This includes the MySQL driver's MPL 2.0 terms; its unmodified source remains
available at the module/version links recorded there.

The optional datacenter pilot builds on this free CLI and existing monitoring
tools; it does not change the CLI MIT license or promise a deployed fleet service.
See [the pilot overview](https://localhostexplorer.com/datacenter) for current scope.
The paid Mac offer remains US$99 once, pending verified App Store availability;
datacenter pilot scope and pricing are discussed separately.
