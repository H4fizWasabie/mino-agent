#!/bin/bash
# release.sh — the v2.9.1-style release lane, with the stage-smoke gate.
#
# Runs the full release procedure from the tagged commit, with ONE hard gate:
# the candidate binary must PASS stage-smoke.sh (a rehearsal against a copy of
# the live VPS state) before anything is published. If the smoke fails, the
# release is aborted — no tag push, no release, nothing to unpublish.
#
# Releases stay manual and deliberate (rules.md): this script automates the
# lane, it never schedules it. It needs the repo's usual local tools
# (go, gh, scp, ssh) and SSH access to the VPS (the `vps` host, or override
# with VPS_HOST=...). GitHub Actions is intentionally NOT wired in — giving
# CI SSH access to the VPS would turn a workflow-file compromise into a
# production breach.
#
# Usage (from the tagged commit, clean tree):
#   ./scripts/release.sh vX.Y.Z
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?usage: release.sh vX.Y.Z}"
VPS="${VPS_HOST:-vps}"

echo "== release.sh: $VERSION (gate: stage-smoke on $VPS)"

# 1. tag must exist and match HEAD, tree must be clean (build-release.sh
#    re-enforces the tag-only rule)
HEAD_TAG="$(git describe --tags --exact-match HEAD 2>/dev/null || true)"
if [ "$HEAD_TAG" != "$VERSION" ]; then
	echo "error: HEAD is '${HEAD_TAG:-not a tag}', not '$VERSION' — checkout the tag first" >&2
	exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
	echo "error: working tree not clean — commit or stash before releasing" >&2
	exit 1
fi

# 1b. push master + the tag BEFORE building: a release created from an
# unpushed tag silently points at the remote's old HEAD (v2.9.1 lesson).
git push origin master >/dev/null
git push origin "$VERSION" >/dev/null

# 2. build the release assets (all platforms + extensions + SHA256SUMS)
./build-release.sh "$VERSION"

# 3. THE GATE: rehearse the linux-amd64 candidate against a copy of live state
CAND="/tmp/mino-candidate-$VERSION"
scp -q "mino-linux-amd64" "$VPS:$CAND"
scp -q "scripts/stage-smoke.sh" "$VPS:/tmp/stage-smoke.sh"
if ! ssh "$VPS" "bash /tmp/stage-smoke.sh '$CAND' 7780"; then
	echo "error: stage-smoke FAILED — release aborted, nothing published." >&2
	echo "       Fix, re-tag, and re-run. The live VPS was never touched." >&2
	exit 1
fi
echo "PASS: stage-smoke gate — publishing $VERSION"

# 4. publish the release + assets
gh release create "$VERSION" --repo "$(git remote get-url origin | sed -E 's#.*[:/]([^/]+/[^/.]+)(\.git)?$#\1#')" \
	--title "$VERSION" --notes "See CHANGELOG.md for the full list." >/dev/null
for a in mino-linux-amd64 mino-linux-arm64 mino-darwin-amd64 mino-darwin-arm64 \
	mino-windows-amd64.exe minowrap threads-extension cost-watch SHA256SUMS.txt; do
	gh release upload "$VERSION" "$a" --clobber >/dev/null 2>&1 && echo "  ↑ $a"
done

echo "== release $VERSION published. Deploy:"
echo "   ssh $VPS 'sudo HOME=/home/mino /usr/local/bin/mino update'"
echo "   ssh $VPS 'systemctl restart mino.service'"
echo "   + cost-watch: fetch the release cost-watch asset, swap /opt/mino-cost-watch/, install cost-watch-check.timer"
echo "   + re-run stage-smoke after deploy for the post-release confirmation"
