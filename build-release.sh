#!/bin/bash
# Build Mino for all platforms. Run before cutting a release.
# Upload the resulting binaries as GitHub release assets.
set -euo pipefail
cd "$(dirname "$0")"

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
  GOOS="$GOOS" GOARCH="$GOARCH" go build -o "$out" .
done

echo ""
echo "Done. Upload these to the GitHub release:"
ls -lh mino-*
