#!/bin/bash
set -euo pipefail

HOME=/home/mino/.mino
NOW=$(date +%H:%M)
DOW=$(date +%u)
HOUR=$(date +%H)
MINUTE=$(date +%M)
NOW_MINS=$((10#$HOUR * 60 + 10#$MINUTE))

for d in "$HOME"/playbooks/*/config.md; do
    test -f "$d" || continue
    name=$(basename "$(dirname "$d")")
    schedule=$(grep "^schedule:" "$d" | head -1 | cut -d: -f2- | xargs)
    test -n "$schedule" || continue

    if echo "$schedule" | grep -q "^every"; then
        interval=$(echo "$schedule" | grep -o '[0-9]\+')
        test -n "$interval" || continue
        stamp="$HOME/run-locks/$name.last"
        if test -f "$stamp"; then
            last=$(cat "$stamp")
            elapsed=$(($(date +%s) - last))
            if [ "$elapsed" -lt "$((interval * 60))" ]; then continue; fi
        fi
        date +%s > "$stamp"
    else
        time_part=$(echo "$schedule" | grep -o '[0-9][0-9]:[0-9][0-9]')
        day_part=$(echo "$schedule" | tr '[:upper:]' '[:lower:]' | grep -o 'mon\|tue\|wed\|thu\|fri\|sat\|sun' || echo "")
        test -n "$time_part" || continue
        t_hour=${time_part%%:*}
        t_min=${time_part##*:}
        t_mins=$((10#$t_hour * 60 + 10#$t_min))
        diff=$((NOW_MINS - t_mins))
        if [ "$diff" -lt 0 ] || [ "$diff" -gt 5 ]; then continue; fi
        if test -n "$day_part"; then
            day_num=$(echo "Mon Tue Wed Thu Fri Sat Sun" | tr ' ' '\n' | grep -ni "^$day_part" | cut -d: -f1 | head -1)
            if [ "$DOW" != "$day_num" ]; then continue; fi
        fi
        stamp="$HOME/run-locks/$name.last"
        if test -f "$stamp"; then
            last=$(cat "$stamp")
            if [ $(($(date +%s) - last)) -lt 120 ]; then continue; fi
        fi
        date +%s > "$stamp"
    fi

    logger -t mino-dispatcher "Running playbook: $name"
    /usr/local/bin/mino-playbook-runner "$name" &
done
wait
