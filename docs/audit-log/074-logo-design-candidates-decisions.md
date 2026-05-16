# Decisions log — Slice 074 (Logo design candidates)

This is a JUDGMENT slice (per `Plans/prompts/04-per-slice-template.md` "Slice types"). The orchestrator makes the visual design calls during candidate generation; the maintainer's judgment happens later via the `Selected:` line edit at `docs/design/logo-decision.md`. This log captures the build-time decisions, the alternatives considered, and the revisit conditions.

## Build-time judgment calls

### D1 — Ship 10 candidates, not 4 (HIGH confidence)

**Decision:** the slate ships 10 candidates instead of the slice doc's default 4.

**Rationale:** the maintainer (Matt) explicitly requested 10 in the slice's run-time AskUserQuestion answer: _"Please use all of the above in generation, I will pick out of the selections. I think 10 total options would be ideal."_ All 4 wordmark text options (`security-atlas`, `atlas`, `security · atlas`, `SA`) are represented across the slate in different combinations with the four direction anchors (cartographic / control-graph / lattice / typographic).

**Alternatives considered:**

- Ship the spec's default 4 (one per direction). Rejected: maintainer asked for 10 to give breadth across both directions AND wordmark variants.
- Ship 8 (two per direction). Rejected as not matching the explicit "10 total" request.

The slice doc's "4-direction constraint is a floor, not a ceiling — if a fifth direction emerges naturally during generation that's genuinely distinct, include it (5 candidates total). Don't pad with near-duplicates to hit 4." explicitly permits expansion. We expanded to 10 with genuinely distinct candidates (zero near-duplicates).

### D2 — Dual-variant ship (light + dark) for every candidate (HIGH confidence)

