# 133 — mkdocs user docs content refresh (slice 058 follow-on)

**Cluster:** Docs
**Estimate:** 2-3d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` from the maintainer-driven full-docs cleanup. Slice 058 (merged) shipped the mkdocs Material scaffolding — sidebar structure, theme config, navigation skeleton — but the operator-facing content has not kept pace with platform evolution since then. The audit-log trio (124 + 125 + 126 + 129 + 130), the CI hardening trilogy (117 + 127 + 128), the admin/me role-parity work (slice 125 D5 + slice 130), the unified audit-log page, the external sink, the audit periods workflow, the risk hierarchy dashboard, the board-pack flow, the policy / exception / sample lifecycles — none of these have a "how to use it" page on the operator docs site.

This slice ships the content layer: the operator can land on `docs.security-atlas.dev` (or the published mkdocs URL) and find a complete walk through "I'm a solo security leader; how do I run my SOC 2 audit out of this?" The screenshot pipeline established in slice 132 is reused here for the docs site's inline images.

**What this slice ships:** a fully-populated mkdocs site under `docs/` with content for getting-started, the six primitive surfaces (control / risk / evidence / policy / scope / framework), the audit-log trio, the CI hardening posture, the self-host deployment guide (extends `SELF_HOSTING.md`), the connector authoring guide (extends slice 003's Evidence SDK doc), and the contributor docs.

**Scope discipline (what is OUT):**

- README refresh — slice 132 (parent; this slice depends on 132 for the screenshot capture pipeline).
- In-app walkthrough refresh — slice 134 (sibling spillover).
- New mkdocs theme work (color palette tweaks, custom CSS) — slice 058 owns theme; this slice owns content only.
- Translation / i18n — out of scope.

## Threat model

| STRIDE                       | Threat                                                                                                                                                                                  | Mitigation                                                                                                                                                                                                          |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a                                                                                                                                                                                     | n/a                                                                                                                                                                                                                 |
| **T** Tampering              | Same as slice 132 — screenshots may leak real data                                                                                                                                      | Reuse slice 132's capture pipeline + P0-A1 through P0-A4 verbatim                                                                                                                                                   |
| **R** Repudiation            | n/a                                                                                                                                                                                     | n/a                                                                                                                                                                                                                 |
| **I** Information disclosure | **HIGH** (same as slice 132, larger surface). Docs site has ~10-20 pages of screenshots; each is a leak vector. Also: copy-paste code samples in docs may include real-looking secrets. | Reuse slice 132 P0-A1 through P0-A4 for screenshots. Add: all code samples use neutral fixture tokens (`test-bearer-do-not-leak-1234567890abcdef` pattern); all example URLs are `localhost:8080` or `example.com`. |
| **D** DoS                    | Docs site image budget. mkdocs assets must remain CDN-friendly.                                                                                                                         | Per-image cap inherited from slice 132 (200 KB). Total docs-site image budget: ≤ 15 MB across all pages.                                                                                                            |
| **E** Elevation of privilege | n/a                                                                                                                                                                                     | n/a                                                                                                                                                                                                                 |

## Acceptance criteria (stub — to be expanded at pickup)

Initial AC sketch (engineer at pickup time fills in the detail):

- [ ] AC-1: getting-started landing page rewritten — covers what the platform does, who the operator is, the 5-minute "see it working" path.
- [ ] AC-2: per-primitive how-to pages (one each for control / risk / evidence / policy / scope / framework).
- [ ] AC-3: audit workflow walkthrough (SOC 2 sample — using the demo seed).
- [ ] AC-4: audit-log + external-sink operator guide (slices 124 + 126 + 129 + 130).
- [ ] AC-5: self-host deploy guide (extends `SELF_HOSTING.md`).
- [ ] AC-6: connector authoring guide (extends `Plans/EVIDENCE_SDK.md` for the operator audience).
- [ ] AC-7: CI hardening reference (slices 117 + 127 + 128 explained for operators).
- [ ] AC-8 through AC-N: per-page screenshots captured via slice 132's pipeline.
- [ ] Final AC: mkdocs build + link-check + image-budget check passes.

## Constitutional invariants honored

- **#9 Manual evidence is first-class.** Docs site treats manual + automated capabilities equally.
- **AI-assist boundary.** No AI-generated docs without human review; maintainer reviews PR.

## Canvas references

- `Plans/canvas/01-vision.md` — operator persona to write for.
- `Plans/canvas/11-open-questions.md` item 20 (RESOLVED 2026-05-14): mkdocs Material as the platform.

## Dependencies

- **#132** README refresh (this slice's parent) — establishes the screenshot capture pipeline this slice reuses. **Gate: 132 must be `merged` before 133 flips to `ready`.**
- **#058** User docs scaffold (merged) — sidebar / theme / nav structure.
- All audit-log trio + CI hardening trilogy slices (merged 2026-05-18) — content references these as the current platform state.

## Anti-criteria (P0 — block merge)

- **P0-A1 through P0-A4:** Inherit slice 132's screenshot anti-criteria verbatim.
- **P0-A5:** NO theme / CSS changes; slice 058 owns those.
- **P0-A6:** NO new mkdocs plugins; ship content only with the existing plugin set.
- **P0-A7:** NO real customer org names in any example, code sample, or screenshot.
- **P0-A8:** NO vendor-prefixed test fixture tokens in any code sample.

## Skill mix

- **`grill-with-docs`** — terminology + scope discipline at pickup.
- **mkdocs Material** — content authoring against the slice 058 scaffold.
- **Playwright** — screenshot capture via slice 132's pipeline.
- **`markdown-link-check`** — link validation.
- **`vale` or similar prose linter** (optional) — voice consistency across pages.

## Notes for the implementing agent

Slice 132 ships first; this slice reuses its capture pipeline. Do NOT re-author the pipeline.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 132 from the maintainer-driven full-docs cleanup. The maintainer's intent is a complete operator-facing reading flow ("how do I run my SOC 2 audit out of this?") — the AC matrix is a sketch; expand it at pickup time after re-reading the current state of `docs/`.
