# Emergency Deploy Procedure (REL-05)

> The **only** production path is release → `mino update` on the VPS (verified
> against `SHA256SUMS.txt` by the updater since #132). This document is the
> **emergency lane** — used only when GitHub is down or a release is broken.
> It is deliberately awkward: you should only reach for it when you mean it.

## When it is acceptable

- GitHub (API, releases, or downloads) is unreachable and a fix cannot wait.
- The latest release is broken in a way the self-updater cannot recover from,
  and the fix cannot ship as a release first.

If neither is true, **use the normal path**: tag → `build-release.sh` →
`gh release create` → `mino update` on the VPS.

## The procedure (raw scp + verification)

1. **Build from a tag on a clean tree** — the same guard as normal releases:
   ```
   git checkout vX.Y.Z
   git describe --tags --exact-match HEAD   # must print vX.Y.Z
   git status --porcelain                  # must be empty
   go build -ldflags "-X main.Version=vX.Y.Z" -o /tmp/mino-esc .
   ```
2. **Verify against the release's checksums** — never trust a downloaded pair;
   compare against the **repo's** `SHA256SUMS.txt` (or the release asset of
   the same name), not the file that arrived next to the binary:
   ```
   sha256sum -c SHA256SUMS.txt            # must print: mino-linux-amd64: OK
   ```
3. **Atomic swap on the VPS** (scp onto a running binary fails with ETXTBSY):
   ```
   scp /tmp/mino-esc root@VPS:/usr/local/bin/mino.new
   ssh root@VPS 'chmod +x /usr/local/bin/mino.new && mv /usr/local/bin/mino.new /usr/local/bin/mino'
   ssh root@VPS 'sha256sum /usr/local/bin/mino'   # must match your local build
   ```
4. **Record it** — the emergency lane bypasses the code-generated
   `deployments.log`, so append the line yourself (and note: the self-updater
   writes to `MINO_HOME`, so run manual updates as
   `MINO_HOME=/home/mino/.mino mino update` (the Mino home — a bare
   `mino update` as root logs to root's own home):
   ```
   ssh root@VPS 'echo "$(date -u +%FT%TZ) update=vX.Y.Z-EMERGENCY sha256=<sum> binary=/usr/local/bin/mino" >> /home/mino/.mino/deployments.log'
   ```
5. **Restart** only when no playbook run is in flight:
   ```
   ssh root@VPS 'systemctl restart mino && systemctl is-active mino'
   ```

## Extensions (minowrap, threads-extension, cost-watch)

The same shape: build from the tag, verify against `SHA256SUMS.txt` (they are
release assets since REL-05c), swap via `.new` + `mv`, restart the service.
cost-watch is a manual-only unit (invisible to deploy.sh); its swap procedure
is identical with `/opt/mino-cost-watch/cost-watch`.

## After an emergency deploy

Open a follow-up issue naming the incident and why the release path failed —
the emergency lane is a symptom, not a workflow.
