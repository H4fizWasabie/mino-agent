#!/bin/bash
set -euo pipefail

HOME_DIR=/home/mino/.mino
PLAYBOOK=daily-malaysia-news-8pm
OUTPUT="$HOME_DIR/playbooks/$PLAYBOOK/output/01-malaysia-news.md"
RESPONSE=$(mktemp)
trap 'rm -f "$RESPONSE"' EXIT

: "${TELEGRAM_BOT_TOKEN:?TELEGRAM_BOT_TOKEN is required}"
: "${MINO_TELEGRAM_CHAT_ID:?MINO_TELEGRAM_CHAT_ID is required}"

curl -fsS --max-time 300 \
  -X POST http://127.0.0.1:7779/api/chat \
  -H 'Content-Type: application/json' \
  --data-binary "{\"message\":\"Run the $PLAYBOOK playbook now for tonight's Malaysia news.\",\"session_id\":\"systemd-$PLAYBOOK-$(date +%Y%m%d)\"}" \
  -o "$RESPONSE"

python3 - "$RESPONSE" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    response = json.load(f)
if response.get("status") != "complete":
    raise SystemExit(f"Mino playbook failed: {response.get('status')}: {response.get('reply')}")
PY

test -s "$OUTPUT"
curl -fsS --max-time 30 \
  -X POST "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
  --data-urlencode "chat_id=${MINO_TELEGRAM_CHAT_ID}" \
  --data-urlencode "text@${OUTPUT}" \
  --data-urlencode 'disable_web_page_preview=true' \
  >/dev/null
