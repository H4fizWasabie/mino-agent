# Interface Checklist

What every new or changed surface must answer before the design note is complete.

## Every Interface

- What is the signature, exactly?
- What does it do when given input it does not expect?
- Is it safe to call twice? If not, what stops that?
- Can it be cancelled mid-flight? What happens to partial state?
- Does the caller need to know which provider is behind it? The answer must be no.

## Every Config Key

- Name, type, default.
- What happens when it is absent. Not "it uses the default" but what the user observes.
- What happens when it is set to something invalid.
- Can it change at runtime, or only at start?
- Is this key permanent? Every key shipped is a key supported forever.

## Every Loop

- What bounds it? Iterations, time, tokens, or an explicit stop condition.
- What happens at the bound. Does it stop cleanly or report a failure?
- Can a malformed model response prevent the bound from being reached?

## Every External Call

- Timeout value and what happens on timeout.
- Retry policy, or an explicit statement that there is none.
- What the user sees when it fails.

## Every Guardrail

- What resource does it protect?
- Where is it enforced? The answer must be the boundary, not the call sites.
- Is there any path to the resource that does not pass the enforcement point?

## Every Interface Surface

For each of Telegram, CLI, HTTP, or any other surface the change touches:

- Does the change appear on this surface? If not, why not?
- Does the surface need a new message, command, or response shape?
- Does behaviour stay consistent across surfaces, or does it diverge for a stated reason?
