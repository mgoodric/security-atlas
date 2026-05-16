# Logo design — selected candidate

security-atlas's logo selection lives in the repo, not in the docs site, because the source assets ship as binary images that don't benefit from re-publication through the mkdocs build pipeline.

**Selected:** candidate-04 — node-graph "A" with a warm→cool 8-color temperature gradient. Hand-authored SVG source-of-truth at [`docs/design/logo-candidates/candidate-04/mark.svg`](https://github.com/mgoodric/security-atlas/blob/main/docs/design/logo-candidates/candidate-04/mark.svg). Dual-variant raster outputs (light + dark, 1024 + 512 each) accompany it. View the gallery + per-candidate notes inline at [`docs/design/logo-decision.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/design/logo-decision.md).

**Integration:** slice 075 picks up the selected candidate and propagates it across the README hero, this docs site's `theme.logo`, the web UI top-nav, the favicon set, and the social-share preview cards. The integration starts as soon as slice 074 merges to `main`.

**Provenance:** the candidate originally came from the `Media:Art` PAI skill (initial direction via Flux 1.1 Pro). Subsequent iterations (v2-v6) refined the design as hand-authored SVG with deterministic rasterization — no image-model regeneration after the v1 direction landed. Full prompts + design calls + per-version contrast measurements live at `docs/design/logo-candidates/candidate-04/notes.md`.

**Slice decisions log:** [`docs/audit-log/074-logo-design-candidates-decisions.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/audit-log/074-logo-design-candidates-decisions.md) records the 18 judgment calls made across the slice — the original 10-candidate slate generation, the six rounds of candidate-04 iteration, the WCAG SC 1.4.11 standard adoption for logo marks, and the post-selection cleanup of unused candidate files.
