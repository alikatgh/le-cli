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
`gh attestation verify <tarball> --repo alikatgh/le-cli`.

## Automated (GitHub Actions)

```sh
git tag v0.1.0
git push origin v0.1.0
```

The [`release`](.github/workflows/release.yml) workflow runs the tests, then
[goreleaser](.goreleaser.yaml) cross-compiles macOS + Linux binaries
(amd64 + arm64), attaches the tarballs (binary + man pages) +
`checksums.txt` to a GitHub release, and attests build provenance.

**Homebrew tap automation:** if the repo has a `TAP_GITHUB_TOKEN` secret
(a fine-grained PAT with `contents: write` on `alikatgh/homebrew-tap`),
goreleaser also pushes the updated formula — no hand-editing of sha256s.
Without the secret, the tap step is skipped and the bump is manual
(section below).

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
