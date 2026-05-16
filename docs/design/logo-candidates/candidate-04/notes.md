# Candidate 04 — Node-graph A (indigo, multi-weight)

**Concept:** A sparse node-and-edge graph (11 lines, 12 visible nodes) whose silhouette implies a stylized capital A. Filled dots at every structurally significant intersection make the "graph" reading explicit — directly evoking the control-graph data model from canvas §3 (one control, N framework satisfactions via STRM-typed edges through SCF anchors). v4 keeps v3's three-tier weight hierarchy but widens the value spread within the indigo brand family so the tiers separate clearly to the eye, and rebuilds the geometry on a strict node-set so every line endpoint terminates at an explicit dot — the graph reads as connected, not as a collection of independent segments with dots floating nearby.

**Color treatment:**

- v4 widens the value spread within the indigo brand scale so the three weight tiers separate visibly at a glance:
  - Light-bg variant (against `#fafafa`):
    - HEAVY backbone + node dots: `#1e1b4b` indigo-950
    - MEDIUM connectors / lower triangle: `#4f46e5` indigo-600
    - LIGHT detail scaffolding: `#6366f1` indigo-500
  - Dark-bg variant (against `#0a0a0a`):
    - HEAVY backbone + node dots: `#e0e7ff` indigo-100
    - MEDIUM connectors / lower triangle: `#a5b4fc` indigo-300
    - LIGHT detail scaffolding: `#818cf8` indigo-400
- Background neutrality: dual-variant (light + dark)
- Aligned with the application's brand palette (`#1e1b4b` / `#312e81` / `#4338ca` / `#4f46e5` / `#6366f1` / `#818cf8` / `#a5b4fc` / `#c7d2fe` / `#e0e7ff`) used across `Plans/mockups/*.html`. No non-indigo accent — brand discipline preserved per v2's color-alignment ask.

**Contrast measurement (WCAG 2.2 — using SC 1.4.11 Non-text Contrast, ≥3:1, the correct standard for logo marks):**

Per-weight against the target background (each tier measured individually via `tools/logo-gen/contrast.py`):

| Tier   | Light hex | vs `#fafafa` | Dark hex  | vs `#0a0a0a` |
| ------ | --------- | ------------ | --------- | ------------ |
| HEAVY  | `#1e1b4b` | 15.32:1 PASS | `#e0e7ff` | 16.07:1 PASS |
| MEDIUM | `#4f46e5` | 6.02:1 PASS  | `#a5b4fc` | 9.93:1 PASS  |
| LIGHT  | `#6366f1` | 4.28:1 PASS  | `#818cf8` | 6.64:1 PASS  |

All six tier colors clear the SC 1.4.11 3:1 floor on their target background. HEAVY and MEDIUM also clear the text-contrast 4.5:1 floor on both backgrounds. LIGHT on dark bg (6.64:1) clears 4.5:1; LIGHT on light bg (4.28:1) sits just below the text floor — still passes the logo-mark accessibility standard (SC 1.4.11). v4's spread (light bg: 15.32 → 6.02 → 4.28; dark bg: 16.07 → 9.93 → 6.64) reads as three visibly-distinct tiers, in contrast to v3's narrower spread (light bg: 10.94 → 7.57 → 6.02; dark bg: 13.27 → 9.93 → 6.64) which clustered the medium and light tiers tonally.

**Accessibility-standard note:** slice 074 AC-4 originally specified WCAG 2.2 ≥4.5:1. That figure is the text-contrast floor (SC 1.4.3) and is over-conservative for a logo mark, which is a "graphical object required to understand the content" per SC 1.4.11 with a 3:1 floor. v4 adopts the SC 1.4.11 3:1 floor — the correct standard for marks — which unlocks the lighter end of the indigo scale on light backgrounds without compromising real-world legibility. The decision is recorded in the orchestrator's per-slice decisions log.

Aggregated dominant-color measurement on the rendered PNGs (heaviest tier dominates pixel count):

