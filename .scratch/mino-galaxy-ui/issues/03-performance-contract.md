# Galaxy performance contract

Status: resolved
Type: grilling

## Question

What measurable performance contract should the redesign meet, and which minimum mechanisms are required: idle rendering suspension, level-of-detail, event-only animation, bounded hit testing, incremental updates, DOM reduction, and a mobile fallback?

## Answer

Plan for both 100k total Universe nodes and approximately 1M edges. The browser receives a coarse overview first, then requests communities and local neighborhoods on demand; it does not download the complete graph as the first or every refresh payload. Native WebGL2 uses dynamic visible budgets based on zoom and device capability, with hard safety caps. Overview renders regions and anchors, community view renders the selected community, and entity view renders the selected neighborhood and relevant paths. Edges remain hidden until focus. Orbit and zoom target 60 FPS on a modern desktop with a 30 FPS floor; when the floor is missed, Mino reduces labels, edges, and visible nodes before interaction stutters. Search uses the existing graph query boundary and returns matches plus a small neighborhood. The coarse galaxy appears immediately and refines as data arrives. Mobile galaxy work is out of scope; the fallback is for desktop browsers without WebGL2.

Live evidence: 888 memory Markdown files, 1,519 current Universe nodes, 2,269 edges, and roughly one to two distillation writes every five minutes on 2026-08-16. Growth is therefore treated as a first-class input to the contract.
