# Ship note: #445

When a model response contains two or more tool calls that are all read-only (no side
effects, no owner approval needed) and none is `view_image`, the harness now runs them
concurrently instead of one at a time — the model waits for the slowest call in the batch
rather than the sum of all of them. Any batch containing a mutating or approval-gated call
runs exactly as before: fully sequential, one call awaited before the next starts.

This required no change to message history, trace shape, or the order results are recorded
in — every result is always processed in the model's original emission order, regardless of
which call actually finished first. Concurrency only ever affects wall-clock time.

Full concurrency (including mutating and approval-gated calls) was considered and rejected
during grilling: no incident motivates it, and it would add real new failure surface —
approval-gate contention, mutation-ordering risk if a model emits two dependent calls in one
response — for a latency win nothing has asked for at that scope.

## Config additions

None.

## Docs touched

- `CHANGELOG.md`: entry above. No existing doc described the prior one-call-at-a-time
  behavior at a level of detail this change would contradict.

## Migration notes

None. No interface, tool schema, or config surface changed.

## Known limitations

A hypothetical `BehaviorObserve`-classified tool with actual internal side effects could race
under concurrency — a property of correct tool classification generally (already relied on
by `stageRetrySafe` for playbook resume-safety), not a new risk this change introduces. No
such tool exists in the registry today.

Release/tag/deployment intentionally not performed, per the owner's instruction not to release
until the remaining approved Wayfinder issues are merged.
