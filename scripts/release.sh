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
# Schema-bump releases declare the expected target version:
#   EXPECTED_SCHEMA=8 ./scripts/release.sh vX.Y.Z
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

# The release tag must be the merged remote master commit. Do not push a
# stale local master from this lane; a release must never move source and tag
# refs as a side effect of preparing a candidate.
REMOTE_MASTER="$(git rev-parse refs/remotes/origin/master 2>/dev/null || true)"
HEAD_COMMIT="$(git rev-parse HEAD)"
if [ "$REMOTE_MASTER" != "$HEAD_COMMIT" ]; then
	echo "error: HEAD ($HEAD_COMMIT) is not origin/master (${REMOTE_MASTER:-unavailable}) — fetch and tag the merged master commit" >&2
	exit 1
fi

# 2. build the release assets (all platforms + extensions + SHA256SUMS)
./build-release.sh "$VERSION"

# 3. THE GATE: rehearse the linux-amd64 candidate against a copy of live state
CAND="/tmp/mino-candidate-$VERSION"
# The VPS link has dropped mid-scp twice (v2.10.0, v2.10.1) — retry before aborting.
scp_retry() {
	for attempt in 1 2 3; do
		# The VPS SSH service exposes legacy SCP but closes the SFTP-backed
		# default mode used by newer OpenSSH clients.
		if scp -O -q "$1" "$2"; then return 0; fi
		echo "  scp attempt $attempt failed, retrying..." >&2
		sleep 3
	done
	return 1
}
scp_retry "mino-linux-amd64" "$VPS:$CAND" || { echo "error: cannot reach $VPS — aborting" >&2; exit 1; }
scp_retry "scripts/stage-smoke.sh" "$VPS:/tmp/stage-smoke.sh" || { echo "error: cannot stage stage-smoke.sh" >&2; exit 1; }
if ! ssh "$VPS" "EXPECTED_SCHEMA='${EXPECTED_SCHEMA:-}' bash /tmp/stage-smoke.sh '$CAND' 7780"; then
	echo "error: stage-smoke FAILED — release aborted, nothing published." >&2
	echo "       Fix, re-tag, and re-run. The live VPS was never touched." >&2
	exit 1
fi
echo "PASS: stage-smoke gate — publishing $VERSION"

# 4. The gate passed: publish the tag, then the release + assets.
git push origin "$VERSION" >/dev/null
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
