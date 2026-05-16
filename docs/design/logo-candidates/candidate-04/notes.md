# Candidate 04 — Node-graph A (indigo, multi-weight)

**Concept:** A sparse node-and-edge graph (~11 lines, ~12 visible nodes) whose silhouette implies a stylized capital A. Filled dots at every structurally significant intersection make the "graph" reading explicit — directly evoking the control-graph data model from canvas §3 (one control, N framework satisfactions via STRM-typed edges through SCF anchors). v3 introduces a **three-tier weight hierarchy** where each weight is color-coded to a different rung of the indigo brand scale: the heaviest backbone defines the A spine, medium connectors carry the lower triangle, and light scaffolding lines hint at the outer network. The A-ness emerges from a topology that now reads in clear visual layers.

**Color treatment:**

- Light-bg variant uses three tiers of the indigo brand scale, all clearing WCAG AA on `#fafafa`:
  - HEAVY backbone + node dots: `#312e81` indigo-900
  - MEDIUM connectors / lower-triangle: `#4338ca` indigo-700
  - LIGHT detail scaffolding: `#4f46e5` indigo-600
- Dark-bg variant uses the lighter end of the same scale, all clearing WCAG AA on `#0a0a0a`:
  - HEAVY backbone + node dots: `#c7d2fe` indigo-200
  - MEDIUM connectors / lower-triangle: `#a5b4fc` indigo-300
  - LIGHT detail scaffolding: `#818cf8` indigo-400
- Background neutrality: dual-variant (light + dark)
- Aligned with the application's brand palette (`#312e81` / `#4338ca` / `#4f46e5` / `#6366f1` / `#818cf8` / `#a5b4fc` / `#c7d2fe` / `#e0e7ff`) used across `Plans/mockups/*.html`

**Contrast measurement (WCAG 2.2):**

Per-weight against the target background (each tier measured individually):

| Tier   | Light hex | vs `#fafafa` | Dark hex  | vs `#0a0a0a` |
| ------ | --------- | ------------ | --------- | ------------ |
| HEAVY  | `#312e81` | 10.94:1 PASS | `#c7d2fe` | 13.27:1 PASS |
| MEDIUM | `#4338ca` | 7.57:1 PASS  | `#a5b4fc` | 9.93:1 PASS  |
| LIGHT  | `#4f46e5` | 6.02:1 PASS  | `#818cf8` | 6.64:1 PASS  |

Aggregated dominant-color measurement on the rendered PNGs (heaviest tier dominates pixel count):

- `mark-1024.png` against `#0a0a0a` (dark): 1.73:1 — FAIL (expected — light variant)
- `mark-1024.png` against `#fafafa` (light): 10.94:1 — PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 13.27:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.43:1 — FAIL (expected — dark variant)

**Wordmark provenance:** none — mark-only.

**Source provenance:**

- v3 is **SVG-native** — `mark.svg` in this directory is the source of truth; PNGs are deterministic rasterizations.
- Rasterizer: `cairosvg` 2.9.0 via `tools/logo-gen/.venv-svg/` (requires `DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib` on macOS).
- Render helper: `tools/logo-gen/recolor_by_weight.py` — token-swaps the three palette hex values to produce the dark-bg variant and rasterizes both at 1024 and 512.
- Geometry was hand-authored to preserve the v2 Flux output's network topology (apex + two shoulders + two leg-bases + outer outriggers + cross-bar midpoint) while classifying every line by its structural role.
- License of output: this candidate is no longer model-generated; v3 is original SVG authored in this repository under the project license.

**Prompt-or-recipe (verbatim — v3):**

The v3 mark is not produced from a prompt — it is rendered from `mark.svg` in this directory. The full recipe is the SVG file itself. To regenerate the four PNGs from the SVG:

```bash
DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib \
    tools/logo-gen/.venv-svg/bin/python tools/logo-gen/recolor_by_weight.py \
        --svg docs/design/logo-candidates/candidate-04/mark.svg \
        --out-dir docs/design/logo-candidates/candidate-04
```

The helper reads `mark.svg` (light variant, source of truth), produces the dark variant by token-swapping the three indigo brand-palette colors per the mapping in `LIGHT_TO_DARK`, then rasterizes both at 1024 and 512 with PNG optimization.

**Strengths:** The node-and-edge graph reading is even more explicit at v3 because the three weight tiers — color-coded — create immediate visual layering: the eye reads the heavy backbone first (the A), then the medium support structure, then the light outer scaffolding. The hierarchy directly evokes the control-graph metaphor where some edges are primary (backbone controls) and others are derived (framework satisfactions). Every line color is on the application's brand scale and clears WCAG AA on its target background. Being SVG-native means zero generation drift on future renders — bit-identical output every time. File sizes are small (combined 132 KB across the 4 PNGs, against a 600 KB budget).
**Weaknesses:** Three weights + per-tier color is a richer reading at large sizes; favicon-scale (16 px) will likely collapse the light-tier detail scaffolding into the page background and may merge the medium tier with the heavy backbone tonally — the favicon will read as a simpler 2-color mark. The hand-authored geometry trades some of v2's organic Flux-rendered character for deterministic precision; the A is now slightly more geometric/regular and slightly less hand-drawn. The outrigger detail nodes/braces float in negative space — some viewers will read them as graph extensions; others may read them as decorative scaffolding the eye should ignore.

## Iteration history

**v3 (this version, 2026-05-15)** — replaces v2 in response to maintainer feedback on PR #180 ("more than 2 different line thicknesses with different colors for each weight of line that are aligned with the color pallet of the app"):

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
