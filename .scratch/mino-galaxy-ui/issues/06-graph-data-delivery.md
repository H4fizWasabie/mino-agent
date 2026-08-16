# Scalable graph data delivery

Status: resolved
Type: grilling
Blocked by: 03

## Question

What read-only API projection and update protocol should replace the eventual full-snapshot assumption: coarse overview, community detail, entity neighborhood, search results, and incremental refreshes while preserving the current graph query and topology contracts?

## Answer

Keep `/api/universe` fully backward-compatible. Add read-only Galaxy
projections for overview, community, entity neighborhood, and search, each
bounded to the requested level of detail and carrying a revision when
available. Existing `/api/query` and `/api/memory/remember` remain query
authorities. Edges are returned only when both endpoints are present; initial
refinement may refetch a projection, with true deltas deferred until measured.
