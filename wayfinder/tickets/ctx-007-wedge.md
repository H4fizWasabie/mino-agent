# Context Truth — Dashboard client disconnect can wedge the session mutex

Status: **OPEN** (GitHub issue #152)

## Question

Why did a dashboard turn whose client disconnected mid-turn block every later turn for that session until a restart?

## Evidence (2026-08-11 live test)

- **T3:** `curl --max-time 120` died at ~02:20, but the turn kept running (tool calls until 02:23), then went silent for 10+ minutes — the loop was inside a provider call that ignored context cancellation while holding `conversation.mu`. A follow-up turn (02:28) blocked on the mutex forever. Recovery required a service restart.
- **T5:** curl expired at 16 min; this time the loop noticed the cancel and released the mutex ("Stopped." logged, subsequent ping responded). **The wedge is intermittent** — it depends on whether the loop is inside a provider call that observes `ctx` when the connection dies.

## Mechanism

`RespondForContext` (app.go:294) holds `conversation.mu` for the entire turn. A client disconnect cancels `r.Context()`, but an in-flight provider call (`codex.go Create`) that does not observe the cancellation means the loop never returns — so every later turn for that session blocks on the mutex.

## Design sketch

- Provider calls must honor `ctx` (timeout on the HTTP call), so a cancelled turn returns bounded.
- Alternatively/additionally: decouple the turn's lifetime from the client connection (run with a detached context, reply by outbox/DB when the client is gone).
- A watchdog that force-releases a turn stuck past a deadline is the last resort.

## Acceptance criteria

- [ ] Kill the client connection mid-turn → session responsive within a bounded time
- [ ] No service restart needed to recover
- [ ] Regression test simulating a client disconnect mid-provider-call
