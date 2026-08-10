#!/bin/bash
# Build Mino for all platforms. Run before cutting a release.
# Usage: ./build-release.sh v1.2.0
# Upload the resulting binaries as GitHub release assets.
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-dev}"

# Tag-only builds (REL-05b, #130): version labels must never precede a tag.
# Refuse unless HEAD is exactly the requested tag AND the tree is clean — a
# dirty tree 'at the tag' could still ship uncommitted drift.
if [ "$VERSION" = "dev" ]; then
	echo "usage: ./build-release.sh vX.Y.Z — builds only from an exact, clean tag" >&2
	exit 1
fi
HEAD_TAG="$(git describe --tags --exact-match HEAD 2>/dev/null || true)"
if [ "$HEAD_TAG" != "$VERSION" ]; then
	echo "error: HEAD is '${HEAD_TAG:-not a tag}', not the '$VERSION' tag — check out the tag before building (e.g. git checkout $VERSION)" >&2
	exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
	echo "error: working tree is dirty — commit or stash before building a release" >&2
	exit 1
fi

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
