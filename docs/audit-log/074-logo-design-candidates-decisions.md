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
