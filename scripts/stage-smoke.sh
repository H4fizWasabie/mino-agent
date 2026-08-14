#!/bin/bash
# stage-smoke.sh — rehearsal with the real script (2026-08-14).
#
# Boots a candidate mino binary against a COPY of the live VPS state on a
# spare port, healthchecks it (boot, schema, universe, one scoped chat turn),
# then verifies the #188 bind-conflict behavior on the live port. The live
# service is never touched; the staged copy is deleted afterwards.
#
# Side-effect safety on the copy:
#   - schedules.json removed  -> the staged scheduler cannot fire playbooks
#   - TELEGRAM_BOT_TOKEN removed from the copied mino.env -> no bot polling,
#     so it can never steal the live agent's Telegram updates or reply to the
#     owner from a rehearsal copy
#   - traces / audit / outbox / *.bak* excluded
#
# The one live cost: the chat turn calls the real LLM with the staged keys
# (a few cents).
#
# Usage (run ON the VPS): ./stage-smoke.sh /path/to/candidate-mino [port]
set -euo pipefail

CANDIDATE="${1:?usage: stage-smoke.sh /path/to/candidate-mino [port]}"
PORT="${2:-7780}"
LIVE_HOME=/home/mino/.mino
STAGE=/tmp/mino-stage
BASE="http://127.0.0.1:${PORT}"
FAIL=0

echo "== stage-smoke: $(basename "$CANDIDATE") on :${PORT} (live service untouched)"

# 1. stage a copy of the real state
rm -rf "$STAGE"
mkdir -p "$STAGE"
rsync -a --exclude traces --exclude audit.jsonl --exclude outbox --exclude '*.bak*' "$LIVE_HOME/" "$STAGE/"
rm -f "$STAGE/schedules.json"
sed -i '/^TELEGRAM_BOT_TOKEN=/d' "$STAGE/mino.env"

# 2. boot the candidate
MINO_HOME="$STAGE" MINO_DASHBOARD_PORT="$PORT" "$CANDIDATE" > /tmp/stage-smoke.log 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null || true; rm -rf "$STAGE"' EXIT

# 3. wait for readiness
ready=0
for _ in $(seq 1 45); do
	if curl -sf -m 2 "$BASE/api/universe" > /dev/null 2>&1; then ready=1; break; fi
	if ! kill -0 "$PID" 2>/dev/null; then
		echo "FAIL: candidate exited early:"; tail -5 /tmp/stage-smoke.log; exit 1
	fi
	sleep 1
done
if [ "$ready" != 1 ]; then
	echo "FAIL: no readiness after 45s"; tail -5 /tmp/stage-smoke.log; exit 1
fi
echo "PASS: boot + /api/universe"

# 4. schema: staged DB is still at the live version, no migration ran
LIVE_VER=$(sqlite3 "$LIVE_HOME/state.db" "SELECT value FROM _meta WHERE key='schema_version'" 2>/dev/null || echo "?")
STAGE_VER=$(sqlite3 "$STAGE/state.db" "SELECT value FROM _meta WHERE key='schema_version'" 2>/dev/null || echo "?")
if [ "$LIVE_VER" = "$STAGE_VER" ] && ! grep -q "schema migrated" /tmp/stage-smoke.log; then
	echo "PASS: schema $STAGE_VER, no migration run"
else
	echo "FAIL: schema live=$LIVE_VER staged=$STAGE_VER or migration ran:"
	grep -E "migrat|ERROR" /tmp/stage-smoke.log | head -5; FAIL=1
fi

# 5. one scoped chat turn: proves providers load, keys resolve, and the
#    default (openai) transport path works end to end (PRV-001)
REPLY=$(curl -sf -m 180 -X POST "$BASE/api/chat" -H 'Content-Type: application/json' \
	-d '{"message":"run system_check and reply with ONLY the cost block, verbatim. Nothing else.","session_id":"stage-smoke"}' \
	2>/dev/null || echo "CHAT-FAILED")
if echo "$REPLY" | grep -q "cost:"; then
	echo "PASS: live chat turn + cost block (openai transport default works)"
else
	echo "FAIL: chat turn did not return a cost block: $(echo "$REPLY" | head -c 200)"; FAIL=1
fi

# 6. #188: candidate must FAIL LOUDLY on the occupied live port (7779)
kill "$PID" 2>/dev/null || true
wait "$PID" 2>/dev/null || true
set +e
MINO_HOME="$STAGE" MINO_DASHBOARD_PORT=7779 "$CANDIDATE" > /tmp/stage-smoke-bind.log 2>&1
CODE=$?
set -e
if [ "$CODE" -eq 0 ]; then
	echo "FAIL: candidate exited 0 on occupied :7779 — silent failure, #188 not fixed"
	FAIL=1
elif grep -qi "bind" /tmp/stage-smoke-bind.log; then
	echo "PASS: bind conflict is loud — exit $CODE with bind error (#188)"
else
	echo "WARN: exit $CODE but no bind error in log:"; tail -3 /tmp/stage-smoke-bind.log
fi

rm -rf "$STAGE"
trap - EXIT
if [ "$FAIL" = 0 ]; then
	echo "== stage-smoke: PASS"
else
	echo "== stage-smoke: FAIL"
	exit 1
fi
