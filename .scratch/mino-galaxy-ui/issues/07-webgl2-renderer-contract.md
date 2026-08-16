# Native WebGL2 renderer contract

Status: resolved
Type: grilling
Blocked by: 03

## Question

What dependency-free WebGL2 rendering contract should define buffers, instancing, camera projection, picking, label overlays, level-of-detail transitions, GPU resource limits, and the simplified desktop fallback when WebGL2 is unavailable?

## Answer

Use dependency-free native WebGL2 for nodes, focused edges, scaffold, trails,
and selection states. Use instanced node buffers, bounded focused-edge buffers,
and interaction-triggered GPU picking. Keep controls, inspector, timeline,
visible labels, and the accessible index in HTML. Use overview, community, and
entity LODs with device-specific budgets, a 60 FPS target, and a 30 FPS floor;
reduce labels, edges, then nodes under pressure. Suspend rendering at rest.
Use the existing 2D canvas-style fallback when WebGL2 is unavailable.
