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
#   - extensions.json + extensions/ excluded (RUN-001) -> the staged boot's
#     extension-supervisor reconciliation must not spawn the LIVE extensions
#     (duplicate processes, port conflicts)
#
# RUN-feature rehearsals (map #213, release gate):
#   - RUN-002: ops_journal table present in the staged schema
#   - RUN-005: config self-heal — two SIGHUPs against the staged providers.json
#     (pass 1 establishes .prev from the valid file, corrupt, pass 2 must
#     restore it) — deterministic, no LLM
#   - RUN-006: approval entry-point interception — "deny 999" through the real
#     /api/chat must be consumed before the loop
#   Not smoke-able in stage (covered by unit tests + the owner-run live
#   rehearsal): RUN-001 install (needs git+go toolchain + a fixture source on
#   the VPS), RUN-004 rollback (needs a real swap cycle), RUN-003 sudoers
#   (needs real root).
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
rsync -a --exclude traces --exclude audit.jsonl --exclude outbox --exclude '*.bak*' \
	--exclude extensions --exclude extensions.json "$LIVE_HOME/" "$STAGE/"
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

# 6. #195: removing a provider while a session is sticky to it must not
#    panic — the session falls through to the remaining providers. Pre-fix
#    binaries nil-deref in candidates() and the chat call fails loudly.
STICKY_PROVIDER="mimo"
if curl -sf -m 15 -X POST "$BASE/api/switch" -H 'Content-Type: application/json' 	-d "{\"provider\":\"$STICKY_PROVIDER\",\"model\":\"xiaomi/mimo-v2.5\",\"session\":\"stage-removal\"}" > /dev/null 2>&1; then
	python3 - "$STAGE/providers.json" << 'PYEOF2'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["providers"] = [x for x in d["providers"] if x["name"] != "mimo"]
json.dump(d, open(p, "w"), indent=2)
PYEOF2
	kill -HUP "$PID" 2>/dev/null || true
	sleep 2
	REPLY2=$(curl -sf -m 120 -X POST "$BASE/api/chat" -H 'Content-Type: application/json' 		-d '{"message":"Reply with exactly: removal ok","session_id":"stage-removal"}' 2>/dev/null || echo "REMOVAL-CHAT-FAILED")
	if echo "$REPLY2" | grep -q "removal ok"; then
		echo "PASS: provider removal while sticky does not panic (#195)"
	else
		echo "FAIL: sticky-provider removal: $(echo "$REPLY2" | head -c 200)"; FAIL=1
	fi
else
	echo "WARN: could not switch staged session (provider missing?) — skipping #195 step"
fi

# 7. RUN-002: the operation journal (RUN-002 seam) exists in the staged schema
JOURNAL_OK=$(sqlite3 "$STAGE/state.db" "SELECT name FROM sqlite_master WHERE type='table' AND name='ops_journal'" 2>/dev/null || echo "")
if [ "$JOURNAL_OK" = "ops_journal" ]; then
	echo "PASS: ops_journal table present (RUN-002)"
else
	echo "FAIL: ops_journal missing from staged schema (RUN-002)"; FAIL=1
fi

# 8. RUN-005: config self-heal — heal pass 1 (SIGHUP) refreshes .prev from the
#    valid staged providers.json; corrupt it; heal pass 2 must restore the file
#    and keep the instance serving. The staged file only — live untouched.
PROVIDERS_ORIG=$(mktemp)
cp "$STAGE/providers.json" "$PROVIDERS_ORIG"
kill -HUP "$PID" 2>/dev/null || true   # pass 1: establishes .prev baseline
sleep 2
echo '{"providers":[' > "$STAGE/providers.json"   # truncated JSON — the bad-edit class
kill -HUP "$PID" 2>/dev/null || true   # pass 2: must restore
sleep 2
if diff -q "$PROVIDERS_ORIG" "$STAGE/providers.json" > /dev/null 2>&1 \
	&& curl -sf -m 5 "$BASE/api/universe" > /dev/null 2>&1; then
	echo "PASS: config self-heal restored bad providers.json from .prev (RUN-005)"
else
	echo "FAIL: config self-heal did not restore providers.json (RUN-005):"
	diff "$PROVIDERS_ORIG" "$STAGE/providers.json" | head -5; FAIL=1
fi
rm -f "$PROVIDERS_ORIG"

# 9. RUN-006: approval entry-point interception — "deny <id>" is consumed by
#    the harness BEFORE the loop at the real /api/chat entry point, even for an
#    unknown id (the format is reserved). Deterministic — no LLM involvement.
REPLY3=$(curl -sf -m 30 -X POST "$BASE/api/chat" -H 'Content-Type: application/json' \
	-d '{"message":"deny 999","session_id":"stage-approval"}' 2>/dev/null || echo "APPROVAL-CHAT-FAILED")
if echo "$REPLY3" | grep -q "No pending approval request 999"; then
	echo "PASS: approval reply intercepted before the loop (RUN-006)"
else
	echo "FAIL: approval interception: $(echo "$REPLY3" | head -c 200)"; FAIL=1
fi

# 10. #188: candidate must FAIL LOUDLY on the occupied live port (7779)
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
