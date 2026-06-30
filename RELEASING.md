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
