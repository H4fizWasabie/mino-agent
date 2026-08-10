#!/bin/bash
# Mino VPS bootstrap — setup only. Production binaries arrive via release →
# `mino update` (REL-05, #131): this script never builds or pushes a binary.
# Emergency manual deploys: see docs/emergency-deploy.md.
set -euo pipefail

VPS="${VPS_HOST:?Set VPS_HOST to your server IP}"
VPS_USER="root"
MINO_HOME="/home/mino"

# Removed: binary builds + pushes (mino, minowrap, threads-extension).
# The self-updater (release → `mino update`) is the only production path;
# updater-less extensions ship as release assets (build-release.sh).

echo "=== Bootstrapping Mino on $VPS ==="

# 1. Create mino user if not exists
echo "--- Creating mino user ---"
ssh "$VPS_USER@$VPS" '
    id mino &>/dev/null || useradd -m -s /bin/bash mino
    mkdir -p /home/mino/.mino /home/mino/extensions /home/mino/.minowrap
'

# Install the pinned Token Killer system-wide so Mino's Bash runtime can use it.
echo "--- Installing RTK ---"
ssh "$VPS_USER@$VPS" '
if [ "$(rtk --version 2>/dev/null || true)" != "rtk 0.43.0" ]; then
    pkg=/tmp/rtk_0.43.0-1_amd64.deb
    curl -fsSL https://github.com/rtk-ai/rtk/releases/download/v0.43.0/rtk_0.43.0-1_amd64.deb -o "$pkg"
    echo "eb571d784b3269521722ebe2f0dc2409e89da6bd70bf097ddb21e9d4b3b240b9  $pkg" | sha256sum -c -
    dpkg -i "$pkg"
    rm -f "$pkg"
fi
'

# 2. Push binaries is REMOVED — release → `mino update` is the only path
#    (REL-05). Extensions update from release assets when present; the
#    bootstrap installs whatever is already on the host.

# 3. Push extensions
echo "--- Pushing extensions ---"
if [ -d extensions/fileingest ]; then
    scp -r extensions/fileingest "$VPS_USER@$VPS:/home/mino/extensions/"
else
    echo "No fileingest extension in this release; preserving the existing VPS sidecar"
fi

# 4. Seed minowrap tools.json if not exists
echo "--- Seeding minowrap tools ---"
ssh "$VPS_USER@$VPS" '
if [ ! -f /home/mino/.minowrap/tools.json ]; then
  cat > /home/mino/.minowrap/tools.json << EOFTOOLS
[
  {"name": "disk_usage", "description": "Show disk usage for a path", "run": "df -h {path}"},
  {"name": "memory_usage", "description": "Show current memory usage", "run": "free -h"},
  {"name": "uptime_check", "description": "Check system uptime", "run": "uptime"}
]
EOFTOOLS
fi
'

# 5. Push oauth provider configs
echo "--- Pushing OAuth configs ---"
ssh "$VPS_USER@$VPS" 'mkdir -p /home/mino/.mino/oauth.d'
scp -r oauth.d/* "$VPS_USER@$VPS:/home/mino/.mino/oauth.d/"

# 6. Create extensions.json for Mino
echo "--- Configuring extensions ---"
ssh "$VPS_USER@$VPS" '
cat > /home/mino/.mino/extensions.json << EOF
[
  {"name": "fileingest", "url": "http://127.0.0.1:9103"},
  {"name": "minowrap", "url": "http://127.0.0.1:9876"},
  {"name": "threads", "url": "http://127.0.0.1:9200"}
]
EOF
chown -R mino:mino /home/mino
'

# 6. Push + enable systemd units
# Note: the old playbook dispatcher (mino-playbook-dispatcher.{service,timer},
# mino-playbook-runner.sh, mino-daily-malaysia-news.timer) was replaced by the
# in-process scheduler (closes #74) and must NOT be installed — re-installing
# it resurrects dead units and scripts on every deploy.
echo "--- Setting up systemd ---"
for unit in extensions/*.service; do
    [ -f "$unit" ] || continue
    scp "$unit" "$VPS_USER@$VPS:/etc/systemd/system/"
done

ssh "$VPS_USER@$VPS" '
    command -v sqlite3 >/dev/null || apt-get install -y -qq sqlite3
    if grep -q "^TELEGRAM_BOT_TOKEN=" /home/mino/.mino/mino.env 2>/dev/null && ! grep -q "^MINO_TELEGRAM_CHAT_ID=" /home/mino/.mino/mino.env; then
        echo "TELEGRAM_BOT_TOKEN requires MINO_TELEGRAM_CHAT_ID" >&2
        exit 1
    fi
    if [ -f /home/mino/.mino/state.db ]; then
        backup_dir=/home/mino/.mino/backups
        install -d -m 700 -o mino -g mino "$backup_dir"
        backup="$backup_dir/state.db-$(date -u +%Y%m%dT%H%M%SZ)"
        sqlite3 /home/mino/.mino/state.db ".backup $backup"
        [ "$(sqlite3 "$backup" "PRAGMA quick_check;")" = ok ]
        chown mino:mino "$backup"
        chmod 600 "$backup"
        ln -sfn "$backup" /home/mino/.mino/state.db.bak
        find "$backup_dir" -type f -name 'state.db-*' -mtime +30 -delete
    fi
    systemctl daemon-reload
    systemctl enable mino mino-fileingest minowrap threads
    # The external playbook-dispatcher and daily-malaysia-news timers were
    # removed (in-process scheduler, closes #74); nothing to disable.
    systemctl restart minowrap
    systemctl restart mino-fileingest
    systemctl restart threads
    systemctl restart mino
'

echo "=== Done ==="
echo "Check: ssh $VPS_USER@$VPS systemctl status mino minowrap"
echo "Dashboard: http://$VPS:7779 (if enabled)"
