#!/bin/bash
set -euo pipefail

HOME_DIR=/home/mino/.mino
STATE_DIR="$HOME_DIR/scheduled-runs"
mkdir -p "$STATE_DIR"

for config in "$HOME_DIR"/playbooks/*/config.md; do
  [ -f "$config" ] || continue
  schedule=$(sed -n 's/^schedule:[[:space:]]*\([0-9][0-9]:[0-9][0-9]\).*$/\1/p' "$config" | head -1)
  [ -n "$schedule" ] || continue
  notify=$(sed -n 's/^notify:[[:space:]]*//p' "$config" | head -1)
  [ "$notify" = "true" ] || continue
  playbook=$(basename "$(dirname "$config")")
  zone=$(sed -n 's/^schedule:[[:space:]]*[0-9][0-9]:[0-9][0-9][[:space:]]*//p' "$config" | head -1)
  zone=${zone:-Asia/Kuala_Lumpur}
  current=$(TZ="$zone" date +%H:%M)
  [ "$current" = "$schedule" ] || continue
  day=$(TZ="$zone" date +%F)
  marker="$STATE_DIR/$playbook-$day"
  [ -e "$marker" ] && continue
  if /usr/local/bin/mino-playbook-runner "$playbook" "Run the $playbook playbook now."; then
    install -m 600 /dev/null "$marker"
  fi
done