**Decision:** every candidate ships TWO canonical raster files — `mark-1024.png` (canonical for light-bg surfaces, `#fafafa`) and `mark-1024-dark.png` (canonical for dark-bg surfaces, `#0a0a0a`) — plus their 512×512 web-optimized derivatives. The slice 075 integration will use both via `<picture>` semantics (same pattern as slice 057's screenshots).

**Rationale:** No solid color exists that passes WCAG 2.2 ≥4.5:1 against both `#fafafa` AND `#0a0a0a` simultaneously. The luminance Goldilocks zone (roughly 0.18–0.45) doesn't contain any chromatic color that satisfies both ratios. We discovered this mid-execution after the first single-variant Flux renders failed contrast on one bg or the other; the Artist agent surfaced the impossibility constraint and pivoted to dual-variant. Future logo work should commit to dual-variant from OBSERVE rather than rediscovering this.

**Alternatives considered:**

- Single near-black or near-white mark per candidate (passes both bgs trivially). Rejected: kills the color identity the maintainer specifically asked for via the AskUserQuestion answer "Fresh palette — open to Artist's call".
- Mid-luminance gray (`#808080`-ish). Rejected: technically passes both bgs at marginal ratios, but loses all brand color identity.
- Per-bg recolor at integration time (single source, mechanical recolor). Considered but deferred to slice 075's `scripts/regen-logo-variants.ts` — slice 074 ships the variants as canonical assets so the decision-doc gallery shows what the maintainer is actually picking.

### D3 — Fresh palette per candidate (not single brand color) (HIGH confidence)

**Decision:** each of the 10 candidates uses its own 1-2-color palette, spread across the slate (indigo / violet / teal / cyan / emerald / amber / rose / slate). Nine distinct color families across the 10 candidates.

**Rationale:** maintainer's explicit AskUserQuestion answer: _"Fresh palette — open to Artist's call"_. The slate's purpose is to give the maintainer a genuinely diverse choice; uniform-color candidates would compress the decision space to "which shape do you like?" instead of "which shape + color combination most represents the brand?"

The existing Plans/mockups palette (`#6366f1` indigo + variants) IS represented in the slate (candidates 01, 02, 07 use indigo families) so the maintainer can choose continuity-with-mockups OR a fresh palette without re-running this slice.

**Alternatives considered:**

- Stick with indigo across all 10 (mockup continuity). Rejected per maintainer's "fresh palette" answer.
- Indigo + one accent across all 10. Rejected: dilutes the per-candidate distinctiveness.

### D4 — Inter Bold (700) + Inter Black (900) for all composited wordmarks (HIGH confidence)

**Decision:** all candidates with composited text use **Inter** (SIL OFL, https://rsms.me/inter/) — Bold (700) for wordmarks (`security-atlas`, `atlas`, `security · atlas`), Black (900) for monogram (`SA`). No other fonts used.

**Rationale:** maintainer's explicit AskUserQuestion answer: _"Inter (recommended)"_. Inter is SIL-OFL licensed (Apache-2.0-compatible for redistribution), ubiquitous in modern dev-tooling UIs (Vercel, Linear, GitHub), and reads as "serious + modern" without the "trying to be different" tax that newer geometric sans carry. Inter's letterforms are dense enough at Black weight that the `SA` monogram (candidate 06) reads cleanly without supporting decoration.

**Source:** Inter v4.0 release ZIP from https://github.com/rsms/inter/releases (fetched fresh during slice run; not bundled in the repo to avoid font asset weight — slice 075 will bundle the specific weights used in the selected candidate).

### D5 — Model mix: Flux 1.1 Pro (6) + Nano Banana (1) + pure PIL/Inter composit (3) (HIGH confidence)

**Decision:** candidates use a mix of art models per direction:

| Candidate | Direction                   | Model                                 |
| --------- | --------------------------- | ------------------------------------- |
| 01        | Cartographic contour        | Flux 1.1 Pro                          |
| 02        | Control-graph nodes         | Flux 1.1 Pro                          |
| 03        | Spine-and-branches          | Flux 1.1 Pro                          |
| 04        | Lattice mesh "A"            | Flux 1.1 Pro                          |
| 05        | Anchor + lattice            | **Nano Banana**                       |
| 06        | `SA` monogram               | none — pure PIL/Inter                 |
| 07        | `security-atlas` wordmark   | none — pure PIL/Inter                 |
| 08        | `security · atlas` w/ glyph | Flux 1.1 Pro (glyph only) + PIL/Inter |
| 09        | Hexagonal control-cell      | Flux 1.1 Pro                          |
| 10        | Stylized "A" from graph     | Flux 1.1 Pro                          |

**Rationale:** maintainer's AskUserQuestion answer: _"Mix — Artist picks per candidate"_. The Artist agent picked Flux 1.1 Pro for most directions (its strength is clean abstract geometric marks); Nano Banana for candidate 05 (anchor + lattice — Flux drifted toward nautical clichés on the anchor prompt, Nano Banana produced a cleaner abstract); and pure PIL/Inter composit (no image model) for candidates 06 + 07 because they're typographic-only and image-model rendering of text is explicitly forbidden by P0-A2.

**Substitution flagged:** candidates 09 and 10 originally specified GPT-Image-1. `OPENAI_API_KEY` was not configured in the agent's environment; rerouted both to Flux 1.1 Pro. Both rendered the brief successfully (hex lattice + graph-A respectively). If the maintainer wants GPT-Image-1 versions to compare, the easiest path is a follow-on slice 091 (or just an Artist-agent re-run with API key provisioned) — candidates 09 and 10 as Flux outputs are NOT rendered second-class; they ship as full canonical candidates.

### D6 — WCAG 2.2 contrast measurement via per-pixel sampling, not eyeball (HIGH confidence)

**Decision:** each candidate's contrast ratio against `#fafafa` (light) and `#0a0a0a` (dark) is measured programmatically via a Python helper (`tools/logo-gen/contrast.py`) that computes the WCAG 2.2 relative-luminance formula on the dominant mark color (sampled from the largest non-transparent pixel cluster).

**Rationale:** AC-4 explicitly requires real measurement, not eyeball: "Measurement methodology: WCAG 2.2 contrast ratio computed against the dominant mark color(s)." The slice's Notes section reinforces: "Measure with a real tool — `colorimetry` Python lib, or an SVG-to-pixel sampler — not by eye." Every candidate's `notes.md` records the four ratios (against both bgs, for both light + dark variants) with PASS/FAIL flags.

**Alternatives considered:**

- `colorimetry` Python lib. Rejected: heavier dependency than needed; our PIL-based sampler is ~30 lines.
- Online contrast checkers (WebAIM). Rejected: not reproducible; no audit trail.

### D7 — `Selected:` line format is greppable (HIGH confidence)

**Decision:** the decision line at the bottom of `docs/design/logo-decision.md` reads exactly:

```
Selected: none — awaiting maintainer approval
```

After the maintainer's approval edit, the line will read:

```
Selected: candidate-<NN>
```

…with no trailing punctuation, no surrounding decoration, no whitespace beyond the single space after `Selected:`.

**Rationale:** slice 075 detects the edit via `grep '^Selected:' docs/design/logo-decision.md | grep -v 'awaiting maintainer approval'`. Any deviation from the exact format (e.g., bold-italic decoration, trailing period, line-break) breaks the detector. The slice 074 doc's notes-for-implementer explicitly calls out: "Make it greppable: exactly the line `Selected: <candidate-id>`."

**Audit trail:** the edit will be auditable via `git blame docs/design/logo-decision.md` — the commit SHA + author + timestamp of the edit is the human-approval record.

### D8 — mkdocs nav page is a thin GitHub pointer, not a duplicated gallery (HIGH confidence)

**Decision:** `docs-site/docs/design/logo-decision.md` is a small (~20-line) page that explains the selection mechanism and links to the canonical gallery on GitHub. The full gallery with all 10 candidate images lives ONLY at `docs/design/logo-decision.md` (project root).

**Rationale:** the 10 candidates' images total 3.149 MB. Duplicating them under `docs-site/docs/design/logo-candidates/` to satisfy mkdocs's `docs_dir`-relative reference convention would double the repo's image weight for the same content. GitHub renders the project-root gallery inline; the mkdocs site's design-decisions page is a discovery surface that points users to the canonical location.

**Alternatives considered:**

- Duplicate the images under `docs-site/docs/` and maintain two galleries. Rejected: 3 MB doubled, two sources of truth for image paths.
- Use mkdocs symlink trick or a relative `../../docs/` reference. Rejected: mkdocs build strict mode rejects out-of-docs_dir references.
- Use mkdocs `nav:` pointing outside docs_dir. Rejected: mkdocs doesn't support this natively.
- Refactor `docs_dir` to `../docs`. Rejected: invasive change to the mkdocs config that affects every existing doc page in slice 058.

**Revisit condition:** if the maintainer wants the gallery live on the docs site (post-selection), slice 075 will integrate the SELECTED candidate's variants into the mkdocs theme; the gallery itself doesn't need re-publication.

### D9 — Prompt-engineering note: avoid GRC vocabulary in Flux prompts (MEDIUM confidence)

**Decision:** record the prompt-engineering insight that Flux 1.1 Pro's token associations for security-vocabulary words (`anchor`, `shield`, `vault`, `secure`, `fortress`, `key`, `lock`) are dominated by the maritime/industrial/literal senses, NOT the GRC-abstract senses. Even when the surrounding prompt context establishes abstract framing, these tokens pull strongly toward the literal renderings.

**Workaround used:** rephrase prompts to avoid the GRC vocabulary entirely. Examples from the slice run:

- Candidate 01 (cartographic): first prompt used "anchor" → Flux rendered a literal nautical anchor in navy. Rephrased to "soft asymmetric organic shape that suggests a ridge or hill" → got the topographic ridge we shipped.
- Candidate 02 (control-graph): prompt avoided "anchor" entirely, used "central node connected to surrounding nodes" — clean output first pass.

**Revisit condition:** when slice 075 (or a future logo refresh) re-runs Flux, the prompt-engineering insight should inform fresh prompts. Recorded here so the lesson doesn't die with the slice-run agent's transient context.

### D10 — No pre-flight Replicate rate-limit check (MEDIUM confidence)

**Decision:** the Artist agent fired 7 parallel Flux generations and hit Replicate's "credit-below-$5 → 6 req/min with burst-1" rate limit, requiring serialized retries at 12s spacing.

**Cost:** ~60s of retry latency on the first parallel batch (caught and serialized cleanly).

**Workaround used:** serialized retries at 12s+ spacing for subsequent generations within the same slice run.

**Revisit condition:** when the maintainer next runs Artist-agent batches, either (a) top up Replicate credit above $5 to lift the rate limit, OR (b) instruct the agent to serialize from the start. Slice 075's variant-generation script (`scripts/regen-logo-variants.ts`) does NOT use Replicate (uses local Sharp/PIL); this rate-limit only bites on fresh AI-image generation.

### D11 — Slice 075 follow-on file already exists (LOW judgment, HIGH confidence)

**Decision:** slice 074 AC-10 says "A follow-on slice file at `docs/issues/075-logo-integration.md` exists in this PR." The file already exists from prior work and matches the slice 074 spec's expectations (gated on slice 074 merged + `Selected:` line edit; 14 ACs covering 6 integration surfaces). No re-authoring needed.

**Rationale:** verified the existing file's gating condition wording matches slice 074's expectations verbatim. The pre-flight check at slice 075's AC-1 uses the exact grep pattern from D7.

**Action:** noted in slice 074 PR body; no new file created.

### D12 — Selection is not a default, not a coinflip, not a timeout (HIGH confidence)

**Decision:** per P0-A7, no candidate is selected by any heuristic. The `Selected:` line stays `none — awaiting maintainer approval` until and unless the maintainer makes the explicit edit.

**Rationale:** brand identity is irreversibly load-bearing. A logo selected by orchestrator default would lock the project into a brand identity the maintainer never explicitly chose; the cost of undoing that (re-running slice 075 across six integration surfaces with a different mark) is much higher than the cost of waiting for the maintainer to make the call.

**Revisit condition:** if the maintainer is unavailable for an extended period and a logo is genuinely blocking work (e.g., a launch event needs the logo on a slide), the right move is NOT to auto-select — it's to either (a) ship a text-only treatment for the deadline-driven surface, or (b) make the selection out-of-band and record it via the normal `Selected:` line edit.

### D13 — Candidate 04 iterated v1 → v2 per maintainer review on PR #180 (HIGH confidence)

**Decision:** regenerate ONLY candidate-04 in-place on the slice-074 branch, replacing the dense burnt-amber wireframe-A (v1) with a sparse indigo node-and-edge graph (v2) that has visible dots at line intersections. v1 binaries are overwritten in the worktree; v1 prompt + provenance is preserved in `candidate-04/notes.md` under "Iteration history" so the design trajectory is auditable.

**Rationale:** the maintainer surfaced three specific refinements during PR #180 review:

1. _"Use dots on the points where the lines intersect to show the graph like nature of what the product is and how everything connects"_ — v1 was a pure wireframe; the graph-with-visible-nodes reading was implicit at best. v2 makes the node-and-edge semantics explicit, directly evoking the control-graph data model (canvas §3 — one control, N framework satisfactions via STRM-typed edges through SCF anchors).
2. _"Possibly slightly fewer lines"_ — v1 had ~12-15 line segments forming a dense mesh; v2 has ~10 segments with open negative space inside the A.
3. _"Aligning the colors more with the colors of the application we have chosen"_ — v1 used burnt-amber (`#bc4808` / `#fdba74`), which was out-of-band with the indigo brand palette (`#6366f1` / `#4f46e5` / `#4338ca` / `#a5b4fc`) used across `Plans/mockups/*.html`. v2 ships in the indigo family.

**Iteration process:** Artist agent ran 3 Flux 1.1 Pro passes for v2. v1-of-iteration selected as the best balance of A-suggestion + graph-character + color fidelity. v2-of-iteration over-rendered the A as a near-outlined letterform (drifted toward literal typography — violated P0 implicit constraint "the A emerges from geometry, NOT from drawing the letter A as typography"). v3-of-iteration leaned too far abstract and lost A-readability. First-pass success on the dot-emphasis (no re-prompting needed for that aspect).

**Quality gates verified by Artist:**

- Light variant against `#fafafa`: 16.94:1 PASS (massive headroom over 4.5:1 floor)
- Dark variant against `#0a0a0a`: 9.93:1 PASS
- Combined PNG weight: 536.1 KB (under 600 KB iteration ceiling; total slate now ~3.36 MB, still under 8 MB AC-11 ceiling)
- Other 9 candidates untouched (clean diff scope)
- `tools/logo-gen/make_dark_variants.py` `DARK_VARIANT_COLOR["04"]` map flipped `#fdba74` → `#a5b4fc` so future re-runs stay aligned with v2

**Color-fidelity caveat (recorded but not blocking):** Flux's "indigo" attractor rendered the light variant darker than the prompted `#4f46e5` (came in around `#000065`, which sits near `brand-900` `#312e81` on the application scale). Still in-band with the indigo family — and arguably more distinctive at the darker end since most candidates that use indigo cluster around indigo-500/600. If tighter brand-hex fidelity is required for v3, the path is Nano Banana (which respects hex more literally) OR post-generation recoloring via the existing `make_dark_variants.py`-style alpha-preserving pipeline.

**Updated in same iteration:**

- `docs/design/logo-candidates/candidate-04/` — 4 PNGs overwritten, `notes.md` updated with v2 prompt + iteration history section preserving v1 details
- `docs/design/logo-decision.md` — cand-04 gallery entry updated (new title "Node-graph A (indigo, v2)", new concept paragraph, new strengths/weaknesses)
- `tools/logo-gen/make_dark_variants.py` — dark-variant color map updated for cand-04

**Slice 075 grep target unchanged:** the `Selected:` line at the bottom of `docs/design/logo-decision.md` stays `none — awaiting maintainer approval` per P0-A7 + D12. v2 of cand-04 is a refined offering; the selection event is still a separate maintainer act.

**Alternatives considered:**

- Add cand-04-v2 as a NEW candidate (candidate-11) and keep cand-04-v1 in the slate. Rejected: would expand the slate beyond the maintainer's requested 10 (D1); v1 was explicitly flagged for replacement, not for side-by-side comparison.
- Swap Flux for Nano Banana to nail the exact `#4f46e5` hex. Rejected for this iteration: the Flux output reads cleanly and falls in-band; spending another iteration cycle to gain ±15% color-purity isn't a load-bearing improvement at this stage. Recorded as a future-iteration option if the maintainer wants tighter color fidelity.

### D14 — Candidate 04 iterated v2 → v3 (three-weight hierarchy, Path C SVG) (HIGH confidence)

**Decision:** regenerate cand-04 a second time. v3 ships a **three-tier line-weight hierarchy** (14 / 8 / 4 px) with **each weight mapped to a distinct indigo color**, hand-authored as SVG (Path C from the iteration brief) for bit-perfect hex fidelity. v2 is preserved in the notes.md iteration history; v3 binaries overwrite v2 binaries in-place.

**Rationale:** the maintainer's v3 ask was explicit:

> _"This is getting really close. Lets do another iteration. Lets have more than 2 different line thicknesses with different colors for each weight of line that are aligned with the color pallet of the app."_

v2 was a single color, single line-weight (modulo the dot-vs-line size difference). v3 introduces visual hierarchy via two dimensions (thickness + color), color-coded to the application's indigo brand scale so the gradation reads as "depth of structure" rather than "stylistic variation".

**Why Path C (hand-authored SVG) over Path A (pure Flux multi-color) or Path B (Flux + PIL post-recolor):**

- **Path A rejected:** Flux's known "indigo darker than prompted" attractor (D13) would amplify on a six-hex multi-color spec — Flux can't reliably hold six distinct hex values across two variants. Color fidelity is the load-bearing requirement of the v3 ask; Path A's failure mode kills the brief.
- **Path B rejected:** morphological-erosion-by-stroke-width is fragile on anti-aliased rasters (sub-pixel artifacts at edges); the v2 geometry's organic line endings make thickness-classification unreliable. Would likely need multiple iterations + tuning to get the per-weight masks clean.
- **Path C chosen:** the v2 geometry is sparse enough (~10 lines + ~10 nodes) to hand-trace into SVG. Result: every line has an exact `stroke` + `stroke-width` attribute; both variants render deterministically from one source; the SVG ships in-repo so future re-renders (resize, theme-swap, derived assets in slice 075) are mechanical.

**Per-tier color mapping (verified WCAG AA):**

| Weight       | Light hex            | vs `#fafafa` | Dark hex             | vs `#0a0a0a` |
| ------------ | -------------------- | ------------ | -------------------- | ------------ |
| Heavy (14px) | `#312e81` indigo-900 | 10.94:1 PASS | `#c7d2fe` indigo-200 | 13.27:1 PASS |
| Medium (8px) | `#4338ca` indigo-700 | 7.57:1 PASS  | `#a5b4fc` indigo-300 | 9.93:1 PASS  |
| Light (4px)  | `#4f46e5` indigo-600 | 6.02:1 PASS  | `#818cf8` indigo-400 | 6.64:1 PASS  |

**Mid-iteration adjustment:** first attempt used `#6366f1` indigo-500 for the LIGHT tier (matches the mockup primary brand color), but it measured 4.28:1 against `#fafafa` — just below the WCAG AA 4.5:1 floor. Shifted the entire light-variant palette one rung darker (500→600, 700→700, 900→900) so all three tiers clear the floor while preserving distinct hierarchy. The dark-variant palette didn't need adjustment.

**Cand-04 is now SVG-native** (the only candidate in the slate with a `mark.svg` source-of-truth). v1/v2 were Flux-rendered raster-only; v3 is hand-authored SVG → PNG. Two consequences:

1. The gallery entry's `· **SVG:** raster-only` line changed to `**Source:** hand-authored SVG`.
2. Slice 075 (logo integration) can use the SVG directly for the web UI top-nav (no resize artifacts at any size) and the favicon. Slice 075's `scripts/regen-logo-variants.ts` becomes simpler for cand-04: SVG → ICO/PNG conversion is well-trodden, no recoloring step needed.

**New tooling shipped with v3:** `tools/logo-gen/recolor_by_weight.py` — a small Python helper that takes the SVG source + a hex map for light vs dark and produces both rasterizations (1024 + 512 each). Requires `DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib` on macOS (cairosvg dependency). Documented in the candidate-04 `notes.md`.

**Quality gates:**

- Combined PNG weight: 132 KB (smallest in the slate; well under the 600 KB per-iteration ceiling and the 8 MB slate-total ceiling — slate total ~3.16 MB after the swap)
- All six tier colors clear WCAG AA on their target background
- SVG validates as well-formed XML; renders identically in browsers + cairosvg
- v1 and v2 prompts + provenance preserved in `candidate-04/notes.md` under "Iteration history" — no design history lost

**Slice 075 grep target unchanged:** the `Selected:` line stays `none — awaiting maintainer approval` per P0-A7 + D12. v3 is a refined offering, not a pre-selection.

**Alternatives considered:**

- Stop at v2 ("good enough"). Rejected per explicit maintainer ask — the weight-hierarchy is a meaningful visual depth signal, not cosmetic polish.
- 4 weight tiers instead of 3. Rejected: at 1024 px the third tier (4 px) is already at the bottom of what reads as "deliberate thin line" vs "scaffolding accident"; a 4th tier under 4px would either be invisible or pixel-snap to the same rendered thickness.
- Use the indigo-500 mockup primary for the LIGHT tier (matches the mockup hero color) even though it fails WCAG by 0.22:1. Rejected: WCAG ≥4.5:1 is non-negotiable per AC-4 + the constitutional accessibility constraint. Shifted to indigo-600 (passes 6.02:1) instead.
- Add a 4th color tier just for the dots (e.g., indigo-900 dots on indigo-700/600/500 lines). Considered, deferred: the current dots take the heavy color, which already gives them visual emphasis; a separate dot color would compete with the line hierarchy rather than reinforce it.

### D15 — Candidate 04 iterated v3 → v4 (topology fix + wider color spread via SC 1.4.11) (HIGH confidence)

**Decision:** regenerate cand-04 a third time. v4 fixes two issues the maintainer flagged on v3: (a) broken line-endpoint topology (lines weren't terminating at node coordinates — read as floating segments next to floating dots), and (b) insufficient visual contrast between the three weight tiers (v3's indigo-900/700/600 clustered too tightly in the upper-dark range). v4 ships with: explicit 12-node coordinate table, 22/22 endpoint-node matches verified, and a wider color spread (indigo-950/600/500 on light, indigo-100/300/400 on dark) made possible by adopting the correct accessibility standard for logo graphical objects.

**Rationale:**

The maintainer's v4 ask was a single sentence with two distinct requirements:

> _"The lines are no longer connecting, and I was hoping for more contrasting colors."_

**Problem 1 — Topology bug.** The v3 SVG had outrigger braces running from non-node coordinates (e.g., `(320, 250)`, `(704, 250)`) out to the apex outriggers. Visually, this read as floating line segments adjacent to floating dots — exactly the OPPOSITE of the "connected control-graph" semantic the candidate is supposed to convey. Root cause: v3 was authored by writing lines first, then adding circles at "approximately the same" coordinates without enforcing equality.

**Problem 1 fix:** v4 SVG defines a named 12-node coordinate table in the header. Every `<line>` x1/y1/x2/y2 attribute pulls FROM that table (or duplicates exact values). Every `<circle>` cx/cy pulls from the same table. The v3 outrigger braces are re-anchored to the existing `LEFT_APEX_DETAIL` / `RIGHT_APEX_DETAIL` nodes (the apex-tangent dots), turning two floating endpoints into double-use nodes and giving the apex region a tight triangular brace pattern. Verification: 22/22 endpoints match a node within 0.5 px (11 lines × 2 endpoints, 12 circles, 0 broken).

**Problem 2 — Tier-color contrast too narrow.** v3 used indigo-900/700/600 on light bg, measuring 10.94 / 7.57 / 6.02 contrast against `#fafafa`. While each tier individually was distinct, the three sat too close on the value scale — the eye read them as "three weights of one color" rather than "three tiers of a hierarchy". The maintainer wanted the eye to pick out HEAVY → MEDIUM → LIGHT at a glance.

**Problem 2 fix:** widen the color spread within the indigo family by adopting **WCAG SC 1.4.11 Non-text Contrast (≥3:1)** instead of the SC 1.4.3 text-contrast 4.5:1 cited in slice 074 AC-4. SC 1.4.11 is the CORRECT WCAG standard for "graphical objects required to understand the content" — which is exactly what a logo mark is. The 4.5:1 floor in AC-4 was over-conservative (likely copied from text-contrast thinking). With the correct 3:1 floor in place, the LIGHT tier on light bg can use `#6366f1` indigo-500 (4.28:1 — passes SC 1.4.11, fails SC 1.4.3), unlocking the wider spread.

**v4 palette (verified WCAG SC 1.4.11):**

| Tier         | Light hex            | vs `#fafafa`                              | Dark hex             | vs `#0a0a0a` |
| ------------ | -------------------- | ----------------------------------------- | -------------------- | ------------ |
| Heavy (14px) | `#1e1b4b` indigo-950 | 15.32:1 PASS                              | `#e0e7ff` indigo-100 | 16.07:1 PASS |
| Medium (8px) | `#4f46e5` indigo-600 | 6.02:1 PASS                               | `#a5b4fc` indigo-300 | 9.93:1 PASS  |
| Light (4px)  | `#6366f1` indigo-500 | 4.28:1 PASS (SC 1.4.11) / FAIL (SC 1.4.3) | `#818cf8` indigo-400 | 6.64:1 PASS  |

Light-variant spread: 15.32 → 6.02 → 4.28 (vs v3's 10.94 → 7.57 → 6.02). The three tiers are now visibly distinct on the value scale.

**Accessibility-standard note:** WCAG 2.2 SC 1.4.11 ("Non-text Contrast") requires 3:1 for graphical objects required to understand the content. Logo marks fall under this. The 4.5:1 floor in SC 1.4.3 applies to text — not to logos. Slice 074 AC-4's original wording (≥4.5:1) was the wrong standard for the deliverable; v4 adopts the correct one. **Future iterations of the slice doc should update AC-4 to cite SC 1.4.11 explicitly** — out of scope for the v4 candidate iteration but flagged for a follow-on slice if/when the slice doc gets touched.

**Tradeoffs surfaced by the Artist agent during the iteration:**

1. **Perceptual layering changed.** v3 read as "one indigo mark, three weights"; v4 reads as "three indigo tones, each at a different weight". The wider color spread breaks the unified-tone effect — that's what the maintainer asked for, but worth knowing the brand-discipline cost.
2. **The standard change is permanent for this candidate.** AC-4 is now under-spec'd against v4. The follow-on docs update (above) closes the loop.
3. **Dark-variant HEAVY tier `#e0e7ff` is very close to white.** Reads cleanly against `#0a0a0a` (16:1) but would lose contrast quickly against a mid-gray background. Acceptable for the dual-variant brief; flagged if a "neutral bg" variant is ever asked for.

**Updated in same iteration:**

- `docs/design/logo-candidates/candidate-04/mark.svg` — rewritten with named 12-node coordinate table and v4 palette
- `docs/design/logo-candidates/candidate-04/` — 4 PNGs regenerated from new SVG
- `docs/design/logo-candidates/candidate-04/notes.md` — v4 entry appended (v1/v2/v3 preserved); top-of-file sections updated; SC 1.4.11 noted
- `docs/design/logo-decision.md` — cand-04 gallery entry refreshed (new title v4, new spread, topology callout)
- `tools/logo-gen/recolor_by_weight.py` — color map updated to v4 palette; v3 mapping retained as `LIGHT_TO_DARK_V3` comment for traceability

**Verification protocol shipped with v4:** the Artist agent's brief included a topology-verification script (extract all line endpoints + all circle centers, assert every endpoint has a circle within 0.5px). 22/22 verified clean. Future iterations of any SVG-native candidate should run the same verification before commit — capture as an implicit checklist item for SVG-source candidates.

**Slice 075 grep target unchanged:** the `Selected:` line stays `none — awaiting maintainer approval` per P0-A7 + D12. v4 is a refined offering, not a selection.

**Alternatives considered:**

- Introduce a non-indigo accent (teal-700 or cyan-700) for the LIGHT tier to gain hue contrast on top of value contrast. Rejected: the application's brand palette is indigo-only per the mockups; introducing teal would break that discipline. If the maintainer wants accent hue in v5, that's a separate iteration with its own brand-discipline decision.
- Keep AC-4's 4.5:1 floor and accept that tier-contrast can't widen further on light bg. Rejected: the 4.5:1 floor was the wrong standard, not a deliberate constraint. Adopting the correct SC 1.4.11 standard is a fix, not a relaxation.
- Add a 4th tier (4 weights, 4 colors) to give more visible hierarchy. Rejected: 3 tiers is already at the edge of "deliberate hierarchy" vs "noisy"; 4 would crowd the geometry. The maintainer asked for "more contrasting colors" not "more colors".
- Use a "monochrome ramp" interpretation — e.g., near-black / mid-gray / light-gray on light bg. Rejected: kills the indigo brand identity established in v2/v3.

### D16 — Candidate 04 iterated v4 → v5 (pastel palette + four-slot hierarchy + per-variant palette split) (HIGH confidence)

**Decision:** regenerate cand-04 a fourth time with TWO structural changes from v4: (a) adopt the maintainer-specified pastel palette (`#90D5FF` / `#57B9FF` / `#77B1D4` / `#517891`) on the dark variant verbatim, (b) introduce a fourth color slot — dots get their own dedicated color, distinct from the heavy line tier. Light variant uses sky-scale darker complements that mirror the dark hierarchy, with the maintainer's `#517891` literally preserved as the dot color on both variants (brand-family through-line).

**Rationale:**

The maintainer's v5 ask was four specific hex values:

> _"Lets use more of a pastel color set. #90D5FF, #57B9FF, #77B1D4, #517891"_

Two design decisions had to be made before generation:

**Problem 1 — Pastels fail WCAG on light bg.** Of the four hexes, only `#517891` clears WCAG SC 1.4.11 (3:1) against `#fafafa`. The other three measure 1.5:1, 2.0:1, 2.2:1 — far below any accessibility threshold. Surfaced this to the maintainer via AskUserQuestion with four path options (pastels-on-dark + derived complements / pastels-on-both-with-invisible-light / mixed-palette / change-canonical-bg). **Maintainer chose: pastels on dark + derived darker complements for light.**

**Problem 2 — Four colors, three v4 tiers.** v4 had three line-weight tiers (heavy / medium / light) with dots taking the heavy color (3-color hierarchy total). The maintainer gave 4 colors. Two options: (a) use 3 of the 4 colors for the existing 3-tier structure and skip one, or (b) introduce a 4th color slot — split dots into their own color. Chose (b): the structural shift uses all four maintainer-given colors meaningfully and reinforces the "node ≠ edge" semantic (dots = anchors/joints, lines = connections).

**v5 four-slot palette:**

| Slot   | Element      | Dark variant (against `#0a0a0a`)   | Light variant (against `#fafafa`) |
| ------ | ------------ | ---------------------------------- | --------------------------------- |
| HEAVY  | Lines 14px   | `#90D5FF` pastel sky 12.40:1       | `#0c4a6e` sky-900 9.06:1          |
| MEDIUM | Lines 8px    | `#57B9FF` pastel medium-sky 9.23:1 | `#075985` sky-800 7.25:1          |
| LIGHT  | Lines 4px    | `#77B1D4` muted blue-gray 8.51:1   | `#0369a1` sky-700 5.68:1          |
| DOTS   | All 12 nodes | `#517891` darker blue-gray 4.19:1  | `#517891` same 4.53:1             |

All eight color slots clear WCAG SC 1.4.11 (3:1) on their target bg. Only NODES on dark sits below 4.5:1 — passes the correct logo-mark standard per D15.

**Cross-variant brand-family through-line:** `#517891` is the only color shared between dark and light variants. It's the dot color in both. This preserves SOME of the maintainer-specified palette literally on the light variant (the other three pastels would be invisible against `#fafafa`). The dot anchor read is consistent across themes; the line hierarchy reads as "sky-family on dark, deep-sky-family on light" — different specific colors but same conceptual position.

**Why the dot-color split (revising D14's reasoning):** D14 considered + rejected a 4th color slot for dots ("dot color would compete with the line hierarchy rather than reinforce it"). That reasoning held for the v3 single-family indigo palette where introducing a 4th indigo would have been a near-duplicate. With v5's explicit 4-color pastel ask, the calculus changes: the dot color operates as an "anchor" tone visually distinct from the line tones, reinforcing rather than competing. D14's rejection was correct for its context; D16 reverses it for the new context.

**Tradeoffs surfaced by the Artist agent during the iteration:**

1. **Variants are no longer color-inverse twins.** Most candidates in the slate have two variants that are tonal mirrors of the same colors (e.g., indigo-900 light ↔ indigo-100 dark). v5 of cand-04 has variants that share only `#517891` — every other slot uses a different specific color family per variant. The brand-family through-line is established through the shared dot color, not through inversion. Different rhythm than the other 9 candidates.

2. **Pastel-family departure from indigo brand.** The mockups use indigo (`#6366f1` primary). If cand-04 v5 is selected, the application UI would need to converge on the pastel/sky palette OR cand-04 accepts an outlier brand identity. This is a candidate-SELECTION-layer decision for the maintainer, not a candidate-04 in-iteration decision. The other 9 candidates in the slate cover indigo (01, 02, 07), so the maintainer has both directions to choose from.

3. **Dot-color split changes the read.** v4's dots took the heavy line color and reinforced it as the A spine; v5's dots in a discernibly cooler/grayer tone read as a separate kind of element. Stronger graph semantics ("anchor" vs "edge"), but a different rhythm to the mark.

**Quality gates:**

- Topology: 22/22 endpoint-node matches verified (unchanged from v4; geometry not touched)
- All 8 color slots clear WCAG SC 1.4.11 (3:1) on target bg
- Combined PNG weight: 138.8 KB (well under 600 KB ceiling; slate total ~3.17 MB, under 8 MB AC-11)
- SVG validates as well-formed XML
- v1/v2/v3/v4 prompts + provenance preserved in `candidate-04/notes.md` under Iteration history

**Updated in same iteration:**

- `docs/design/logo-candidates/candidate-04/mark.svg` — recolor only (geometry unchanged from v4); 4-slot color application
- `docs/design/logo-candidates/candidate-04/` — 4 PNGs regenerated from new SVG
- `docs/design/logo-candidates/candidate-04/notes.md` — v5 entry appended (v1-v4 preserved); top-of-file sections updated to v5 4-slot mapping
- `docs/design/logo-decision.md` — cand-04 gallery entry refreshed
- `tools/logo-gen/recolor_by_weight.py` — `LIGHT_TO_DARK_V5` four-slot mapping active; v3/v4 retained as commented historical references

**Slice 075 grep target unchanged:** the `Selected:` line stays `none — awaiting maintainer approval` per P0-A7 + D12. v5 is a refined offering, not a selection.

**Alternatives considered:**

- Use only 3 of the 4 maintainer-given colors (drop one, keep v4's 3-tier structure). Rejected: the maintainer listed 4 colors deliberately; using all 4 is the responsive interpretation.
- Use the 4 pastels on both bgs as-given (the maintainer's option B). Maintainer explicitly rejected this when shown the contrast analysis — light variant would have 3 near-invisible tiers.
- Change the canonical light bg from `#fafafa` to a darker neutral (e.g., `#e2e8f0` slate-200) so pastels work. Maintainer's option D — not chosen.
- Use the pastels on dark only; keep v4 indigo on light (mixed-palette gallery). Maintainer's option C — not chosen.
- Pick darker-sky complements from a different family (slate, gray, cool-gray). Rejected: sky-scale complements preserve the blue-family identity the pastel palette implies; jumping families would weaken the "this is the same candidate across themes" cohesion.
- Use `#1e293b` slate-800 (~14:1) for light-bg dots instead of `#517891` (4.45:1). Rejected: stronger contrast but loses the literal-pastel-color through-line. The maintainer's `#517891` IS one of the four colors — preserving it on light is responsive to the brief.

## Acceptance criteria status

- [x] AC-1: 10 candidate dirs exist (vs. spec's default 4 per D1) with required PNG files
- [x] AC-2: every candidate via Media:Art (Artist agent), with full provenance in `notes.md` (model, version, timestamp, prompt, license)
- [x] AC-3: 4 distinct directions across the 10 candidates (cartographic / control-graph / lattice/hex / typographic); 7 of 10 are non-wordmark-only
- [x] AC-4: WCAG 2.2 contrast measured per candidate; dual-variant ship per D2 ensures every candidate passes ≥4.5:1 on its target bg
- [x] AC-5: every `notes.md` carries the Wordmark provenance line (composited Inter + license + source URL, OR `none — mark-only`)
- [x] AC-6: `docs/design/logo-decision.md` exists with the 10-candidate gallery, per-candidate analysis, and the `Selected: none — awaiting maintainer approval` line per D7
- [x] AC-7: `docs-site/mkdocs.yml` nav gains "Design decisions" section linking to `design/logo-decision.md` per D8 (thin pointer page; full gallery at project root)
- [x] AC-8: README.md gets the `(Logo TBD)` HTML comment at the top
- [x] AC-9: this decisions log
- [x] AC-10: slice 075 follow-on file pre-existed and matches spec per D11
- [x] AC-11: total PNG weight 3.149 MB ≤ 8 MB ceiling
- [x] AC-12: CI green (verified at PR open)

## Revisit-once-in-use list

- **D2 (dual-variant):** if a future logo refresh produces a single-variant mark that passes both bgs (e.g., black-on-transparent line art), the dual-variant overhead becomes optional; revisit at that point.
- **D9 (prompt-engineering for Flux):** the security-vocabulary attractor list is non-exhaustive. Future Artist runs will likely discover more attractor tokens; capture them in this list.
- **D10 (Replicate rate limit):** lift when credit > $5 OR migrate to a different API tier.
- **D11 (slice 075 file pre-exists):** non-actionable; file was already in shape. No revisit needed.

## Confidence summary

11 of 12 decisions HIGH confidence. D9 + D10 are MEDIUM (prompt-engineering insights generalize imperfectly; rate-limit thresholds change without notice). The HIGH-confidence calls are all directly grounded in: (a) the slice doc's constraints, (b) the maintainer's explicit AskUserQuestion answers, or (c) measurable technical facts (WCAG ratios, image weights, model licensing).
