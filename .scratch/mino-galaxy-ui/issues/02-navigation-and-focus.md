# Galaxy navigation and focus contract

Status: resolved
Type: grilling
Blocked by: 01

## Question

How should guided orbit, zoom, pan, node/community focus, hidden-edge reveal, persistent detail inspection, browser history, keyboard navigation, and the accessible non-canvas index work together?

## Answer

Use a guided orbit centered on Mino. Dragging rotates the galaxy; panning is not the default interaction. Zoom is continuous with semantic transitions at overview, community, and entity thresholds. Selecting a node eases the camera toward it, reveals its local relationships, and opens the persistent inspector. Escape, background click, and browser Back clear or restore focus as appropriate. URLs deep-link the selected node but do not encode fragile pixel camera coordinates. This map is desktop-dashboard-first; mobile galaxy navigation is out of scope because Telegram is the normal phone interface. The renderer target is dependency-free native WebGL2, with a simplified desktop cluster fallback when WebGL2 is unavailable; the separate performance contract defines level-of-detail, budgets, and query behavior.

Live growth evidence on 2026-08-16: 888 memory Markdown files; `/api/universe` returned 1,519 nodes and 2,269 edges; distillation logs showed approximately one to two written outputs every five minutes. This makes scalable progressive disclosure a requirement, not a later optimization.
