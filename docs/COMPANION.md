# Optional local companion (protocol v1)

`le companion` is an explicit foreground macOS command for an independently
installed, free CLI. It is optional: the Mac app's standalone tools remain
available without it. It installs no agent and stops when its terminal closes.
SIGINT, SIGTERM, and SIGHUP cancel active work and retire the owned connection
file before the foreground command exits.

## Wire contract

The command binds only `127.0.0.1` on an ephemeral port. It writes a unique
owner-readable `.leconnection` JSON file in the private user configuration
directory `le/companion`. The user selects that file in the app. Its exact fields
are `protocolVersion` (integer `1`), `endpoint` (`http://127.0.0.1:PORT`), `token`
(unpadded base64url encoding of 32 random bytes), `serverInstance` (32 lowercase
hex characters), and `cliVersion` (string). The token is a credential; never
print it, include it in diagnostics, or return it from the API. A fresh command
start creates a fresh instance, token, and filename. Exit retires only the exact
descriptor the command still owns.

All requests use `POST`, `Content-Type: application/json`, and
`Authorization: Bearer TOKEN`. All responses are JSON. Browser `Origin` and
`Sec-Fetch-*` headers are rejected, as are a Host other than the exact bound
`127.0.0.1:PORT` and a non-loopback peer. There are no cookies, CORS, redirects,
or unauthenticated routes. Unknown JSON fields, multiple JSON values, malformed
IDs, and unsupported operations are rejected.

* `POST /v1/status` with `{}` returns `protocolVersion`, `serverInstance`,
  `cliVersion`, and `capabilities: ["inspect", "stop", "restartManaged"]`.
* `POST /v1/snapshot` with `{}` returns `snapshotID` (32 lowercase hex),
  `capturedAt` and `expiresAt` (RFC3339 UTC strings), and `rows` (array).
  A row contains `targetID` (32 lowercase hex), the existing flat `le list
  --json` listener fields `pid`, `command`, `commandLine`, `user`, `startTime`,
  `cwd`, `addrs`, `ports`, plus the existing `profile` object. Resource fields
  `cpu` and `rss` are optional: only positive finite CPU up to 1,000,000 percent
  and positive RSS up to 9,007,199,254,740,991 KiB (`Int64.max / 1024`) are sent.
  Out-of-range values are omitted. Legacy collectors cannot distinguish an unavailable
  measurement from an idle/zero value, so either case omits that resource.
  Clients must show unavailable resources accordingly, rather than imply a
  measured zero. `rss` is KiB; native clients multiply by 1024 for bytes.
  `profile.evidence` retains the existing evidence/provenance object. The
  internal `commandLineFallback` flag is retained server-side and is not a wire
  field. Empty results use `rows: []`.
* `POST /v1/action` takes exactly `requestID`, `snapshotID`, `targetID` (each
  32 lowercase hex), and `operation` (`"stop"` or `"restartManaged"`). It returns
  `requestID`, `snapshotID`, `targetID`, `operation`, `state` (`"pending"` or
  `"final"`), `outcome` (`"completed"`, `"refused"`, `"failed"`, or `"unknown"`;
  omitted while pending), and `message` (bounded human-readable string).
  A pending duplicate returns HTTP 202; final receipts use HTTP 200. Completed
  means the selected operation returned successfully; a TERM success says the
  signal was sent, not that process exit was confirmed. Deadline/cancellation
  produces `unknown`; never automatically retry it with a new request ID.

Errors have `error: {code: STRING, message: STRING}`. HTTP 400 denotes malformed
input, 401 authentication, 403 a rejected request origin/host/peer, 404 route,
405 method, 409 stale snapshot/unknown target/conflicting request ID, 413 body
size, 415 media type, 429 busy or receipt capacity, 500 execution failure, and
504 snapshot deadline. Error messages never contain command output or credentials.

One complete snapshot is retained in memory for 120 seconds, with at most 4096
rows and a 4 MiB encoded response. Starting a snapshot invalidates the previous
one, and an accepted action consumes its snapshot before execution. Action
targets are server-owned records; the API never accepts listener/profile data,
PIDs, ports, shell commands, executable paths, argv, environments, timeouts, or
filesystem requests. Clients refresh after every action. The server serializes
engine work without a queue (HTTP 429 on contention). Snapshot work is bounded
to 10 seconds and action work to 12 seconds, including child processes.
Homebrew actions require the exact `homebrew.mxcl.FORMULA` launchd job in the
current user domain to own the selected PID immediately before the operation;
formula membership alone does not authorize stopping a different instance.

Up to 128 request receipts are retained for ten minutes. The same request ID
with the same complete parameters returns the pending/final receipt without
rerunning work; reusing it with different parameters is HTTP 409. When all
receipt slots are still live, new actions are refused instead of evicting a
receipt and weakening at-most-once execution. Restarting the companion resets
receipts and invalidates the old credential and all old snapshot IDs.

## Limits and lifecycle

Headers are limited to 8 KiB and bodies to 16 KiB. Server read-header, read,
write, and idle timeouts are 3, 10, 20, and 30 seconds. Captured subprocess
stdout is limited to 8 MiB and stderr to 16 KiB; cancellation terminates owned
subprocess groups. Snapshots and command lines are never persisted. The server
does not log request bodies, command output, credentials, or listener strings.

This is a first companion increment, not an App Store readiness claim. The
app owns connection import/disconnect UX and explains unsupported tools.

## Docker safety restriction

Automatic Docker attribution, Stop and Restart are unavailable. A published TCP
port alone does not prove that a local listener belongs to a container, and an
ambient Docker context may point to a remote engine. The collector does not query
that engine. Known Docker forwarding helpers are inspection-only, and stale
Docker action profiles are refused without any Docker command or raw PID signal.
Use Docker directly after choosing the intended engine and container.
