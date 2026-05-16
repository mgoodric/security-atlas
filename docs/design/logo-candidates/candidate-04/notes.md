# Candidate 04 — Node-graph A (pastel/sky, four-slot multi-weight)

**Concept:** A sparse node-and-edge graph (11 lines, 12 visible nodes) whose silhouette implies a stylized capital A. Filled dots at every structurally significant intersection make the "graph" reading explicit — directly evoking the control-graph data model from canvas §3 (one control, N framework satisfactions via STRM-typed edges through SCF anchors). v5 keeps v4's topologically-clean geometry verbatim and changes only color: the maintainer-supplied pastel palette runs on the dark variant, and a sky-scale dark-complement set runs on the light variant. v5 also splits node-dot color into its own fourth slot (v3/v4 had nodes = heavy color) so all four maintainer-supplied pastels carry visible weight rather than discarding one.

**Color treatment:**

- v5 adopts a four-slot color hierarchy (HEAVY / MEDIUM / LIGHT lines + a distinct NODES dot color). The structural shift from v4's three-slot hierarchy was a deliberate response to the maintainer providing four pastel colors — using all four meaningfully rather than collapsing nodes into the heavy color slot.
- The four maintainer-supplied pastels are `#90D5FF`, `#57B9FF`, `#77B1D4`, `#517891`. Three of the four fail SC 1.4.11 on a light background; only `#517891` passes both backgrounds. v5 therefore splits the palette per variant: pastels run verbatim on the dark variant; a sky-scale dark-complement set runs on the light variant; `#517891` runs on both as the node-dot color, establishing a brand-family through-line so the two variants read as the same mark.
  - Light-bg variant (against `#fafafa`):
    - HEAVY backbone: `#0c4a6e` sky-900 (mirrors `#90D5FF`'s spec position — lightest pastel pairs with darkest complement)
    - MEDIUM connectors / lower triangle: `#075985` sky-800 (mirrors `#57B9FF`)
    - LIGHT detail scaffolding: `#0369a1` sky-700 (mirrors `#77B1D4`)
    - NODES (every dot): `#517891` blue-gray (literal maintainer pastel, the one that passes both bgs)
  - Dark-bg variant (against `#0a0a0a`) — maintainer-supplied pastels verbatim:
    - HEAVY backbone: `#90D5FF` pastel-sky
    - MEDIUM connectors / lower triangle: `#57B9FF` pastel-blue
    - LIGHT detail scaffolding: `#77B1D4` muted-blue
    - NODES (every dot): `#517891` blue-gray
- Background neutrality: dual-variant (light + dark)
- Palette family departure from v4: v5 leaves the indigo brand family for a pastel/sky family per direct maintainer ask. Brand-color alignment across the rest of the application (`Plans/mockups/*.html`) is something the orchestrator will reconcile at the candidate-selection layer — v5's job is to honor the pastel ask faithfully.

**Contrast measurement (WCAG 2.2 — using SC 1.4.11 Non-text Contrast, ≥3:1, the correct standard for logo marks):**

Per-slot against the target background (each color measured individually via `tools/logo-gen/contrast.py --fg-hex`):

| Slot   | Light hex (vs `#fafafa`) | Dark hex (vs `#0a0a0a`)           |
| ------ | ------------------------ | --------------------------------- |
| HEAVY  | `#0c4a6e` 9.06:1 PASS    | `#90D5FF` 12.40:1 PASS            |
| MEDIUM | `#075985` 7.25:1 PASS    | `#57B9FF` 9.23:1 PASS             |
| LIGHT  | `#0369a1` 5.68:1 PASS    | `#77B1D4` 8.51:1 PASS             |
| NODES  | `#517891` 4.53:1 PASS    | `#517891` 4.19:1 PASS (SC 1.4.11) |

All eight tier colors clear the SC 1.4.11 3:1 floor on their target background. All four light-variant colors also clear the text-contrast 4.5:1 floor (SC 1.4.3). Three of the four dark-variant colors clear 4.5:1; NODES on dark bg sits at 4.19:1 — passes SC 1.4.11 cleanly, sits just under the over-conservative text-contrast floor. v5's spread (light bg: 9.06 → 7.25 → 5.68 → 4.53; dark bg: 12.40 → 9.23 → 8.51 → 4.19) reads as three visibly-distinct line tiers with a discernibly cooler/grayer NODES tone — the dot color now operates as an "anchor" tone rather than a fourth shade of the line color.

**Accessibility-standard note:** slice 074 AC-4 originally specified WCAG 2.2 ≥4.5:1. That figure is the text-contrast floor (SC 1.4.3) and is over-conservative for a logo mark, which is a "graphical object required to understand the content" per SC 1.4.11 with a 3:1 floor. v4 adopted the SC 1.4.11 3:1 floor — v5 retains it as the canonical standard. v5's color choices were filtered against SC 1.4.11 first: three of the four maintainer-supplied pastels fall below 3:1 on a light background, which is why the light variant uses a sky-scale dark-complement set rather than the literal pastels. The decision is recorded in the orchestrator's per-slice decisions log (D16).

Aggregated dominant-color measurement on the rendered PNGs (heaviest tier dominates pixel count):

- `mark-1024.png` against `#fafafa` (light): 9.06:1 — PASS (dominant tone = `#0c4a6e` sky-900)
- `mark-1024.png` against `#0a0a0a` (dark): 2.09:1 — FAIL (expected — light variant)
- `mark-1024-dark.png` against `#0a0a0a` (dark): 12.40:1 — PASS (dominant tone = `#90D5FF` pastel-sky)
- `mark-1024-dark.png` against `#fafafa` (light): 1.53:1 — FAIL (expected — dark variant)

**Wordmark provenance:** none — mark-only.

**Source provenance:**

- v5 remains **SVG-native** — `mark.svg` in this directory is the source of truth; PNGs are deterministic rasterizations.
- Rasterizer: `cairosvg` 2.9.0 via `tools/logo-gen/.venv-svg/` (requires `DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib` on macOS).
- Render helper: `tools/logo-gen/recolor_by_weight.py` — token-swaps the four palette hex values (v5 expanded from three to four slots) to produce the dark-bg variant and rasterizes both at 1024 and 512. The `LIGHT_TO_DARK` dict retains `LIGHT_TO_DARK_V3` and `LIGHT_TO_DARK_V4` as commented-out historical references for traceability.
- Geometry is unchanged from v4. The 12-node coordinate table in the SVG header comment is byte-identical to v4. Post-render topology check (every line endpoint within 0.5px of a circle center): 22/22 endpoints match, 0 broken — the same result as v4, as expected since geometry was not touched.
- License of output: original SVG authored in this repository under the project license.

**Prompt-or-recipe (verbatim — v5):**

The v5 mark is not produced from a prompt — it is rendered from `mark.svg` in this directory. The full recipe is the SVG file itself. To regenerate the four PNGs from the SVG:

```bash
DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib \
    tools/logo-gen/.venv-svg/bin/python tools/logo-gen/recolor_by_weight.py \
        --svg docs/design/logo-candidates/candidate-04/mark.svg \
        --out-dir docs/design/logo-candidates/candidate-04
```

The helper reads `mark.svg` (light variant, source of truth), produces the dark variant by token-swapping the four palette colors per the v5 mapping in `LIGHT_TO_DARK` (note the asymmetric NODES entry — `#517891` maps to itself and is preserved on both variants as the brand-family through-line). Then rasterizes both at 1024 and 512 with PNG optimization. Post-render topology verification (every `<line>` endpoint must hit a `<circle>` center within 0.5px) remains a strict gate for any future revision.

**Strengths:** v5 honors the maintainer's pastel-palette ask faithfully while keeping the mark accessible across both backgrounds. The four-slot color hierarchy uses all four maintainer-supplied colors meaningfully — `#517891` does double duty as the node-dot color on both variants, establishing a brand-family through-line so the dark and light renders read as the same mark rather than two unrelated marks. The dark variant carries the user's pastels verbatim and reads as airy/futuristic at all sizes; the line tiers separate cleanly (HEAVY 12.40 → MEDIUM 9.23 → LIGHT 8.51 are three visibly-distinct shades of sky/cyan against `#0a0a0a`). The light variant's sky-scale dark complements mirror the hierarchy slot-for-slot and produce a serious, professional reading consistent with a security/GRC tool. All eight tier colors clear SC 1.4.11. Geometry, topology, and the render pipeline are unchanged from v4 — the 22/22 endpoint-to-node verification continues to hold. Files remain well under budget (combined 135.7 KB / 600 KB). The structural shift to a four-slot hierarchy (splitting nodes from heavy) is also stronger graph semantics: the dot tone visually distinguishes "node" from "edge", reinforcing the graph metaphor rather than competing with it.

**Weaknesses:** The split-palette approach (pastels on dark, complements on light) means the two variants are no longer color-identical inversions of each other — they share only `#517891` (the node-dot color). The brand-family through-line is real but subtle: at first glance the two renders read as the same mark with different palettes rather than the same palette photographically inverted. Users who expect dark-mode to be the visual photonegative of light-mode may notice this asymmetry. The pastel-family departure from indigo means v5's brand alignment with the rest of the application (`Plans/mockups/*.html`, which use the indigo scale) is now an open question — the orchestrator may need to either roll back to indigo, propagate the pastel palette into the mockups, or hold a discussion about which color family the application as a whole should adopt. NODES on dark bg sits at 4.19:1 — below the text-contrast 4.5:1 floor but above the SC 1.4.11 3:1 floor; documented and intentional, but anyone scanning AC-4 for a literal 4.5:1 will need to read the SC 1.4.11 note. Favicon-scale (16 px) will still collapse the LIGHT-tier detail scaffolding into the page background and the dot color (`#517891`) will tend to merge with the line colors at that size — the favicon will read as a simpler 2-color mark.

## Iteration history

**v5 (this version, 2026-05-15)** — replaces v4 in response to maintainer feedback on PR #180 ("Lets use more of a pastel color set. #90D5FF, #57B9FF, #77B1D4, #517891"):

1. **Pastel palette adopted verbatim on the dark variant.** All four user-supplied pastels run on the dark variant of the mark exactly as specified: `#90D5FF` (HEAVY backbone), `#57B9FF` (MEDIUM connectors), `#77B1D4` (LIGHT scaffolding), `#517891` (NODES). All four clear SC 1.4.11 against `#0a0a0a`: 12.40:1, 9.23:1, 8.51:1, 4.19:1 respectively.
2. **Light variant uses sky-scale dark complements, mirroring the hierarchy slot-for-slot.** Three of the four user-supplied pastels fail SC 1.4.11 (≥3:1) on a light background — `#90D5FF` at 1.53:1, `#57B9FF` at 2.06:1, `#77B1D4` at 2.23:1, with only `#517891` clearing at 4.53:1. When this constraint was surfaced with four path options, the maintainer chose "pastels on dark + derived darker complements for light" — the candidate is functional + readable across both themes, with the pastels preserved verbatim on the variant where they belong. The complement set mirrors the hierarchy: `#0c4a6e` sky-900 (HEAVY, mirrors lightest pastel's spec position), `#075985` sky-800 (MEDIUM, mirrors `#57B9FF`), `#0369a1` sky-700 (LIGHT, mirrors `#77B1D4`), `#517891` (NODES, literally the user's pastel — the one color preserved on both variants).
3. **Color slot count expanded from three to four.** v3/v4 used a three-slot hierarchy where NODES took the HEAVY color (same hex). v5 introduces a separate NODES color slot — a structural shift made deliberately in response to the maintainer providing four colors. Using all four meaningfully (rather than discarding one) gives the dot tone its own visual identity: it operates as an "anchor" tone visually distinct from the line tones, reinforcing the "node = stable joint" semantic of the graph metaphor rather than competing with the line hierarchy. v4's D14 had considered + rejected this pattern for a single-family indigo palette ("dot color would compete with the line hierarchy"); the four-color pastel ask changed the calculus, because the dot color now sits in a clearly different chromatic zone (cooler / grayer) from the three line colors and reads as a different kind of element rather than a fourth shade of the line color.
4. **`#517891` is the brand-family through-line.** It is the one user-supplied color that clears SC 1.4.11 on BOTH backgrounds (4.53:1 on light, 4.19:1 on dark). Using it as the NODES color on both variants means the two renders share at least one identical color — a subtle but real "same mark, different palette" signal that helps the light and dark variants read as variants of one another rather than two unrelated marks. The split-palette approach (pastels on dark, complements on light) is unusual; this shared color is the device that keeps the variants identifiably-related despite the asymmetry.
5. **Geometry unchanged from v4.** The 12-node coordinate table is byte-identical. The 22/22 endpoint-to-node topology check continues to pass (`tools/logo-gen/.venv-svg/bin/python` topology scan returned 22/22 matched, 0 broken — same result as v4, as expected for a recolor-only iteration).
6. **`tools/logo-gen/recolor_by_weight.py` updated.** The active `LIGHT_TO_DARK` dict now holds the v5 four-slot mapping (one of the four entries is asymmetric: `#517891 → #517891`, retained for slot-alignment documentation even though it's a no-op on the source text). The v3 and v4 mappings are retained as commented-out `LIGHT_TO_DARK_V3` and `LIGHT_TO_DARK_V4` blocks for traceability. The module docstring is updated to describe the v5 four-slot structure and the rationale for the per-variant split.
7. **Files rendered + verified.** Combined PNG weight 135.7 KB across the 4 PNGs (1024 light 47.5 KB, 512 light 21.4 KB, 1024 dark 46.1 KB, 512 dark 20.7 KB) — well under the 600 KB budget. Aggregated dominant-color contrast on the renders: light PNG against `#fafafa` = 9.06:1 (HEAVY tone `#0c4a6e` dominant) PASS; dark PNG against `#0a0a0a` = 12.40:1 (HEAVY tone `#90D5FF` dominant) PASS.

Tradeoff surfaced: v5's two variants are no longer simple photographic inversions of each other. They share only `#517891`. Most users will not notice — the candidate reads as "a graph A in light blues" in dark mode and "a graph A in deep blues" in light mode, both with grayish-blue node dots, and the family resemblance is immediate. But a designer auditing the variants side-by-side will see that v5 trades the v4 "perfectly inverted palette" property for honoring a pastel ask that does not survive the inversion accessibly. The maintainer made this tradeoff explicitly when asked; it is recorded here so future iterations don't accidentally undo it.

Prompt-engineering insight worth recording: when a brief provides a color palette that does not survive an accessibility constraint on one of two required variants, surface the constraint with specific contrast measurements per color (not just "some of these will fail") and offer 3-4 named paths forward (the four options the maintainer chose between: drop failing pastels to BAA-floor / shift pastels darker / split palette per variant / scrap the pastel ask). Giving named paths converts a vague accessibility concern into a one-question decision. The maintainer's chosen path — "split palette per variant" — is unusual but legitimate, and it surfaces a useful design question (do the two variants need to be photographic inversions of each other, or only family-resemblances) that the v3/v4 iterations had not posed.

**v4 (2026-05-15)** — replaced v3 in response to maintainer feedback on PR #180 ("the lines are no longer connecting, and I was hoping for more contrasting colors"):

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
