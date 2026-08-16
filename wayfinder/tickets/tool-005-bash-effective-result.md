# Tool — bash reports the effective result, not the pipe's tail exit code

Status: **IMPLEMENTED** (code + tests, closes #235)

## Why

`pip install --quiet playwright | tail -2` exited 0 → bash reported `status: ok` → **nothing was installed** (live 2026-08-16, verified: no playwright in site-packages, venv empty). The model built ~10 iterations of playwright work on a phantom package before compensating with a cookie-proxy hack that actually worked. Root causes: (a) `--quiet` suppresses output, (b) `| tail -2` masks the real exit code (no pipefail), (c) no post-install verification.

## Mechanism

1. **Pipe-masked exit detection** (tools.go): single-line commands containing a pipe are run with a one-shot PIPESTATUS capture appended (`__mino_cap="$(printf '%s %s' "$?" "${PIPESTATUS[*]}")"` — a command substitution forks before any new command runs, so it still sees the pipeline's statuses, where a plain assignment would reset PIPESTATUS first). The shell re-exits with the command's own status, so the reported exit code is unchanged. When the reported success came from the LAST element while an earlier element failed, the result gains an informational warning (`element 1 exited 7 — pipeline statuses (left to right): 7 0`); the call still succeeds. SIGPIPE death (141, `yes | head -1` truncation idiom) is not flagged. Multi-line commands (heredocs) are left unwrapped — a capture line cannot be appended safely; a missed detection is harmless.
2. **Quiet-install hint**: install-pattern commands (`pip|pip3|npm|npx|go|cargo|apt-get|brew install`) that produce NO output get an in-band verify hint instead of the generic no-output line. Deliberately NOT a package-presence probe — deriving the package name from the command is brittle (flags, extras, `-r` files, multiple packages), and the ticket's own conclusion was "prefer the prompt rule over brittle parsing".
3. **Prompt rule** (session.go): the verification discipline gains "after ANY install command, verify the package is actually present — pip show <pkg>, an import check, or which <bin> — BEFORE building on it", with the --quiet/pipe why.

## Tests

- `TestBashPipeMaskedExitWarning` — failing earlier element + succeeding tail → warning names the element and status; marker never leaks.
- `TestBashPipeSuccessNoWarning` — `echo ok | tail -1` unchanged (acceptance: no false positives).
- `TestBashPipeSigpipeNoWarning` — `yes | head -1` (141) not flagged.
- `TestBashPipeVisibleFailureUnchanged` — failing last element keeps the normal error path.
- `TestBashQuietInstallGetsVerifyHint` / `TestBashQuietNonInstallNoHint` — stub `pip` on PATH (no network): quiet install gets the verify hint, non-install keeps the generic line.
- `TestBuildSystemIncludesInstallVerificationRule` — the prompt rule is in the system prompt (repo convention).

## Acceptance criteria

- [x] `pip install --quiet X | tail -2` with X failing → the model sees the masked-exit warning (stub command test).
- [x] The prompt rule is present and testable (prompt-content test per convention).
- [x] No false positives: `cmd | tail` with cmd succeeding shows no warning.
- [x] Normal bash behavior unchanged for non-install, non-pipe commands.
