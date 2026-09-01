# Implementation manifest: issue #233

## Files changed

- `platform_host.go` — native package, service, privilege, and health command adapters for Linux, macOS, and Windows.
- `tools.go`, `tools_test.go` — select Bash on Unix and PowerShell on Windows while retaining Unix pipeline diagnostics.
- `coding_tools.go` — use PowerShell-native file listing, glob, and text search on Windows.
- `playbook_script.go`, `playbook_workspace.go`, `playbook_script_test.go` — prefer `script.ps1` on Windows, retain `script.sh` through installed Git Bash, and keep explicit prerequisite failures.
- `extensions/minowrap/main.go`, `extensions/cost-watch/main.go` — select native shell execution and native macOS signaling; report Windows hot-reload limitation.
- `host_tools.go`, `host_tools_test.go`, `service_definition.go`, `service_definition_test.go` — route package/service tools through the adapter and render neutral service definitions for each OS.
- `rollback.go` — route update-time service restart through the native adapter.
- `playbook.go` — use native service and recent-error probes in `system_check`.
- `main.go` — open the dashboard through Windows `cmd start`.
- `.github/workflows/ci.yml` — cross-build Linux, macOS, and Windows matrix.
- `README.md`, `CHANGELOG.md` — document native support and the systemd-unit boundary.

## Interfaces and configuration

- Added one private `hostPlatform` command seam; no public API or config keys.
- Existing model-facing tool names and schemas remain unchanged.
- No arbitrary administrator/root shell was added.

## Tests and builds

- `go test ./... -count=1`: passed.
- `go test ./... -race -count=1` and `go vet ./...`: passed.
- Cross-builds passed for Linux amd64, macOS amd64, and Windows amd64.
- Graph indexes refreshed with `graphify update .` and `codegraph sync`.

## Follow-up hardening in this PR

- Service identities are normalized before self-restart comparisons on native hosts.
- macOS restart/user resolution fails closed; Windows rollback follows the actual service existence probe.
- Neutral launchd environment fields are rendered; unsupported Windows fields are rejected rather than dropped.
- Package IDs, browser ports, unsupported platforms, and health-command output are bounded at their trust boundaries.

## Deliberate boundary

Raw systemd content remains Linux-only compatibility mode. New neutral service definitions render to systemd, launchd, or Windows Service commands. Existing Bash playbooks run natively where Bash is installed; Windows-native stages use `script.ps1`. Exact runtime behavior still needs user reports from native Windows/macOS hosts.
