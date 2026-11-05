# Releasing le

Tags are plain `v*` (e.g. `v0.1.0`).

## Before you tag — the checklist that exists because we skipped it

v0.1.5 shipped with its flagship feature (`le stop --dir`) completely broken:
the live-fire test only used absolute temp paths, never the relative `.` a
human types, and the adversarial review that would have caught it ran *after*
the release — so v0.1.7–v0.1.9 existed mostly to patch v0.1.5–v0.1.8. Review
first, tag second:

1. **Gate:** `go build ./... && go vet ./... && gofmt -l . && go test -race
   ./... && golangci-lint run ./...` — all clean (same as CI, but don't tag on
   hope).
2. **Live-fire every new user-facing behavior on a real process** — and
   exercise the inputs a human actually types, not just the ones convenient to
   script: relative paths (`--dir .`), quoted config values, invalid ports,
   single-digit dates, a non-English locale.
3. **New feature or touched stop-path? Run an adversarial review of the diff
   BEFORE tagging**, not as post-release QA. Three post-hoc review rounds
   found 18 bugs in already-shipped releases; each would have been cheaper
   caught pre-tag.
4. **Batch.** One reviewed release beats three same-day patch releases.
5. Promote `[Unreleased]` in CHANGELOG.md (date + compare links), then tag.

After the release publishes: update the tap with the real `checksums.txt`
values (cross-check every URL↔sha pair), `brew upgrade`, and verify the new
behavior on the *installed* binary — plus
`gh attestation verify <archive> --repo alikatgh/le-cli` — one of EACH archive type (tarball and zip), not just the one you happen to run.

## Automated (GitHub Actions)

```sh
git tag v0.1.0
git push origin v0.1.0
```

The [`release`](.github/workflows/release.yml) workflow runs the tests, then
[goreleaser](.goreleaser.yaml) cross-compiles macOS + Linux binaries
(amd64 + arm64), attaches the tarballs (binary + man pages) +
`checksums.txt` to a GitHub release, and attests build provenance.

