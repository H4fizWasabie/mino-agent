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

- Focused host/shell/rollback/service/coding/playbook tests: passed.
- Extension tests/builds: cost-watch (27 tests), threads (4 tests), minowrap (no tests); Windows/macOS cross-builds passed for tested extensions.
- Compile-only package check: passed.
- Cross-builds passed for Windows amd64, macOS amd64, and macOS arm64 with `sqlite_fts5`.
- Full `go test ./...` was attempted twice but exceeded the observed local runtime without output; it was stopped, so CI remains the authoritative full-suite gate.

## Deliberate boundary

Raw systemd content remains Linux-only compatibility mode. New neutral service definitions render to systemd, launchd, or Windows Service commands. Existing Bash playbooks run natively where Bash is installed; Windows-native stages use `script.ps1`. Exact runtime behavior still needs user reports from native Windows/macOS hosts.
