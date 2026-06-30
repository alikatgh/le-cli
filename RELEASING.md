# Releasing le

Tags are plain `v*` (e.g. `v0.1.0`).

## Automated (GitHub Actions)

```sh
git tag v0.1.0
git push origin v0.1.0
```

The [`release`](.github/workflows/release.yml) workflow runs the tests,
cross-compiles macOS + Linux binaries (amd64 + arm64), and attaches the
tarballs + `checksums.txt` to a GitHub release.

## Manual (no CI required)

```sh
./build-release.sh 0.1.0
gh release create v0.1.0 \
  --title "le 0.1.0" \
  --notes "Prebuilt le binaries for macOS and Linux." \
  dist/*.tar.gz dist/checksums.txt
```

## Homebrew (optional, recommended for reach)

One-time: create a public tap repo `alikatgh/homebrew-tap`. After each release,
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
      sha256 "REPLACE_WITH_darwin_arm64_SHA"
    end
    on_intel do
      url "https://github.com/alikatgh/le-cli/releases/download/v0.1.0/le_0.1.0_darwin_amd64.tar.gz"
      sha256 "REPLACE_WITH_darwin_amd64_SHA"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/alikatgh/le-cli/releases/download/v0.1.0/le_0.1.0_linux_arm64.tar.gz"
      sha256 "REPLACE_WITH_linux_arm64_SHA"
    end
    on_intel do
      url "https://github.com/alikatgh/le-cli/releases/download/v0.1.0/le_0.1.0_linux_amd64.tar.gz"
      sha256 "REPLACE_WITH_linux_amd64_SHA"
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
