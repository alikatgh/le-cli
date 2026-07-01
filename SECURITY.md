# Security

`le` runs entirely locally — it reads process/port state via `ps` and
`lsof`, and shells out to `kill`, `brew services`, or `docker` only for
processes it just scanned. It makes no network calls, stores nothing, and
sends nothing anywhere.

## Reporting a vulnerability

If you find a security issue (e.g. a way `le` could be tricked into acting
on the wrong process, an injection risk in how it builds a shell command, or
anything that trusts unescaped external input), please email
**hi@alik.asia** instead of opening a public issue. Include:

- The version (`le --version` or `le version`)
- OS and architecture
- Steps to reproduce, and what you'd expect instead

I'll acknowledge within a few days and aim to ship a fix before any public
disclosure.

## Supported versions

Only the latest released version is supported. There's no LTS branch —
`brew upgrade` or a fresh binary download gets you the fix.