**Release-tool installation:** CI and release install the official OSS
GoReleaser `v2.16.0` through
[`goreleaser/goreleaser-action` v7.3.0](https://github.com/goreleaser/goreleaser-action/tree/a4f02ab5355a589de60d917a659ce617ed19b3de),
pinned to commit `a4f02ab5355a589de60d917a659ce617ed19b3de`, with
`install-only: true`. That mode adds the executable to `PATH` and returns
before running it. The existing release branches then call `goreleaser`;
Go `1.27.3` remains the CLI compiler. Version 2.16.0 is retained because this
project intentionally uses its deprecated `brews` configuration.

The official installer warns and can use an unverified download if
`checksums.txt` cannot be downloaded. Both workflows therefore check the
installed Linux x86_64 executable against the fixed SHA256 below **before its
first execution**, failing the job on a mismatch. Cosign verification remains
optional in the installer; the fixed executable digest is enforced regardless.

```text
Official v2.16.0 goreleaser_Linux_x86_64.tar.gz
archive bytes: 26196931
archive sha256: eaae05b5eba07533bd0f06846b68c808399504784df00c62eb219541fc04e5e2
goreleaser ELF64 x86_64 member bytes: 85733538
goreleaser executable sha256: 00b06a2f8de8711ade37289b2cb61887936a8b04745414c5b23298fc8e9e3dfd
```

The receipt was established on 2026-11-05 from the
[official release asset](https://github.com/goreleaser/goreleaser/releases/tag/v2.16.0):
archive size and published SHA256 matched, then the bounded tar member was
streamed into SHA256 without extracting or executing it. The temporary archive
was removed. Deliberate tool upgrades must refresh the action pin, tool version
and executable digest together.

The existing Ubuntu CI job installs and verifies this same binary, then runs
plain `goreleaser check`. Pinned v2.16.0 has no `--ignore=deprecations` option.
Its [check implementation](https://github.com/goreleaser/goreleaser/blob/v2.16.0/cmd/check.go)
returns **2** for a valid configuration using deprecated properties; schema or
default-validation errors return **1**, including a configuration that is also
deprecated. CI accepts only 0 or 2 and propagates every other failure. The
exception is deliberate: `brews` generates our existing Formula for macOS and
Linux; silently switching to a cask would change the installed product contract.
Deprecation messages remain visible.

CI then performs a real, credential-free snapshot packaging run:

```sh
goreleaser release --snapshot --clean --skip=publish --timeout=10m --parallelism=2
```

The [pinned snapshot implementation](https://github.com/goreleaser/goreleaser/blob/v2.16.0/cmd/release.go)
skips publishing, announcements and release-state validation. The separate
configuration check above keeps schema validation mandatory. The command is
bounded to ten minutes/two concurrent tasks, with a twelve-minute CI step limit.
It supplies no GitHub or tap credentials and uploads no Actions artifacts.
CI verifies exactly four macOS/Linux tarballs and two Windows zips, matching
checksums, included binaries/man pages, and a generated Formula referencing all
four tarballs and their hashes. Files stay on the disposable runner; they are
not a public release. Publication and provenance remain in the tag workflow.

The `v0.1.26` attempt failed while compiling the release tool's dependency graph
(`grpc@v1.80.0`: undefined `http2.TrailerPrefix`), before any CLI archives or
GitHub release were published. Preserve that tag; the repair and its original
feature set are prepared as `v0.1.27` with a `v0.1.25...v0.1.27` comparison.

**Homebrew tap automation:** the `TAP_GITHUB_TOKEN` secret should be a
fine-grained PAT with `contents: write` on `alikatgh/homebrew-tap`. Before
GoReleaser, a bounded preflight checks that the tap's default branch is
unprotected and negotiates an authenticated `git push --dry-run` through the
GitHub CLI credential helper. The preflight changes no remote refs and keeps
its temporary Git/GitHub CLI configuration in a private runner directory.
A passing check establishes access at that moment; token validity, branch
policy, and the later formula push can still change or fail.

A missing/unusable token, rejected push, protected/unknown branch, or timeout
selects the manual fallback: GoReleaser publishes with Homebrew skipped, and
the workflow attempts archive provenance before deliberately failing with
`TAP_SKIPPED=1`. Update the tap for that release as described below. If the
preflight passes, GoReleaser keeps the existing automatic formula update.

Archive attestation also runs after a failed GoReleaser step when archives
exist and the job has not been cancelled. Built archives, published release
assets, valid attestations, and an updated tap are separate results. A red job
may already have published assets; inspect the release and tap before retrying
or recovering manually. If no archives were built, attestation is skipped.

The preflight's offline regressions run on the existing Ubuntu CI job and can
be checked locally with `python3 .github/scripts/test-tap-preflight.py`; they
use only stub clients and synthetic credentials.

## Local dry-run (no publishing)

```sh
TAP_GITHUB_TOKEN="" goreleaser release --snapshot --clean --skip=homebrew
```

Builds everything into `dist/` exactly as CI would, without touching
GitHub or the tap — the way to validate packaging changes before tagging.

## Homebrew (optional, recommended for reach)

One-time: create a public tap repo `alikatgh/homebrew-tap`. Only needed when
the `TAP_GITHUB_TOKEN` secret is NOT configured — otherwise goreleaser does
this automatically. After each release,
add/update `Formula/le.rb` with the version and the sha256s from
`dist/checksums.txt`:

```ruby
class Le < Formula
  desc "See and stop what's listening on localhost, from the terminal"
  homepage "https://localhostexplorer.com"
  version "0.1.0"

  on_macos do
    on_arm do
      url "https://github.com/alikatgh/le-cli/releases/download/v0.1.0/le_0.1.0_darwin_arm64.tar.gz"
      sha256 "da0953b4403bc6f65a9047125cde6b9932ef4209c874ccbce034f96bae1d6900"
    end
    on_intel do
      url "https://github.com/alikatgh/le-cli/releases/download/v0.1.0/le_0.1.0_darwin_amd64.tar.gz"
      sha256 "68704cc76a205a956426724c3dd05e14a281c9336f3a6ad812082f316e56f76e"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/alikatgh/le-cli/releases/download/v0.1.0/le_0.1.0_linux_arm64.tar.gz"
      sha256 "bb54ad0ef1b8c0d5991a436965f165d64116943a3095b695afcb8a5d5a0e8954"
    end
    on_intel do
      url "https://github.com/alikatgh/le-cli/releases/download/v0.1.0/le_0.1.0_linux_amd64.tar.gz"
      sha256 "329dfba47131b49c8a201160a37225de1f006621ec92bdf9bac3157d6046d21c"
    end
  end

  def install
    bin.install "le"
  end

  test do
    system "#{bin}/le", "--version"
  end
end
```

Then anyone can install with:

```sh
brew install alikatgh/tap/le
```

## Nightly fuzz crash recovery

The nightly fuzz matrix and its six 180-second campaigns are unchanged.
Failures remain failed; the final reporting step retains the current target's
complete Go corpus in base64 chunks in the workflow log, with paths, SHA256s,
source commit and exact replay commands. It uses logs and a short job summary
instead of Actions artifact uploads. Normal workflow-log retention still applies.

Save the **report crash corpus** step's plain-text log as `crash.log`. In a
checkout of its recorded `GITHUB_SHA`, run:

```sh
python3 .github/scripts/report-fuzz-crash.py restore --log crash.log --source-sha <recorded-commit-sha>
```

The helper validates every chunk, hash, path and source SHA before writing.
It creates new corpus files only, rejects symlink paths and existing files,
and prints the exact `go test -run ...` command for each restored case. Review
and run those commands separately; restoration does not check out code or
execute a corpus. The restore helper uses macOS/Linux directory-fd guards.

Reporting is explicitly bounded to 32 files and 8 MiB of raw corpus per target,
with 3 KiB raw chunks and a 16 MiB encoded-log ceiling. A larger or unsafe corpus
fails the reporter clearly; it never emits a truncated corpus as complete.
These are reporting bounds: Go 1.27.3's `internal/fuzz/worker.go` allows 100 MiB
of shared input, so a minimized failure has no guaranteed small size. Summary
payloads are included only when the whole summary fits within 900 KiB, below
[GitHub's 1 MiB per-step limit](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands#adding-a-job-summary);
larger payloads remain in the step log. A failure
that produced no corpus is reported explicitly without changing its status.

Run the offline transport/restore regressions with
`python3 .github/scripts/test-fuzz-crash-report.py`.

## Public CLI execution cost evidence

This scope is the public `alikatgh/le-cli` repository using standard
GitHub-hosted runners, public GitHub Release assets and public build-provenance
attestations. It does not authorize larger runners, private repositories,
paid storage or services. The verified free paths are standard public runner
execution, public Release-asset storage, public-repository attestations, and
workflow logs/summaries; see
[GitHub Actions billing](https://docs.github.com/en/billing/concepts/product-billing/github-actions#examples-of-how-usage-is-measured).

At **2026-11-05 05:26:40 UTC**, a read-only
`GET /repos/alikatgh/le-cli/actions/cache/storage-limit` returned HTTP 200 and
`max_cache_size_gb: 10`. This is the repository's configured hard cache maximum,
matching the included 10 GB allowance; GitHub evicts cache entries at that
limit rather than allowing storage growth above it. The same check observed
87,171,255 bytes across one cache entry and zero Actions artifacts. See the
[cache storage-limit API](https://docs.github.com/en/rest/actions/cache#get-github-actions-cache-storage-limit-for-a-repository).

After the fuzz-report change, no workflow uploads Actions artifacts. Crash
recovery uses logs and bounded step summaries; GitHub Release assets and
attestations keep their existing public-repository paths. These observations
are dated evidence, not permission to raise a limit. Refresh affected account
and repository controls before later activation or configuration changes.
