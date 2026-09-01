# Design: cross-platform host operations

## Problem

Mino ships binaries for Linux, macOS, and Windows, but `HostTools` and the privilege bridge are hard-coded to Debian/systemd conventions. The core harness already runs across platforms; only the host-operation implementation needs platform-specific behavior.

## Approach

Use small platform capability interfaces and one adapter per supported OS, selected at construction. Keep the current Linux implementation as the compatibility baseline, and put macOS/Windows shell commands, paths, privilege transport, package probes, service lifecycle rules, browser launching, and update restart behavior inside their adapters. Preserve the existing model-facing tool names and schemas where they remain semantically valid.

Rejected: scattered `runtime.GOOS` branches in each tool, because they duplicate policy and make guardrail coverage easy to miss.

Rejected: arbitrary cross-platform shell execution as a single universal command, because shell availability and privilege semantics differ and it would bypass the existing argument validation and allowlist. Native shell adapters still execute the existing user-requested command surface with explicit prerequisite and failure reporting.

## Interfaces

| Name | Signature | Purpose |
|------|-----------|---------|
| Host operations | existing `HostTools` tool surface | Keep model-facing package/service tools stable. |
| Platform adapter | private interfaces covering shell execution, package install/probe, service resolve/restart, neutral service rendering/install, system-health probes, browser launch, and privileged command execution | Hide OS-specific implementations from user-facing tools. |
| Linux adapter | private adapter selected on Linux | Preserve apt-get, dpkg-query, systemctl, sudoers, and systemd behavior. |
| macOS adapter | private adapter selected on Darwin | Use native shell/tool paths, Homebrew, launchd/launchctl, browser opening, and macOS privilege execution. |
| Windows adapter | private adapter selected on Windows | Use PowerShell, native command equivalents, winget or Chocolatey, Windows service control, browser opening, and Windows elevation. |
| `write_unit` definition | existing tool name with a new neutral schema: name, executable, args, environment, working directory, restart policy | Render native systemd, launchd, or Windows Service configuration while retaining journal/rollback behavior. Raw systemd content remains Linux compatibility input. |
| Playbook execution | existing script-stage and system-check surfaces | Run supported scripts/probes through the selected platform adapter and report unsupported prerequisites explicitly. |
| Update restart | existing update lifecycle seam | Delegate post-update restart to the selected adapter and report manual restart when no managed service exists. |

The interface accepts structured arguments and typed operation inputs; no caller supplies a shell line. Existing journal entries remain the operation receipt.

## Config Surface

| Key | Type | Default | When absent |
|-----|------|---------|-------------|
| None | — | — | Existing environment configuration remains unchanged. Package-manager discovery and platform defaults are automatic; unsupported or unavailable tools fail clearly. |

## Data Flow

1. Mino constructs platform capabilities during core assembly.
2. Construction selects the OS adapter while retaining the existing model-facing tools.
3. A tool validates its input, asks the adapter for the platform command and pre-state, and checks the platform privilege policy.
4. The adapter executes the selected native command with the existing timeout/cancellation context and explicit shell policy.
5. The tool records before/after state through `OpJournal` and returns the adapter result.
6. Playbook scripts and system-health probes use the same selected adapter rather than embedding Unix commands in the scheduler or script runner.
7. Update verification and release-asset selection remain shared; platform restart and service-presence handling use the adapter.

## Failure Behaviour

| Failure | Behaviour |
|---------|-----------|
| Package manager missing | Return an explicit unsupported/prerequisite error; do not fall back to a Linux command or silently install a manager. |
| Service manager missing or service absent | Return a clear operation error; do not claim restart success. |
| Privilege denied | Return the OS error, journal the failed operation where the existing tool does so, and preserve the no-shell boundary. |
| External command timeout | Cancel the process, return a timeout error, and record failure; no automatic retry. |
| Cancellation | Propagate context cancellation to the command and return an incomplete/failed result; do not report success. |
| Malformed model input | Reject before adapter execution, as Linux tools do today. |
| Update restart unavailable | Keep the verified binary swap result, report that manual restart is required, and do not invent a running-service result. |
| Adapter selection unsupported | Build and run the core normally; host tools return an explicit unsupported-platform error. |
| Native shell/tool prerequisite missing | Return an actionable prerequisite error and do not silently switch to a different shell or command language. |
| Existing playbook script is not executable on the native platform | Return a clear stage failure naming the missing execution prerequisite; preserve the run evidence and do not claim completion. |

## Invariant Check

| Invariant | Verdict | Note |
|-----------|---------|------|
| Model agnosticism | Held | The model-facing tool contract does not name a provider or OS command. |
| Loop termination | Held | Existing host-operation timeout remains the bound; adapter commands inherit cancellation. |
| Context is managed | Held | No prompt or tool-result growth is introduced. |
| Guardrails are not optional | Held | Validation and privilege checks remain at the host-operation seam, before adapter execution. |
| Failure is explicit | Held | Missing tools, denial, timeout, cancellation, and unsupported platforms are returned explicitly. |
| State stays local and inspectable | Held | Existing operation journal and platform-local state remain authoritative. |
| Single binary, no framework | Held | Use standard-library process/filesystem APIs and existing platform commands; no new dependency. |

## Files to Touch

- `host_tools.go` — route existing host tools through the adapter while preserving public schemas and journal behavior where valid.
- `privilege.go` — retain Linux policy and separate platform privilege execution/policy as needed.
- `tools.go`, `coding_tools.go` — route shell and Unix command assumptions through platform command adapters.
- `playbook_script.go`, `playbook.go` — route script execution and system-health probes through platform capabilities.
- `rollback.go` — use the adapter for post-update restart and service detection.
- `app.go` — construct the selected adapter.
- `main.go` — complete Windows browser opening behavior.
- New platform adapter files and their tests, preferably split by OS where compile-time dependencies differ.
- `host_tools_test.go`, `privilege_test.go`, `update_test.go`, `tools_test.go`, `dashboard_eval_test.go`, and new adapter contract tests.
- extension release build scripts and manifests where platform assets are currently native-only.
- `.github/workflows/ci.yml`, `build-release.sh`, or release verification for the full platform matrix.
- `README.md`, `docs/decisions.md`, and `CHANGELOG.md`.

## Out of Scope

- Arbitrary root/administrator shell execution or approval-model changes.
- Firewall, user/account, registry, scheduled-task, launch-agent discovery beyond the existing service/unit tool contract.
- New package managers beyond the first supported manager per platform unless detection requires a fallback.
- Native hardware certification; hosted CI and local Linux fake-runner tests are the initial evidence.