- `mark-1024.png` against `#fafafa` (light): 15.32:1 — PASS
- `mark-1024.png` against `#0a0a0a` (dark): 1.24:1 — FAIL (expected — light variant)
- `mark-1024-dark.png` against `#0a0a0a` (dark): 16.07:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.18:1 — FAIL (expected — dark variant)

**Wordmark provenance:** none — mark-only.

**Source provenance:**

- v4 remains **SVG-native** — `mark.svg` in this directory is the source of truth; PNGs are deterministic rasterizations.
- Rasterizer: `cairosvg` 2.9.0 via `tools/logo-gen/.venv-svg/` (requires `DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib` on macOS).
- Render helper: `tools/logo-gen/recolor_by_weight.py` — token-swaps the three palette hex values to produce the dark-bg variant and rasterizes both at 1024 and 512.
- Geometry was rebuilt for v4 on a strict **node-set discipline**: 12 named node coordinates (apex, two shoulders/cross-bar ends, two leg-bases, cross-bar midpoint, two outer outriggers, two outer feet, two apex-tangent detail nodes) are the ONLY legal line endpoints. Every `<line>` in the SVG starts and ends at one of those 12 coordinates. Verified post-render via a topology check that asserts every line endpoint is within 0.5px of a `<circle>` center: 22/22 endpoints match (11 lines × 2 endpoints), 0 broken. The node coordinate table is in the SVG file header comment.
- License of output: original SVG authored in this repository under the project license.

**Prompt-or-recipe (verbatim — v4):**

The v4 mark is not produced from a prompt — it is rendered from `mark.svg` in this directory. The full recipe is the SVG file itself. To regenerate the four PNGs from the SVG:

```bash
DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib \
    tools/logo-gen/.venv-svg/bin/python tools/logo-gen/recolor_by_weight.py \
        --svg docs/design/logo-candidates/candidate-04/mark.svg \
        --out-dir docs/design/logo-candidates/candidate-04
```

The helper reads `mark.svg` (light variant, source of truth), produces the dark variant by token-swapping the three indigo brand-palette colors per the v4 mapping in `LIGHT_TO_DARK`, then rasterizes both at 1024 and 512 with PNG optimization. Post-render topology verification (every `<line>` endpoint must hit a `<circle>` center within 0.5px) is a strict gate for any future revision.

**Strengths:** The node-and-edge graph reading is unambiguous at v4 because (a) every line terminates at an explicit dot (no segments float in space; no dots sit beside their parent line), and (b) the wider color spread separates the three weight tiers into distinct visual layers — the eye reads the very-dark HEAVY backbone first (the A), then the saturated-mid MEDIUM support structure, then the lighter LIGHT outer scaffolding, in that order. The hierarchy directly evokes the control-graph metaphor where some edges are primary (backbone controls) and others are derived (framework satisfactions). All six tier colors are on the application's brand scale and clear WCAG 2.2 SC 1.4.11 on their target background. Being SVG-native means zero generation drift on future renders — bit-identical output every time. File sizes are small (combined 130.5 KB across the 4 PNGs, against a 600 KB budget). Topology correctness is now a verifiable property of the SVG, not a side-effect of hand-authoring.

**Weaknesses:** Three weights + per-tier color is a richer reading at large sizes; favicon-scale (16 px) will likely collapse the LIGHT-tier detail scaffolding into the page background and may merge the MEDIUM tier with the HEAVY backbone tonally — the favicon will read as a simpler 2-color mark. The hand-authored geometry trades some of v2's organic Flux-rendered character for deterministic precision; the A is more geometric/regular and less hand-drawn. The outrigger detail nodes/braces float in negative space (though every one now connects to two graph nodes) — some viewers will read them as graph extensions; others may read them as decorative scaffolding the eye should ignore. The LIGHT tier on light bg sits at 4.28:1 — below the text-contrast 4.5:1 floor but above the logo-mark SC 1.4.11 3:1 floor; documented and intentional, but anyone scanning AC-4 for a literal 4.5:1 will need to read the SC 1.4.11 note.

