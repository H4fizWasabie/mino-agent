# Deploy Single Source of Truth — One Path to Production

Type: `wayfinder:grilling` (HITL — process change the owner must own)

## Question

What is the one canonical path from code to the VPS, so three agents can never again be on three different states?

## Context

- Today: GitHub, local, and VPS diverged; a release was cut from a stale master; a version label (v2.8.0) was deployed before the tag existed; a manual scp swap nearly shipped a stale binary (and did ship it once, caught by checksum mismatch).
- The self-updater exists and is version-checked — but manual deploys bypass it entirely.
- deploy.sh is a one-shot setup + push script, not a pipeline; it also resurrected dead units (now fixed).

## Ask

- Is the self-updater (release → VPS) the only path to production, with manual deploys banned — or is a manual path kept for emergencies (with a ledger)?
- Builds: only from tags, never from local trees? (Version labels must never precede a tag — today's v2.8.0 confusion.)
- Who may deploy: owner only, or any agent with a recorded "who/what/when" line?
