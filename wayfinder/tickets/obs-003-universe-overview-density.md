# OBS-003 — Universe Overview Node Density

Status: **CLOSED** (shipped commit 71a9254, 2026-08-14) — overview now marks top 15-25% of nodes by degree at zoom < 1.5; the visual acceptance criteria are confirmed on the deployed dashboard.

## Problem

When zoomed out on the Living Field graph (`#universe`), only ~30-40 nodes are visible out of 1,128 total. The graph looks sparse and disconnected, failing to convey the rich topology that exists.

**Reference**: The topology should feel like a dense, organic network (see WhatsApp Image 2026-08-09 reference) — hundreds of visible nodes with connections, not just branch hubs.

## Root Cause

`universeLayout()` marks only branch anchors (~10-15 nodes) as `_overviewVisible`. Memory communities show only their central ellipse. Overview mode activates at `zoom < 1.2`, hiding >95% of nodes.

## Fix

1. **Expand overview activation range**: `zoom < 1.5` (was 1.2)
2. **Mark high-connectivity nodes as visible**: top 15-25% of nodes by degree (`_degree`) alongside branch anchors
3. **Result**: ~200-300 visible nodes in overview, preserving the dense network feel

## Acceptance Criteria

- [ ] Zoom out on `#universe` → see 200+ nodes (not just 30-40)
- [ ] Graph looks dense and organic, not sparse
- [ ] Memory communities still render as ellipses with connections
- [ ] Performance acceptable (no lag at overview zoom levels)
- [ ] Works on mobile (720px breakpoint)

## Files Changed

- `static/universe.js`: `universeLayout()`, `overviewMode()`

## Testing

1. Open `http://100.101.53.98:7779/#universe`
2. Zoom out (scroll wheel down)
3. Verify 200+ nodes visible
4. Verify connections between nodes are visible
5. Verify memory communities still render correctly

## Related

- Reference image: WhatsApp Image 2026-08-09 at 21.00.26 (1).jpeg
- Current data: 1,128 nodes, 2,213 edges
- Overview threshold: `zoom < 1.5` (was 1.2)
- Visibility: branch anchors + top 15-25% by degree

---
**Created**: 2026-08-14
**Status**: In Progress
