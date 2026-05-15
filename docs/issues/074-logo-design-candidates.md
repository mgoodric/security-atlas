# 074 — Logo design candidates (Media:Art, human approval pending)

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** JUDGMENT

## Narrative

security-atlas has no logo. The README hero, the docs site (slice 058), the web UI header, the social-share preview cards, and the favicon all currently fall back to text-only treatments. This slice produces a small slate of distinct logo candidates via the **Media:Art** PAI skill, lands the candidate files in a versioned location in the repo, and ships a single design-doc page that surfaces them side-by-side for the maintainer to choose from.

**Critical scope boundary:** this slice ships **only candidates and a decision-pending doc**. It does NOT integrate any logo into any production surface. Logo integration is a separate slice (075) that explicitly waits on this one to merge AND on the maintainer to approve one variant by editing a single line in the design-doc page. Two slices because (a) generating candidates is mechanical / pattern-matched, (b) choosing a logo is genuinely subjective and irreversibly load-bearing for brand identity, and (c) batching them would couple a low-risk generation slice to a high-judgment selection slice that needs out-of-band human review.

**Candidate constraints (load-bearing — the Artist agent must honor these):**

- **Concept anchor:** the platform's positioning is _"open-source, self-hostable, replacement-grade GRC platform — a control-graph and evidence-pipeline system that lets a security program run against many frameworks from one source of truth"_ (CLAUDE.md). The logo should evoke: control graph, evidence pipeline, atlas-as-map-of-controls, OR the spine-and-branches multidimensional-scope geometry from canvas §5. Generic security-padlock / shield / fortress imagery is explicitly out — the platform is a system of mapping and proof, not a vault.
- **Style range:** ship 4 candidates spanning at least three distinct directions. Suggested directions: (1) abstract geometric (e.g., interlocking control-anchor nodes), (2) typographic / wordmark-led, (3) iconographic with a single anchored symbol, (4) cartographic / atlas-evocative. The Artist agent picks the specific renders within each direction.
- **Format:** each candidate ships as (a) `1024×1024 PNG` source render, (b) `512×512 PNG` web-optimized, (c) `SVG` if the source render can cleanly vector-trace OR an SVG re-author at the same compositional shape — flagged in the doc which candidates have native SVG vs raster-only.
- **Color discipline:** dark-mode safe (≥4.5:1 contrast against `#0a0a0a`) AND light-mode safe (≥4.5:1 against `#fafafa`). The Artist agent renders each candidate twice (light + dark variant) or once with explicit "works on both" geometry. The mockups (`Plans/mockups/*.html`) and shadcn theme are the contrast reference — same accessibility constraint as slice 057's `<picture>` rendering.
- **No text-only "wordmark"** as the SOLE candidate. The candidate slate may include a wordmark variant, but at least 3 of the 4 candidates have a non-textual mark — the project's docs already lean text-heavy.
- **No AI-rendered text inside the mark.** The Artist agent is well-known for producing illegible / misspelled text inside generated images; the candidates' wordmark elements (if any) come from a separate text-typography compositing step using a chosen open-font (e.g., Inter, JetBrains Mono), NOT from the image model's text-rendering pass. This is enforced in the doc as a per-candidate annotation: "Wordmark: composited (font: Inter Bold)" or "Wordmark: none — mark-only".
- **Licensing:** every candidate must be produceable under the project's chosen license (Apache 2.0, slice 050). The Artist agent's source model must support commercial use; record the model name + version per candidate in the doc for licensing audit.

**Deliverables:**

