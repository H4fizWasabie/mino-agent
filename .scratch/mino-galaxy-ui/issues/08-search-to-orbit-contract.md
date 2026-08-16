# Search-to-orbit contract

Status: resolved
Type: grilling
Blocked by: 03

## Question

How should a debounced query against `/api/memory/remember` map ranked matches and related graph paths into community focus, camera movement, local edge reveal, result selection, empty states, and recovery from stale or failed requests?

## Answer

Debounce and send the exact query to `/api/memory/remember`; preserve its
ranking. Keep the camera still while typing and expose an accessible result
list. Explicit selection loads bounded context when needed, focuses the
result's community, selects the node, reveals local paths, opens the inspector,
and updates the deep link. Empty, failed, or stale requests preserve current
Galaxy state and report truthfully. Search does not silently change the
timeline.
