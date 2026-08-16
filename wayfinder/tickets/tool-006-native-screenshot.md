# Tool — native screenshot capability

Status: **CLOSED** (GitHub issue #239)

## Why

Live evidence 2026-08-16 (portfolio redesign task): the owner asked "if you use screenshot it would be great" — capturing authenticated JS-rendered dashboards behind login cost ~15 of 60+ bootstrap iterations: wkhtmltoimage discovery → curl+cookie attempts → failed playwright install (the #235 phantom) → a hand-written cookie-injecting proxy. The proxy worked, but the harness had `view_image` (read) and no `screenshot` (capture) tool.

## Fix

New `screenshot` harness tool (tools.go, registered in `BuildRegistry` as a mutate tool):

- **Capture:** URL (http/https only) or local file → render via the host's `wkhtmltoimage` binary (a host tool — the VPS has it and it works for static pages; no new repo dependency) → PNG saved under the durable spill store (`~/.mino/results/screenshots/`, RUN-007: `spillDir` routing, prunable by the 30-day retention) → the result states the target, IHDR-parsed dimensions, bytes, and the absolute artifact path for `view_image` round-tripping.
- **Honest failure (the #235 lesson, non-negotiable):** renderer missing → the result says so and names the requirement (`install_package`, `apt-get install -y wkhtmltopdf`, verify with `which wkhtmltoimage`); render error → exit error + stderr + the note that JS/auth pages need a headless browser (chromium-class via RUN-003's `install_package`) that Mino does not ship; empty output → no phantom success. Never an empty "ok".
- Input contract: exactly one of `url`/`path`; bad input is refused with the reason.

## Tests

- `TestScreenshotCapturesStaticURLAndFile` — real capture (httptest server + local file): real PNG, path under `spillDir(home)`, dimensions stated in the result, `view_image` reads the artifact, aged artifact pruned by `pruneSpills` (RUN-007). Skipped with a clear message when the sandbox lacks wkhtmltoimage — the honest-result contract applies to the tests too; no mock captures.
- `TestScreenshotHonestFailureWhenRendererMissing` — deterministic on any host (empty PATH): result text names the install requirement; no phantom artifact.
- `TestScreenshotRefusesBadInput`, `TestScreenshotAdvertisedInRegistry` (advertise-all discipline).

## Limitation reported (not mocked)

This sandbox has no `wkhtmltoimage` binary, so the real-capture test skips here with a message; the VPS has the binary, so it runs there (release lane / `stage-smoke` exercises the same environment).
