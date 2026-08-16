# Runtime Self-Management — Extension lifecycle (clone/build/register + supervision)

Status: **RESOLVED** (wayfinder ticket, RUN-001 — GitHub issue #215)

Resolved 2026-08-16: `manage_extension` tool + `ExtensionSupervisor` in
ext_supervisor.go. Install = clone (git) → build (`go build -o <name> .`) →
spawn on 127.0.0.1 → health-check (`GET /tools`) → register via the existing
discovery/proxy machinery. One runLoop goroutine per extension: restart on
crash with capped backoff (1s→30s, reset after a stable minute), SIGTERM→
SIGKILL on uninstall/shutdown, boot reconciliation re-spawns everything in
extensions.json. Install and uninstall are journaled
(`extension.install`/`extension.uninstall` with before/after = extensions.json
snapshots); uninstall links the install op via `UndoOf` and transitions it to
`rolled_back` through the new `OpJournal.SetStatus` seam (carry-forward from
the RUN-002 review). Failed installs journal `failed` with the config
untouched. Restarts do not re-register tools (proxy closures capture an
unchanged URL) — the supervisor's background goroutine never touches the
Registry; `toolsMu` (RWMutex) on Registry guards the map for the boot-time
registration path. Tests: `TestSupervisorBootSpawnsRegistersAndShutdownKills`,
`TestSupervisorRestartsCrashedExtension`,
`TestManageExtensionInstallUninstallJournaled`,
`TestManageExtensionInstallBadRepoFailsClean`, `TestManageExtensionToolValidation`,
`TestOpJournalSetStatus` — against a real fake extension (testdata/fakeext,
built in TestMain) exercising the actual §3 protocol.

## Question

Mino clones, builds, registers, and supervises its own extensions. Today a
human clones, builds, starts, and configures each extension by hand.

## Decisions so far

- **Stage 1 (shipped): in-process supervision, unprivileged** (#213 comment,
  2026-08-16): Mino spawns extension children, health-checks, restarts on
  crash, kills on shutdown; lifecycle tracks Mino's. The systemd-per-
  extension tier is a later opt-in needing RUN-003's privilege bridge — not
  built.
- **Install path: clone → build → register via existing machinery** — the
  extensions.json write reuses LoadExtensions' discovery/proxy registration;
  no new registration mechanism.
- **Build convention (fog item resolved, recorded on #213):** extensions are
  Go modules building with `go build -o <name> .` — matches every in-repo
  extension (minowrap: go.mod at root; cost-watch: prebuilt binary).
  Non-Go extensions fail install with a clear error.
- **Port convention:** the harness picks a free port (or takes one) and
  passes it to the child as the `PORT` env var; extensions that use their own
  env name (minowrap's `MINOWRAP_PORT`) declare it via the config's
  per-extension `env` override.
- **Health check = /tools reachable at spawn + process liveness (Wait).**
  The `/check` endpoint stays the alerting path (§18 alert checker); the
  supervisor does not kill on alerts — an alert may be informational.
- **Restarts don't re-register tools** — the URL is unchanged, so
  registrations survive the process swap; re-discovery would also race
  registry mutation with in-loop execution.
- **Crash ordering:** the config write happens only after first health, so a
  failed install never leaves a dead entry; the failure is journaled
  `failed` (before == after) with the clone removed. On journal failure the
  op is torn down (kill + restore config + rm clone) — no op without an
  entry, no entry without an op.
- **Registry concurrency:** `toolsMu` RWMutex added — registration is now
  background-adjacent (boot-time runLoop registration); all map readers go
  through it.

## Out of scope

- systemd-per-extension tier (stage 2, opt-in, needs RUN-003)
- Extension marketplace/distribution (map #213)
- Dockerized build environments (fog item — host Go toolchain decided)
- Configurable build commands per extension (YAGNI; `go build` covers every
  in-repo extension)
