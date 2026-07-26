#!/bin/bash
set -euo pipefail

HOME_DIR=/home/mino/.mino
PLAYBOOK=${1:?playbook name is required}
REQUEST=${2:-Run the $PLAYBOOK playbook now.}
PLAYBOOK_DIR="$HOME_DIR/playbooks/$PLAYBOOK"
OUTPUT_DIR="$PLAYBOOK_DIR/output"
RESPONSE=$(mktemp)
trap 'rm -f "$RESPONSE"' EXIT

: "${TELEGRAM_BOT_TOKEN:?TELEGRAM_BOT_TOKEN is required}"
: "${MINO_TELEGRAM_CHAT_ID:?MINO_TELEGRAM_CHAT_ID is required}"
test -d "$PLAYBOOK_DIR"

curl -fsS --max-time 300 \
  -X POST http://127.0.0.1:7779/api/chat \
  -H 'Content-Type: application/json' \
  --data-binary "{\"message\":\"$REQUEST\",\"session_id\":\"systemd-$PLAYBOOK-$(date +%Y%m%d-%H%M)\"}" \
  -o "$RESPONSE"

python3 - "$RESPONSE" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    response = json.load(f)
if response.get("status") != "complete":
    raise SystemExit(f"Mino playbook failed: {response.get('status')}: {response.get('reply')}")
PY

OUTPUT=$(find "$OUTPUT_DIR" -maxdepth 1 -type f -name '*.md' -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-)
test -n "$OUTPUT" && test -s "$OUTPUT"
curl -fsS --max-time 30 \
  -X POST "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
  --data-urlencode "chat_id=${MINO_TELEGRAM_CHAT_ID}" \
  --data-urlencode "text@${OUTPUT}" \
  --data-urlencode 'disable_web_page_preview=true' \
  >/dev/null