1. `docs/design/logo-candidates/` directory with one subdirectory per candidate (`candidate-01/`, `candidate-02/`, ...). Each subdirectory holds `mark-1024.png`, `mark-512.png`, optionally `mark.svg`, plus a `notes.md` describing the candidate's concept, color treatment, contrast measurements, and AI provenance (model + version + prompt).
2. `docs/design/logo-decision.md` — the design-doc page. Side-by-side gallery of all candidates (4 `<img>` tags in a 2×2 grid using mkdocs Material's image-grid affordance OR plain markdown table — pick the one that renders cleanest in the mkdocs site), one paragraph per candidate explaining concept + tradeoffs, and a single decision line at the bottom:

   > **Selected:** `<candidate-id>` — _selection signed by maintainer on YYYY-MM-DD_

   …with the value initially set to `none — awaiting maintainer approval`. The follow-on slice (075) blocks until this line is edited by the maintainer in a separate commit on `main` (auditable via `git blame docs/design/logo-decision.md`).

3. The design-doc page is integrated into `docs-site/mkdocs.yml` under a new "Design decisions" nav section so the candidates render in the deployed docs site (sliced 058) alongside the other content.

## Acceptance criteria

- [ ] AC-1: `docs/design/logo-candidates/candidate-01/` through `candidate-04/` exist, each containing at minimum `mark-1024.png` and `mark-512.png`. Candidates with native SVG also have `mark.svg`.
- [ ] AC-2: Every candidate is generated via the **Media:Art** PAI skill (or, equivalently, the `Artist` agent type if the skill internally delegates). The candidate's `notes.md` records: prompt used (verbatim), model + version, generation timestamp, license-of-output statement.
- [ ] AC-3: At least 3 of the 4 candidates span 3 distinct directions per the narrative constraints (abstract-geometric / wordmark / iconographic-mark / cartographic). At least 3 of the 4 are non-wordmark-only.
- [ ] AC-4: Each candidate ships with a contrast measurement against `#0a0a0a` (dark) and `#fafafa` (light) backgrounds. Measurement methodology: WCAG 2.2 contrast ratio computed against the dominant mark color(s). Candidates that fail ≥4.5:1 in either mode are excluded from the slate (regenerate or replace).
- [ ] AC-5: Each candidate's `notes.md` carries an explicit "Wordmark provenance" line: either `"none — mark-only"` or `"composited (font: <font-name>, license: <font-license>, source: <SIL OFL link or URL>)"`. Image-model-rendered text is rejected at generate time per the narrative constraint.
- [ ] AC-6: `docs/design/logo-decision.md` exists with the gallery layout, one-paragraph-per-candidate analysis (concept / strengths / weaknesses / what audience it lands with), and the `Selected:` decision line set to `none — awaiting maintainer approval`.
- [ ] AC-7: `docs-site/mkdocs.yml` nav gains a "Design decisions" top-level section linking to `docs/design/logo-decision.md`. `mkdocs build --strict` (slice 058's AC-2 gate) passes. Mind the prettier-ignore convention for mkdocs admonitions (slice 058's surprise #1).
- [ ] AC-8: README.md gets a brief "(Logo TBD)" comment in a comment-only HTML block at the top so the absence of a hero logo is intentional and trackable. This is the SINGLE README change in this slice; no banner, no image references, no rendered placeholder.
- [ ] AC-9: A `docs/audit-log/074-logo-design-candidates-decisions.md` JUDGMENT-slice decisions log records: (1) the 4 directions chosen (and why the rejected directions were rejected), (2) the contrast methodology, (3) which open font(s) were used for any composited wordmarks and their license posture, (4) the model-version provenance per candidate, (5) the human-approval mechanism (edit the `Selected:` line on `main`, follow-on slice picks it up).
- [ ] AC-10: A follow-on slice file at `docs/issues/075-logo-integration.md` exists in this PR. Its status in `_STATUS.md` is **`not-ready`** with the explicit dep: `074 (this slice) merged + Selected line edited from "none — awaiting maintainer approval" on main`. The follow-on slice is the implementation of "take the approved one and put it everywhere."
- [ ] AC-11: Pre-commit clean (note: image files are not text; binary-content rules don't apply, but the YAML/MD updates do go through prettier). Total weight of all candidate PNGs ≤ 8 MB combined (same order of magnitude as slice 057's 5 MB ceiling; logos are typically smaller than screenshots so 8 MB is generous).
- [ ] AC-12: CI green. No CI gate is added by this slice. No `Frontend · install + build` regression (logo files live under `docs/`, not `web/`).

## Constitutional invariants honored

- **AI-assist boundary**: the candidates are AI-rendered IMAGES with explicit model provenance — this is the legitimate use of generative AI in the platform (visual design, not audit-binding artifact, not control evidence, not policy text). The hard "no audit-binding artifact published without one-click human approval" rule is honored: the SELECTION is one-click human approval (the `Selected:` line edit), and nothing is integrated anywhere until slice 075 lands behind it.
- **Working norms — Ask before destructive operations**: the two-slice split (074 generates, 075 integrates) IS the implementation of "ask before destructive operations" for brand identity. A logo decision is hard to reverse without re-doing every downstream surface.
- **Working norms — No emojis** (CLAUDE.md): the logo replaces the empty visual slot; it is NOT a decorative add-on competing with text. Same discipline.

## Canvas references

- `Plans/canvas/01-vision.md` — positioning + persona; the logo needs to land with the v1 persona (solo security leader at a 50-150-person startup)
- `Plans/canvas/05-scopes.md` — the multidimensional-scope geometry is one of the candidate-direction anchors (option 1 from the narrative)
- `Plans/mockups/*.html` — current visual-language reference (the candidates should sit naturally alongside the existing mockup styling)

## Dependencies

- **058** (user docs scaffold) — the design-doc page integrates into the mkdocs site
- **050** (public release readiness) — licensing posture (Apache 2.0 is the chosen license)

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT integrate any candidate into any production surface (README hero image, web UI header, favicon, social meta tag, docs-site logo, mkdocs `theme.logo`). All integration is the responsibility of slice 075, which is explicitly gated on this slice's `Selected:` line being edited.
- **P0-A2**: Does NOT use image-model-rendered text inside the mark. Wordmark elements come from a composited type pass using a license-clear font; never the image model's text-rendering output.
- **P0-A3**: Does NOT generate a candidate whose source model lacks commercial-use rights. The decision-doc page records each candidate's model + license; any candidate whose license is incompatible with Apache 2.0 redistribution is excluded.
- **P0-A4**: Does NOT use a single direction (e.g., "all four are typographic wordmarks"). The narrative's 3-direction minimum is to give the maintainer a genuinely diverse slate, not four-variations-of-the-same-thing.
- **P0-A5**: Does NOT include security-padlock, shield, fortress, vault, or generic-cybersecurity imagery. The platform is a system of mapping and proof; the visual language should evoke maps, graphs, anchors, lattices — not arms.
- **P0-A6**: Does NOT include any human face, person, body part, hand, or recognizable artistic style of a living artist. Standard generative-AI hygiene; the candidates are abstract / geometric / typographic / cartographic.
- **P0-A7**: Does NOT auto-merge or auto-promote a candidate by any heuristic. The selection is a single, deliberate human edit to the `Selected:` line on `main` — explicitly NOT a default, NOT a coinflip, NOT an "if no one picks within N days" fallback.

## Skill mix (3–5)

- **Media:Art** (`Artist` agent — the canonical generator for this slice's deliverables; the slice IS its showcase)
- `simplify` (the design-doc page's per-candidate paragraphs need to be tight — maintainer skims candidates, doesn't read essays)
- `security-review` (verify no PII, no maintainer reference, no recognizable real person, no license-incompatible upstream model — same shape as slice 057's fixture sanitization)
- `engineering-advanced-skills:codebase-onboarding` (the design-doc page IS a first-touch onboarding surface for "what does the project look like" — same writing discipline as slice 058's `index.md`)

## Notes for the implementing agent

- The Artist agent is well-known for producing illegible / wrong-letter text. **DO NOT rely on it for wordmarks.** If a candidate's design calls for text, composit the text in a separate step (PIL / Pillow, or an SVG `<text>` element with a real font path) AFTER the image-model render produces the mark glyph. AC-5's "Wordmark provenance" line is the audit trail.
- The 4-direction constraint is a floor, not a ceiling — if a fifth direction emerges naturally during generation that's genuinely distinct, include it (5 candidates total). Don't pad with near-duplicates to hit 4.
- The contrast measurement (AC-4) is non-negotiable. A logo that doesn't read against the dark-mode shadcn background fails the accessibility constraint that the rest of the UI honors. Measure with a real tool — `colorimetry` Python lib, or an SVG-to-pixel sampler — not by eye.
- The `Selected:` line at the bottom of `docs/design/logo-decision.md` is the linchpin. Make it greppable: exactly the line `Selected: <candidate-id>` (no trailing punctuation, no surrounding decoration) so slice 075 can detect the edit with a single `grep` and a single `git blame`.
- If the Artist agent surfaces a "this prompt doesn't generate a usable mark" failure mode for one of the 4 directions, RECORD the failure in the decisions log (with the prompt and the rejected output) and either (a) reformulate the prompt and retry, or (b) substitute a different direction. Do not ship 3 candidates with a "(direction four didn't work)" footnote — ship 4 honest candidates.
- The follow-on slice 075 is written as part of THIS slice's deliverables (AC-10). It is the spec for "after the maintainer signs off on a candidate, here's the integration work." Spend real time on it; the maintainer reviews it the moment they make the selection.
