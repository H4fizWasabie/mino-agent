# Harness — Updater: 30s client timeout too tight for the 22MB binary download

Status: **OPEN** (wayfinder ticket, CTX-021 — GitHub issue #177)

## Symptom

`sudo mino update` failed twice during the v2.8.13 deploy (2026-08-13) with:

```
Update failed: download: context deadline exceeded (Client.Timeout or context cancellation while reading body)
```

The binary was deployed via a manual SHA-verified scp instead.

## Root cause

`updateClient` (update.go) is a single `http.Client{Timeout: 30 * time.Second}` shared by both the GitHub releases-API check (`fetchLatestRelease`/`fetchLatestAsset`) **and** the 22MB binary download (`DoUpdate`'s `io.Copy`). The API check needs seconds; the binary download on a slow link needs minutes. One timeout serves both — so the download dies at 30s.

Note: the rc→release promotion fix (v2.8.12) worked correctly here — `v2.8.13-rc1 → v2.8.13` was detected as an update and the download started. Only the body transfer timed out.

## Fix

- Give the **download** its own longer-timeout client (e.g., 5 minutes) in `DoUpdate`, separate from the 30s API-check client.
- Small, isolated to update.go. SHA256 verification already guards the downloaded bytes.

## Acceptance criteria

- [ ] `mino update` completes a 22MB download from the VPS without hitting the timeout.
- [ ] The release-API checks keep their short timeout (no slow failure mode on a dead API).
- [ ] Test: the download path uses a timeout >= 60s (unit-testable via the client var).