# Context Truth — Cancel-intent recognition

Status: **OPEN**

## Question

Why did "Its fine then, ill get this data myself" not stop the run, and how do we recognize cancel intent reliably?

## Evidence

2026-08-10 23:04: the user's message contained a clear cancel ("ill get this data myself") plus a doubt ("i think chem 15 is not supposed to be in the inhouse consumption"). Mino ran 30 iterations and died at the cap. The interrupt machinery existed (`isStopMessage`, app.go:361, wired into telegram.go:166) but only matches:

- leading word `stop` / `cancel` / `halt` (after optional "ok/okay/mino" prefix)
- exact phrase `never mind` / `nevermind`

"It's fine then, I'll get this data myself" matches neither.

## Design sketch

- Extend `isStopMessage` vocabulary to natural cancel phrasings: "its fine", "ill do it myself", "ill get this data", "forget it", "nevermind" (middle-of-sentence), "dont bother", "never mind" (non-initial).
- Guard: a message that also contains an interrogative or a direct question ("is X supposed to be...") should NOT cancel — it's a question with a cancel tail. In the 2026-08-10 case the right behavior was: answer the CHEM 15 question in one short turn, do not start an expedition.
- Distinguish two modes: (a) cancel a *running* loop (existing `CancelTurn`), (b) cancel the *turn itself* — reply with a short acknowledgment, no tool use.

## Acceptance criteria

- [ ] "Its fine then, ill get this data myself" (no question) → short acknowledgment, no tool calls
- [ ] Same message with a question → answers the question, bounded iterations
- [ ] Existing "stop"/"cancel"/"halt" behavior unchanged
- [ ] Table-driven tests at the `isStopMessage` seam
