# Mino Galaxy UI/UX Redesign — Wayfinder Map

## Destination

An implementation-ready specification for a lightweight 3D orbital galaxy surface covering Mino's durable owner-relevant universe: Memory, Responsibilities, Routines, and Conversations. The galaxy is for exploration; Today and Work remain the operational surfaces.

The specification must preserve truthful data and existing capabilities while defining stable spatial navigation, guided orbit, focus/inspection behavior, responsive fallback, and a measurable performance contract.

## Notes

- Discussion and planning only until the owner explicitly approves implementation.
- Consult `docs/dashboard-design.md`, `docs/rules.md`, and `docs/coding-conventions.md`.
- Preserve Mino's light visual system; galaxy depth does not imply glow, neon, fake activity, or decorative particles.
- Existing backend topology and routes remain in scope for preservation, not repair.
- Current live baseline: `v2.11.0`; the frontend serves `app.js`, `universe.js`, and `style.css` as the current dashboard surface.

## Decisions so far

- [Galaxy spatial model](issues/01-spatial-model.md) — Orbital Galaxy selected: guided orbit around Mino, progressive cluster-to-node disclosure, hidden edges until focus, and short localized activity trails.
- [Galaxy navigation and focus contract](issues/02-navigation-and-focus.md) — Desktop-first guided orbit with semantic zoom, focused local edges and inspector, deep-linked selection, desktop cluster fallback, and dependency-free native WebGL2.
- [Galaxy performance contract](issues/03-performance-contract.md) — Plan for 100k nodes and roughly 1M edges through coarse-first loading, on-demand neighborhoods, dynamic WebGL2 budgets, 60 FPS target/30 FPS floor, and immediate refinement.
- [Galaxy visual language](issues/04-visual-language.md) — Use scale, perspective, and occlusion for depth; restrained community hue families; redundant node-kind signals; explicit attention/state; metadata-led recency; and finite localized activity trails.
- [Galaxy surface boundaries](issues/05-surface-boundaries.md) — Galaxy is read-only exploration and navigation. Today, Work, Memory, Routines, System, and Conversations retain canonical operational ownership.
- [Scalable graph data delivery](issues/06-graph-data-delivery.md) — Keep `/api/universe` backward-compatible and add read-only overview, community, entity, search, and revision-aware projections.
- [Native WebGL2 renderer contract](issues/07-webgl2-renderer-contract.md) — Use dependency-free WebGL2 with instanced buffers, bounded focused edges, GPU picking, HTML overlays, LOD budgets, and a 2D fallback.
- [Search-to-orbit contract](issues/08-search-to-orbit-contract.md) — Preserve `/api/memory/remember` ranking; explicit result selection focuses the camera, loads bounded context, reveals local paths, and deep-links the selected node.

## Not yet specified

- Exact implementation and rollout sequence after prototype validation.
- Exact GPU budgets and projection response shapes, to be measured during implementation.

## Out of scope

- Changing or repairing Universe API data, graph facts, edges, communities, or runtime state.
- Redesigning Mino's operational domain model, Responsibility state, scheduler, or conversation backend.
- Replacing Today, Work, System, or Telegram with the galaxy.
- Production deployment or release work during this planning effort.
- Mobile galaxy navigation; Telegram remains the phone interface.

## Planning status

The Galaxy decision set is complete through issues #246–#250. The next artifact
is a throwaway prototype under `prototype/index.html`; it uses synthetic data,
does not change production routes or state, and exists to validate the visual
hierarchy, focus behavior, search-to-orbit flow, and fallback shape before any
production implementation.
