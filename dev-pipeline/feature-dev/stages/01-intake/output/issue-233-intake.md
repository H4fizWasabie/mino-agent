# Intake: cross-platform host operations

## Problem

Mino's released binaries run on Linux, macOS, and Windows, but the host-operation tools only work on Linux because they directly invoke `sudoers`, `apt-get`, `dpkg-query`, `systemctl`, and systemd paths. Non-Linux users therefore get the core harness but cannot use package or service management.

## Who is affected

Owners running Mino natively on macOS or Windows who need Mino to install tools, manage services, or complete its self-update lifecycle. The current workaround for Windows is WSL; macOS and Windows host tools fail closed.

## Smallest change

Preserve the existing model-facing tools and Linux behavior, then add platform adapters for every user-facing capability that currently assumes Unix/Linux: shell and coding-command execution, package installation and state checks, service installation/restart, privilege execution, browser opening, process/update restart handling, and platform-built extensions. Add deterministic fake-runner tests and hosted CI build/smoke coverage for native macOS and Windows.

## Growth risk and scope line

This could grow into a complete cross-platform system administrator, including firewall, users, scheduled tasks, registry, launch agents, and OS-specific installers. This change stops at capabilities Mino already exposes to users: coding/shell tools, packages, service/unit management, extensions, browser launching, and self-update.

## Rejection check

Not on the Do Not Build list. It preserves the single binary and adds platform-specific implementations behind the existing host-operation seam; it does not add a framework, scripting runtime, or provider-specific feature.

## Surfaces touched

- Shell/coding command execution and filesystem/process assumptions.
- Playbook script execution and system-health probes.
- Host-operation implementation and privilege transport.
- Self-update restart detection and execution.
- Extension build/release assets where platform binaries are required.
- Platform-aware browser opening only where needed for Windows parity.
- Tests and CI build/smoke matrix.
- README, design/decision references, changelog, and release notes.

The agent loop, context assembly, memory, playbooks, dashboard routes, provider adapters, and extension protocol are unchanged.

## Acceptance criteria

1. Linux package and service operations behave exactly as they do before the change.
2. On macOS, `install_package` uses an available supported package manager, and service operations use launchd; missing prerequisites fail clearly without attempting a Linux command.
3. On Windows, `install_package` uses an available supported package manager, and service operations use the Windows service manager; missing prerequisites fail clearly without attempting a Unix command.
4. Shell and coding tools use the native supported shell/command equivalents on Linux, macOS, and Windows; missing prerequisites fail clearly rather than silently using a wrong command.
5. Existing playbook scripts and system-health checks have a supported native execution/probe path on all three platforms, or fail with an explicit actionable error instead of silently skipping work.
6. Privileged operations never invoke an unrestricted shell and preserve the existing allowlist and journaling guarantees on every platform.
7. Self-update selects the existing platform release asset and either restarts through the platform service manager or reports the manual restart requirement clearly.
8. Extensions needed by the supported user-facing capability set are built or packaged for each supported release target, or are explicitly documented as unavailable.
9. Fake-runner tests cover success, missing prerequisite, timeout, cancellation, malformed command input, and failed external command paths for each adapter.
10. Linux, macOS, and Windows release targets cross-compile successfully; hosted CI runs platform smoke checks without requiring local macOS or Windows hardware.
11. Documentation states full native support and any genuine platform limitations accurately.

## Explicit non-goals

- Unrestricted root or administrator shell access.
- New host-management capabilities beyond the existing tools.
- A new dependency, framework, daemon, or cross-platform shell abstraction.
- Windows ARM or additional architectures beyond the existing release matrix.
- Claiming full native certification without a real platform smoke run.
