#!/bin/sh
# le installer — https://localhostexplorer.com/cli
#
# Downloads the latest release binary for this machine, verifies it against
# the release's checksums.txt, and installs it. macOS + Linux, amd64 + arm64.
#
#   curl -fsSL https://localhostexplorer.com/install.sh | sh
#
# Options via environment:
#   LE_INSTALL_DIR=~/bin   install somewhere else (default: /usr/local/bin,
#                          falling back to ~/.local/bin if not writable)
#   LE_VERSION=v0.1.13     pin a version (default: latest release)
#
# Prefer a package manager? brew install alikatgh/tap/le
# Distrust installer scripts? Sensible. Every step below is reproducible by
# hand from https://github.com/alikatgh/le-cli/releases — and each tarball
# carries a build-provenance attestation:
#   gh attestation verify le_*.tar.gz --repo alikatgh/le-cli
set -eu

REPO="alikatgh/le-cli"

fail() { echo "le install: $*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin | linux) ;;
  *) fail "unsupported OS: $os (macOS and Linux only)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac

# Resolve the tag: pinned via LE_VERSION, else the latest release. The API
# response is JSON but the one field we need is greppable without jq.
tag="${LE_VERSION:-}"
if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    grep -m1 '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
fi
[ -n "$tag" ] || fail "could not resolve the latest release tag"
ver=${tag#v}

file="le_${ver}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "le $tag ($os/$arch) — downloading…"
curl -fsSL -o "$tmp/$file" "$base/$file" || fail "download failed: $base/$file"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "checksums.txt download failed"

# Checksum verification is not optional: a truncated download or a tampered
# proxy response must stop here, not get installed.
want=$(grep " $file\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || fail "no checksum entry for $file"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$file" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$file" | cut -d' ' -f1)
fi
[ "$want" = "$got" ] || fail "checksum mismatch for $file (want $want, got $got)"
echo "checksum verified."

tar -xzf "$tmp/$file" -C "$tmp"
[ -f "$tmp/le" ] || fail "tarball did not contain the le binary"

# Install dir: explicit override, else /usr/local/bin (sudo if needed and
# possible), else ~/.local/bin.
dir="${LE_INSTALL_DIR:-/usr/local/bin}"
if [ -d "$dir" ] && [ -w "$dir" ]; then
  mv "$tmp/le" "$dir/le"
elif [ -z "${LE_INSTALL_DIR:-}" ] && command -v sudo >/dev/null 2>&1; then
  echo "installing to $dir needs sudo…"
  sudo mv "$tmp/le" "$dir/le"
else
  dir="$HOME/.local/bin"
  mkdir -p "$dir"
  mv "$tmp/le" "$dir/le"
fi

echo "installed: $dir/le"
"$dir/le" version || true
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "note: $dir is not on your PATH" ;;
esac
echo "run: le    (man pages ship in the tarball; brew installs them for you)"
