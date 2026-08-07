#!/bin/bash
# Build Mino for all platforms. Run before cutting a release.
# Usage: ./build-release.sh v1.2.0
# Upload the resulting binaries as GitHub release assets.
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-dev}"
echo "Building Mino $VERSION for all platforms..."
echo ""

platforms=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

for p in "${platforms[@]}"; do
  GOOS="${p%/*}" GOARCH="${p#*/}"
  out="mino-${GOOS}-${GOARCH}"
  [ "$GOOS" = "windows" ] && out="${out}.exe"
  echo "→ $out"
  GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags "-X main.Version=$VERSION" -o "$out" .
done

# Checksums so the VPS self-updater can verify the linux/amd64 binary
# before swapping it in (CD via release assets).
sha256sum mino-linux-amd64 mino-linux-arm64 mino-darwin-amd64 mino-darwin-arm64 mino-windows-amd64.exe > SHA256SUMS.txt

echo ""
echo "Done. Upload these to the GitHub release:"
ls -lh mino-* SHA256SUMS.txt
