#!/usr/bin/env bash
# Cross-compile le for macOS + Linux into dist/ as tarballs + a checksums file.
# Pure Go (no cgo), so it builds from any host.
#
# Usage: ./build-release.sh [version]   (defaults to the latest v* tag, or "dev")
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-$(git describe --tags --match 'v*' --abbrev=0 2>/dev/null | sed 's#^v##' || echo dev)}"
OUT="dist"
TARGETS=(darwin/amd64 darwin/arm64 linux/amd64 linux/arm64)

rm -rf "$OUT"
mkdir -p "$OUT"

for t in "${TARGETS[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  name="le_${VERSION}_${os}_${arch}"
  echo "building ${name}"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "$OUT/le" .
  tar -C "$OUT" -czf "$OUT/${name}.tar.gz" le
  rm -f "$OUT/le"
done

( cd "$OUT" && shasum -a 256 ./*.tar.gz > checksums.txt )

echo
echo "artifacts in ${OUT}:"
ls -1 "$OUT"
