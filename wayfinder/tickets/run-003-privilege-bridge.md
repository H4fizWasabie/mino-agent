# Runtime Self-Management — Privilege bridge & host tools

Status: **RESOLVED** (wayfinder ticket, RUN-003 — GitHub issue #217)

Resolved 2026-08-16: `privilege.go` + `host_tools.go` — the sudoers command
whitelist as transport, harness-native tools as interface, no helper daemon.
`install_package` / `write_unit` / `restart_service` are the ONLY way Mino
touches host state: arg-validated, whitelist-checked (membership = the
autonomous/approval boundary RUN-006 reads later), journaled through
OpJournal (RUN-002) with before/after state, and torn back down when the
journal fails. The bash tool refuses `sudo` outright. Tests:
`TestWhitelistAllows`, `TestWhitelistSudoersRoundTrip`,
`TestLoadWhitelistUnreadable`, `TestContainsSudoInvocation`,
`TestParseUnitShow`, `TestInstallPackageJournaledAndSudoExactCommand`,
`TestInstallPackageRefusedWhenNotWhitelisted`,
`TestInstallPackageInvalidName`, `TestInstallPackageSudoFailureJournaledFailed`,
`TestInstallPackageJournalFailureRollsBack`,
`TestInstallPackageJournalFailureKeepsPreInstalled`,
`TestWriteUnitJournaledAndSudoExactCommand`, `TestWriteUnitReplacesExisting`,
`TestWriteUnitInvalidInput`, `TestWriteUnitJournalFailureRestoresOld`,
`TestWriteUnitJournalFailureRemovesNewUnit`,
`TestRestartServiceJournaledBeforeRestart`,
`TestRestartServiceRefusesForeignUnit`,
`TestRestartServiceJournalFailureAborts`,
`TestRestartServiceFailureMarksJournalFailed`,
`TestRestartServiceNotWhitelisted`, `TestBashRejectsSudo`.

## Question

Host-level operations (packages, systemd units, Mino's own service) need
root, but the LLM must never hold raw privilege: the harness owns every
privileged invocation, and the boundary between autonomous and owner-
approved operations is the whitelist.

## Decisions so far (recorded on #213/#217, 2026-08-16)

- **Transport: sudoers command whitelist, no helper daemon** — the mino
  user may run EXACT binaries as root, never a shell, never ALL.
- **Whitelist entries are exact fixed-prefix commands**, one entry per
  command shape — membership = args prefix-match: `apt-get install -y`,
  `apt-get remove -y` (journal-failure teardown must have membership),
  `systemctl restart`, `systemctl daemon-reload`, `install -o root -g root
  -m 0644 <home>/tmp/* /etc/systemd/system/*` (fixed flags, source pinned
  under the staging dir, target pinned under the unit dir — escalating
  means bypassing the harness's fixed-prefix enforcement, not just
  sudoers), and `/bin/rm -f /etc/systemd/system/*` (the old-content-empty
  journal-failure edge).
- **Sudoers `*` matches slashes in ARGS** (documented security-note
  example: `/bin/rm *` matches `rm -rf /`); the no-slash rule applies only
  to command names.
- **`mino setup-privileges`** (run as root) writes the file from the same
  canonical entries the harness checks against — the file is the boundary,
  the two never drift; the file is chmod 0444 (sudoers forbids group/world
  WRITE, not read) so the mino user can read its own boundary.
- **journal-failure teardown**: the host mutation runs first, then
  `journal.Run` commits the record; on journal failure the op is torn down
  (apt-get remove — only when the package was NOT already installed;
  restore the old unit content, or rm the unit when nothing existed
  before). Failures inside teardown are loud slog errors.
- **Self-restart is intent-first**: resolve the identity via `systemctl
  show -p Id -p UnitFileState <name>`, refuse when it doesn't match
  MINO_SERVICE (default `mino.service`) instead of trusting the env
  blindly, journal the intent BEFORE asking systemd, and only then
  `systemctl restart` — a successful restart kills this process; the
  journal entry is what boot reconciliation finds. systemd remains the
  only thing that keeps Mino alive.
- **Bash sudo guard**: conservative regex tripwire (`(?:^|[;&|(]|\s)sudo
  (?:\s|$)`) — false-positives are harmless refusals; the real enforcement
  is that the harness is the only thing that ever invokes sudo.
- **Host ops journal with mutate=nil** — there is no harness-owned state to
  write in the journal transaction; the mutation is the root-level command
  itself, and the before/after states carry the evidence.

## Out of scope

- Approval tier (RUN-006) — refusal messages name the boundary; the tier
  itself is a later ticket.
- Non-Mino service restarts — restart_service only touches MINO_SERVICE.
- sudoers wildcard-free install entries — unit names are dynamic, so the
  install entry carries `*` args; the harness's own check is stricter than
  the file.
