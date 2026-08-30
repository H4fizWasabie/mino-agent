# Verification Matrix

What to check for each kind of surface. Stage 04 works the rows that apply.

| Surface changed | Check | Evidence that settles it |
|-----------------|-------|--------------------------|
| Agent loop | The bound is reached and respected | A run that hits the bound and stops cleanly |
| Agent loop | A malformed model response cannot prevent termination | A forced malformed response, loop still ends |
| Context management | Oversized input degrades predictably | A run with input past the limit, with observed behaviour recorded |
| Context management | Nothing silently drops without a trace | The trace or log line showing what was dropped |
| Guardrail | No path reaches the resource without enforcement | A search for a bypass path, and its result |
| Guardrail | The guardrail fires and reports | A forced violation and the message a user sees |
| Provider adapter | Behaviour matches across providers | The same operation on two providers, outputs compared |
| Provider adapter | A provider failure surfaces | A forced failure and the observed report |
| Telegram surface | The change is reachable from Telegram | The message sent and the response received |
| Telegram surface | Behaviour matches other surfaces | The same operation on Telegram and on the other surface |
| Config key | Default applies when absent | A run with the key removed |
| Config key | Invalid values are rejected clearly | A run with a bad value and the error text |
| State storage | New state is inspectable | The state read back with ordinary tools |

## Recording Results

Record what was observed, not what was expected. "Loop terminated at iteration 12 with a
bound of 12" is evidence. "Loop terminates correctly" is a claim.

A check that could not be run gets recorded as not run, with the reason. Silence reads as a
pass and that is how untested paths ship.

## Failing Honestly

If verification fails, write the report anyway. Record what failed, the evidence, and the
suspected cause. Then return to stage 03. A failed report is a normal artifact of a working
pipeline.