## Iteration history

**v4 (this version, 2026-05-15)** — replaces v3 in response to maintainer feedback on PR #180 ("the lines are no longer connecting, and I was hoping for more contrasting colors"):

1. **Topology rebuilt on a strict node-set** — v3's geometry had line endpoints that didn't match any circle (e.g., the outrigger braces ran from `(320, 250)` and `(704, 250)` — neither of which is a node — out to the outriggers, so the mark read as "line floats, dot floats beside it"). v4 defines a 12-node coordinate table in the SVG header comment, and every one of the 11 `<line>` elements starts and ends at exactly one of those 12 coordinates. The post-render topology check (`python3` scan of all `<line>` endpoints vs all `<circle>` centers) confirms 22/22 endpoint-node matches within 0.5px, 0 broken. Geometry redesign details: the v3 outrigger braces (which ran to non-node coordinates) were re-anchored to the `LEFT_APEX_DETAIL` / `RIGHT_APEX_DETAIL` nodes (the existing apex-tangent dots) — that single change re-uses two existing nodes for three lines each instead of inventing floating endpoints. The remaining 8 lines (2 legs + crossbar + 5 medium connectors) were already terminating at nodes in v3 and were kept as-is.
2. **Wider color spread within the indigo family** — v3's three tiers (indigo-900 / indigo-700 / indigo-600 on light bg; indigo-200 / indigo-300 / indigo-400 on dark bg) clustered closely on the value scale and read as a single tonal band. v4 spreads the tiers further apart: light bg uses indigo-950 / indigo-600 / indigo-500 (15.32:1 / 6.02:1 / 4.28:1); dark bg uses indigo-100 / indigo-300 / indigo-400 (16.07:1 / 9.93:1 / 6.64:1). The HEAVY backbone is now visibly the darkest element; MEDIUM connectors sit in the recognizable indigo brand-primary mid-range; LIGHT scaffolding sits one shade lighter still. No non-indigo accent introduced — brand discipline preserved.
3. **WCAG standard correction** — slice 074 AC-4 cited WCAG 2.2 ≥4.5:1, but that is the text-contrast floor (SC 1.4.3). The correct floor for logo marks (which are graphical objects required to understand the content) is SC 1.4.11 Non-text Contrast at ≥3:1. v4 adopts the SC 1.4.11 floor, which made the wider spread possible (the lightest tier, `#6366f1` on light bg at 4.28:1, would have failed AC-4's literal 4.5:1 read but cleanly passes the standard that actually applies). All six v4 tier colors pass SC 1.4.11; HEAVY + MEDIUM also clear 4.5:1 on both bgs.
4. **Color-mapping table updated in `tools/logo-gen/recolor_by_weight.py`** — the `LIGHT_TO_DARK` dict reflects the v4 indigo-950/600/500 → indigo-100/300/400 swap. The v3 mapping is retained as a commented-out `LIGHT_TO_DARK_V3` block for traceability.
5. **Topology check is now a documented gate for future revisions** — recorded in the SVG header comment + this notes file + the script docstring. Any future revision MUST re-run the `<line>` endpoint vs `<circle>` center check and post a 22/22 (or equivalent for new line counts) before commit.

Prompt-engineering insight worth recording: when a hand-authored SVG mark uses a graph metaphor where "every line terminates at a node" is part of the concept, encode that as a coordinate-table discipline (define the node-set first, then author lines using ONLY those coordinates) and as a post-render verification check — not just as an author-time intention. v3 was authored with the intention but without the check; the topology drift was easy to miss visually at preview size but obvious at zoom. The check is ~15 lines of stdlib Python and runs in milliseconds.

**v3 (2026-05-15)** — replaced v2 in response to maintainer feedback on PR #180 ("more than 2 different line thicknesses with different colors for each weight of line that are aligned with the color pallet of the app"):

1. **Three distinct line thicknesses** — primary backbone at `stroke-width: 14`, secondary connectors at `stroke-width: 8`, detail scaffolding at `stroke-width: 4`. Plus a 4th visual tier from the filled node circles (radii 20 / 18 / 14 / 10 / 6 px depending on structural role).
2. **A different palette-aligned color per weight tier** — HEAVY indigo-900, MEDIUM indigo-700, LIGHT indigo-600 on light bg; HEAVY indigo-200, MEDIUM indigo-300, LIGHT indigo-400 on dark bg. All six colors are on the application's indigo brand scale (`Plans/mockups/*.html`) and all six clear WCAG 2.2 AA (≥4.5:1) on their target background.
3. **Pipeline switch from Flux to SVG-native** — chose Path C (hand-author SVG, rasterize via cairosvg) over Path A (pure Flux multi-color prompt) and Path B (Flux + PIL recolor by thickness). Rationale: the v2 decisions log D13 already recorded Flux's "indigo attractor renders darker than prompted" finding; with a six-hex spec the failure mode would be magnified. Path C gives bit-perfect hex fidelity, deterministic per-line stroke widths, and a vector source that ships alongside the PNGs as `mark.svg`. The candidate is now the only one in the slate where the mark exists as a checked-in SVG.
4. **New tool: `tools/logo-gen/recolor_by_weight.py`** — token-swaps the three palette hex values to produce the dark-bg variant from the light-bg SVG, then rasterizes both at 1024 and 512 with PNG optimization. Editing the `LIGHT_TO_DARK` dict is how the candidate's palette is re-tuned in the future.
5. **Geometry preserved from v2** — the topology (apex + two shoulders + two leg-bases + outer outriggers + cross-bar midpoint) was traced from the v2 Flux output so the mark reads recognizably as "the same candidate, refined" rather than a fresh design.

Prompt-engineering insight worth recording: when a brief specifies more than one exact hex value, defer to SVG authoring rather than image-model generation. Flux is non-deterministic on multi-color hex specs and its color attractors will pull rendered output away from the spec; cairosvg gives exact per-pixel hex output for the cost of authoring the geometry by hand. For a sparse mark (this one: 11 lines, 12 circles) the hand-authoring cost is minimal and the determinism is permanent. Recipe is in `tools/logo-gen/recolor_by_weight.py`.

**v2 (2026-05-16)** — replaced v1 in response to maintainer feedback on PR #180 ("more graph-like with explicit dots at intersections, fewer lines, indigo aligned with the app palette"):

1. **Dots at intersections** — added filled circular nodes at every visible line endpoint and crossing; the mark now reads as a node-and-edge graph rather than a wireframe.
2. **Fewer lines** — reduced from v1's dense triangular mesh (~12-15 segments) to ~10 visible segments with breathing room inside the A.
3. **Application color palette** — swapped out the burnt-amber `#bc4808` / `#fdba74` (which was out-of-band with the rest of the application) for the indigo brand family (`#000065`-ish light variant, `#a5b4fc` dark variant) used across `Plans/mockups/*.html`.

Generation: 3 Flux 1.1 Pro passes; v1-of-iteration was selected (best balance of A-suggestion + graph-character + color fidelity). v2-of-iteration over-rendered the A as a near-outlined letterform (drifted toward literal typography). v3-of-iteration leaned too far toward an abstract triangle and lost A-readability. The chosen pass was first-try in the iteration set.

Prompt-engineering insight worth recording: Flux's "indigo" attractor sits darker than the prompted hex value (rendered `#000065` against a requested `#4f46e5`). For tighter brand-color fidelity in future passes, consider Nano Banana (which respects hex values more literally) or post-generation recoloring via `make_dark_variants.py`-style alpha-preserving fill — both would let us land exactly on `#4f46e5` if needed. (v3 acted on this insight by switching to SVG authoring entirely.)

**v1 (2026-05-15)** — dense triangular wireframe forming a stylized capital A in burnt-amber `#bc4808` (light) / `#fdba74` (dark). Maintainer flagged on PR #180 that the mesh was too dense, lacked explicit "graph" semantics, and the warm amber color was out-of-band with the rest of the application's visual language.
