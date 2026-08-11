# Context Truth — Stop-word in non-leading position must stop

Status: **RESOLVED** (closes GitHub issue #155, commit pending)

## Question

Why did "Its fine, stop" queue as a normal turn instead of cancelling?

## Evidence (2026-08-11 full-suite live test, phase 13)

The heavy report task was running; "Its fine, stop" was sent. `isStopMessage` returned false: the phrase "its fine" matched, but after stripping phrases + glue words the remainder contained the word "stop" — counted as substantive text, so the message fell through to a normal turn and queued behind the running one. The task finished, then the queued message got a model-replied "Stopped." — workable outcome, wrong mechanism.

## Resolution

`isStopMessage` now treats a stop-word ("stop"/"halt") ANYWHERE as decisive, guarded against questions (message contains "?" or starts with a question word) so "why did you stop mid-task" still proceeds. Leading "stop/cancel/halt" behavior unchanged; "stopwatch" still not a stop (word-boundary).

## Validation

- New test: `TestStopMessageStopWordAnywhere` — "its fine, stop" / "please stop" stop; "why did you stop mid-task" and "stopwatch is on" do not
- `go test ./...` — 508 pass
