# Context Truth — Cancel-intent recognition

Status: **RESOLVED** (closes GitHub issue #148)

## Question

Why did "Its fine then, ill get this data myself" not stop the run, and how do we recognize cancel intent reliably?

## Resolution

`isStopMessage` (app.go) extended in two layers:

1. **Natural cancel phrases** matched anywhere in the message: `its fine`, `never mind`/`nevermind`, `ill/i'll do it myself`, `ill/i'll get this data`, `ill/i'll fetch this`, `forget it`, `dont/don't bother`, `lets/let's drop it`.
2. **Doubt guard:** after stripping cancel phrases + glue words (`then`, `myself`, `so`, `now`, `just`, `already`, `please`, `ok`, `okay`, `first`, `again`), if substantive text remains the message is a doubt or question, not a stop — the turn proceeds. This is the incident's message shape ("i think X is not supposed to be in it... its fine, ill get this data myself"): the doubt survives, and the turn is now cheap because CTX-002 (method tail) and CTX-004 (working note) hold the facts.

Leading `stop`/`cancel`/`halt` (after optional ok/okay/mino) unchanged; a rhetorical trailing `?` on a bare cancel ("never mind?") still stops.

## Acceptance criteria (all met)

- [x] "Its fine then, ill get this data myself" (no question) → short acknowledgment, no tool calls
- [x] Same message with a doubt/question → turn proceeds, bounded iterations (via CTX-002/004/006)
- [x] Existing "stop"/"cancel"/"halt" behavior unchanged
- [x] Table-driven tests at the isStopMessage seam (`TestStopMessageNaturalCancels`)

## Validation

- `go test ./...` — 503 pass
