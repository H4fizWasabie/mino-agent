# Implementation manifest: issue #233

## Files changed

- `platform_host.go` — native package, service, privilege, and health command adapters for Linux, macOS, and Windows.
- `tools.go`, `tools_test.go` — select Bash on Unix and PowerShell on Windows while retaining Unix pipeline diagnostics.
- `host_tools.go`, `host_tools_test.go` — route package and service tools through the adapter; preserve Linux seams and reject systemd unit writing on non-Linux.
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

- Focused host/shell/rollback tests: passed (`27` tests in package).
- Compile-only package check: passed.
- Cross-builds passed for Windows amd64, macOS amd64, and macOS arm64 with `sqlite_fts5`.
- Full `go test ./...` was attempted twice but exceeded the observed local runtime without output; it was stopped, so CI remains the authoritative full-suite gate.

## Deliberate boundary

`write_unit` remains Linux-only because its input is systemd unit syntax. Non-Linux returns an explicit error before staging or execution. A future portable service-definition contract is a separate issue, not an implicit translation layer.
