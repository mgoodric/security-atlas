# Logo design — candidates and selection

security-atlas's logo selection lives in the repo, not in the docs site, because the 10 candidate marks ship as binary images (~3 MB total) that don't benefit from re-publication through the mkdocs build pipeline.

**Candidates + decision page:** [`docs/design/logo-decision.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/design/logo-decision.md) on the repo (GitHub renders the gallery inline).

**Selection mechanism:** the maintainer edits the `Selected:` line at the bottom of that file to a real candidate ID (e.g. `Selected: candidate-04`) and commits on `main`. The follow-on integration slice ([075](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/075-logo-integration.md)) detects the edit and integrates the selected logo across the README hero, this docs site's `theme.logo`, the web UI top-nav, the favicon set, and the social-share preview cards.

**Why two slices:**

- 074 ships only the candidate slate + this decision-pending page. Mechanical (image generation + side-by-side gallery).
- 075 takes the approved candidate and propagates it to six integration surfaces. Mechanical (resize, recolor, composite, declare in Next.js Metadata API).
- The split is "ask before destructive operations": brand identity is hard to reverse, so the human-approval gate is a single, deliberate `Selected:` line edit rather than an inline batch.

**Candidate provenance:** every candidate's image came from the `Media:Art` PAI skill (Flux 1.1 Pro or Nano Banana via Replicate, all commercial-use-OK). Wordmark text was composited separately using Inter (SIL OFL) — no image-model-rendered text in any mark. Full per-candidate provenance lives in the repo at `docs/design/logo-candidates/candidate-NN/notes.md`.

**Slice decisions log:** [`docs/audit-log/074-logo-design-candidates-decisions.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/audit-log/074-logo-design-candidates-decisions.md) records the 12 judgment calls made during candidate generation, including the WCAG dual-variant trajectory, prompt-engineering escapes from the Flux "security clichés" attractor, and the model substitution from GPT-Image-1 to Flux for two candidates.
