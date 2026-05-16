# Candidate 04 — Node-graph A (indigo)

**Concept:** A sparse node-and-edge graph (~10 lines, ~10 visible nodes) whose silhouette implies a stylized capital A. Filled dots at every line intersection make the "graph" reading explicit — directly evoking the control-graph data model from canvas §3 (one control, N framework satisfactions via STRM-typed edges through SCF anchors). The A-ness emerges from network topology, not typography.

**Color treatment:**

- Mark color(s): light-bg variant uses Flux's rendered deep indigo `#000065` (in the indigo/brand-900 family of the application palette); dark-bg variant uses `#a5b4fc` indigo-300
- Background neutrality: dual-variant (light + dark)
- Aligned with the application's brand palette (`#6366f1` / `#4f46e5` / `#4338ca` / `#a5b4fc` / `#c7d2fe`) used across `Plans/mockups/*.html`

**Contrast measurement (WCAG 2.2):**

- `mark-1024.png` against `#0a0a0a` (dark): 1.12:1 — FAIL (expected — light variant)
- `mark-1024.png` against `#fafafa` (light): 16.94:1 — PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 9.93:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.91:1 — FAIL (expected — dark variant)

**Wordmark provenance:** none — mark-only.

**AI provenance:**

- Model: flux-1.1-pro (Replicate `black-forest-labs/flux-1.1-pro`)
- Model version: pinned by Replicate (latest as of 2026-05-15)
- Generation timestamp: 2026-05-16T (iteration v2)
- License of output: Flux 1.1 Pro commercial-use-OK per TOS; Apache-2.0 compatible.

**Prompt (verbatim — v2):**

```
Minimalist abstract logo mark. Sparse geometric node-and-edge graph composed of approximately eight thin straight lines forming an interconnected triangular network that subtly arranges itself into a stylized capital letter A shape, with open negative space inside. At every point where two or more lines intersect or terminate, place a small solid filled circle (a node dot) — these dots are about 1.5 to 2 times the line thickness in diameter and are clearly visible as discrete graph nodes. The mark must read as a NODE-AND-EDGE GRAPH (think network diagram, connected system, data graph), not as a wireframe mesh or truss. Indigo color #4f46e5 on pure white background. Flat 2D vector style, no shading, no gradients, sharp clean crisp lines. Centered composition with generous whitespace around the mark. The A shape emerges quietly from the network geometry — do NOT include any rendered typography, alphabet characters, or letterforms drawn as text. No words, no labels, no font. Fewer lines, more breathing room.
```

**Strengths:** The graph-with-visible-nodes reading is explicit and immediate — directly evokes the platform's control-graph data model. Node hierarchy (large primary nodes vs smaller secondary nodes) gives natural visual emphasis. Sparser than v1 — the negative space inside the A is open and readable. Indigo aligns with the application's brand palette used across the mockups. Distinct silhouette in the slate (only candidate that ships an explicit dot-graph reading).
**Weaknesses:** The A-suggestion is subtler than the geometric scaffold — viewers may read the mark first as "network triangle" and second as "A". Flux rendered the light-variant indigo darker than the prompted `#4f46e5` (came in around `#000065` / brand-900); the result is still in-band with the indigo family but is at the darker end of the brand scale. The node-density may not survive favicon-scale (16 px) cleanly — secondary halo dots will likely drop out.

## Iteration history

**v2 (this version, 2026-05-16)** — replaces v1 in response to maintainer feedback on PR #180:

1. **Dots at intersections** — added filled circular nodes at every visible line endpoint and crossing; the mark now reads as a node-and-edge graph rather than a wireframe.
2. **Fewer lines** — reduced from v1's dense triangular mesh (~12-15 segments) to ~10 visible segments with breathing room inside the A.
3. **Application color palette** — swapped out the burnt-amber `#bc4808` / `#fdba74` (which was out-of-band with the rest of the application) for the indigo brand family (`#000065`-ish light variant, `#a5b4fc` dark variant) used across `Plans/mockups/*.html`.

Generation: 3 Flux 1.1 Pro passes; v1-of-iteration was selected (best balance of A-suggestion + graph-character + color fidelity). v2-of-iteration over-rendered the A as a near-outlined letterform (drifted toward literal typography). v3-of-iteration leaned too far toward an abstract triangle and lost A-readability. The chosen pass was first-try in the iteration set.

Prompt-engineering insight worth recording: Flux's "indigo" attractor sits darker than the prompted hex value (rendered `#000065` against a requested `#4f46e5`). For tighter brand-color fidelity in future passes, consider Nano Banana (which respects hex values more literally) or post-generation recoloring via `make_dark_variants.py`-style alpha-preserving fill — both would let us land exactly on `#4f46e5` if needed.

**v1 (original, 2026-05-15)** — dense triangular wireframe forming a stylized capital A in burnt-amber `#bc4808` (light) / `#fdba74` (dark). Maintainer flagged on PR #180 that the mesh was too dense, lacked explicit "graph" semantics, and the warm amber color was out-of-band with the rest of the application's visual language.
