# 167 — Logo redesign + replace existing assets across all usages

**Cluster:** Frontend (design)
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**WHY.** The current `security-atlas` logo (shipped via slice 153 `fix/153-logo-standalone` merged 2026-05-18, with prior design work in slices 074 + 075) doesn't render well at the sizes it's used. The maintainer's qualitative feedback (2026-05-19): "the logo does not show up well." Specific symptoms aren't enumerated yet — that's part of the JUDGMENT call this slice owns — but candidates include: insufficient contrast at small sizes (topbar at ~24-32px height), weak silhouette legibility, ambiguous shape vs the surrounding chrome, and/or the light-on-dark + dark-on-light pair not being properly mirrored.

**WHAT.** Engineer picks the redesign approach (the JUDGMENT call: D1 — generate-from-scratch vs refine-existing vs commission-external-then-adapt), produces ≥3 design candidates as SVG, picks one (or surfaces options to the maintainer), and replaces the four canonical assets (`web/public/logo-{dark,light}.{svg,png}`) plus any inline icon / favicon derivatives. Every existing usage (login page, topbar, root layout, e2e snapshot tests) re-renders with the new asset without code changes — slice 075's integration contract holds.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT change layout / positioning / sizing of logo containers. Replace the asset, not the surrounding code.
- Does NOT introduce a new logo component, mount point, or import path. The existing `web/components/shell/topbar.tsx` + `web/app/login/page.tsx` + `web/app/layout.tsx` references stay byte-identical at the call site.
- Does NOT add a third color theme or a system-preference-aware variant beyond the existing light/dark pair.
- Does NOT bundle a wordmark redesign in the same slice if the existing wordmark works. If wordmark also needs work, that's a spillover slice.
- Does NOT modify any e2e spec assertion text. If the new logo fails an existing assertion (e.g., snapshot drift), update the snapshot — not the assertion — via the established `--update-snapshots` workflow.
- Does NOT rewrite the favicon stack (.ico + apple-touch + manifest icons) unless the new design materially breaks the existing favicon's recognizability. If it does: spillover slice.

## Threat model

Visual asset replacement is low-risk; the STRIDE pass below is included for completeness per the `/idea-to-slice` mandatory-threat-model rule, and produces explicit anti-criteria around the few real exposure surfaces.

**S — Spoofing.** No new authenticated endpoints. The logo is public. No threat.

**T — Tampering.** The slice ingests new SVG content. SVGs can contain `<script>` tags, `<foreignObject>`, external entity references, and other vectors that turn an "image" into an XSS / SSRF / data-exfil payload IF the SVG is ever inlined into a React tree as `dangerouslySetInnerHTML`. Mitigation: ship SVGs as static files in `web/public/` referenced via `<img src>` / `<Image>` — never inlined. Engineer audits the resulting SVG for the script-tag set; ideally runs through SVGO with strict config to strip metadata + scripts.

**Anti-criterion (P0-A6 below):** No `<script>`, `<foreignObject>`, `xlink:href` to external URLs, or `<image href>` to external URLs in the shipped SVGs.

**R — Repudiation.** Asset swap doesn't write audit log entries. No threat.

**I — Information disclosure.** The SVG file is public-readable; whatever's in it ships to every visitor. Mitigation: review the SVG for embedded metadata (Inkscape/Illustrator/AI tools often embed creator name, file path, timestamps, layer names). Strip via SVGO before commit.

**Anti-criterion (P0-A7 below):** No personal metadata (`<dc:creator>`, embedded font URLs to external CDNs, source file paths in `<title>` / `<desc>`) in shipped SVGs.

**D — Denial of service.** Logo files load on every page paint. If the new SVG is hand-authored and pathologically complex (deeply nested groups, hundreds of filters, large embedded raster `<image>` data URIs), it slows initial render. Mitigation: cap the SVG at ≤ 8 KB minified for the asset variants; raster derivatives ≤ 16 KB each.

**Anti-criterion (P0-A8 below):** Each shipped logo asset's gzipped size MUST be ≤ 16 KB; the SVG variants ≤ 8 KB gzipped.

**E — Elevation of privilege.** Asset content can't elevate. No threat.

**Verdict.** has-mitigations (T + I + D produce real P0 anti-criteria; S + R + E are no-op).

## Acceptance criteria

### Design selection (the JUDGMENT phase)

- **AC-1.** Engineer produces ≥ 3 distinct design candidates as SVG (committed to a scratch directory `web/public/logo-candidates/<slug>.svg` for in-PR review, then DELETED before merge — only the chosen variant ships).
- **AC-2.** Engineer picks one candidate, records the JUDGMENT in `docs/audit-log/167-logo-redesign-decisions.md` (D1: redesign approach; D2: chosen candidate + rationale; D3: light-vs-dark mirror strategy). If the engineer is uncertain between two top candidates, surface BOTH to the maintainer via an AskUserQuestion before committing the asset swap (this is the only HITL touch-point in the slice).

### Asset replacement

- **AC-3.** `web/public/logo-light.svg` replaced with the new light-theme asset. Diff is a byte-changed file with the same path.
- **AC-4.** `web/public/logo-dark.svg` replaced with the new dark-theme asset. Mirror of AC-3.
- **AC-5.** `web/public/logo-light.png` regenerated from the new light SVG at the existing resolution. Use `rsvg-convert` or equivalent; engineer documents the command in the decisions log.
- **AC-6.** `web/public/logo-dark.png` regenerated from the new dark SVG. Mirror of AC-5.
- **AC-7.** All four shipped assets passed through SVGO (for SVGs) or `pngquant` / `optipng` (for PNGs) per the threat-model anti-criteria. Sizes recorded in decisions log.

### Rendering verification

- **AC-8.** `web/app/login/page.tsx` renders the new light + dark variants correctly (visual smoke test by engineer; screenshot in PR description).
- **AC-9.** `web/components/shell/topbar.tsx` topbar renders the new logo at its actual size without clipping or pixelation. Screenshot.
- **AC-10.** `web/app/layout.tsx` (favicon-equivalent root usage) renders correctly. Screenshot.
- **AC-11.** Existing Playwright e2e specs `web/e2e/logo-render.spec.ts` + `web/e2e/logo-render-production-build.spec.ts` continue to pass (or snapshots are regenerated in the same commit via `--update-snapshots`, with the snapshot diff included in the PR for human review).

### Documentation

- **AC-12.** Decisions log at `docs/audit-log/167-logo-redesign-decisions.md` covers D1/D2/D3 + the rejected candidates with one-sentence rationale each.
- **AC-13.** If wordmark, favicon, apple-touch-icon, or web-manifest icons surface as in-scope during execution: file a spillover slice (168) via `/idea-to-slice`, do not bundle.

## Constitutional invariants honored

- **CLAUDE.md "Style"**: no emojis added to code or docs in this slice.
- **Slice 075's integration contract**: the logo's mount points (topbar, login page, root layout) are untouched. Only assets change.
- **Slice 116's e2e discipline**: Playwright stays advisory for required-checks; snapshot updates are reviewed manually before commit.
- **Threat-model boundary**: no `<script>` or external-href SVG content ships.

## Canvas references

- `Plans/canvas/01-vision.md` §3 — brand presentation (the platform is replacement-grade GRC; the logo carries that signal).
- `Plans/canvas/09-tech-stack.md` — Next.js 16 + Tailwind 4 + shadcn/ui stack the logo renders against.

## Dependencies

- **#153** (logo standalone) — `merged` 2026-05-18 at `d55e036`. This slice supersedes 153's chosen design.
- **#075** (logo integration) — `merged`. Provides the mount-point contract this slice respects.
- **#074** (logo design candidates) — `merged`. Provides the candidate-generation pattern this slice extends.

## Anti-criteria (P0 — block merge)

- **P0-A1.** Does NOT modify any logo-rendering call site (`web/components/shell/topbar.tsx`, `web/app/login/page.tsx`, `web/app/layout.tsx`, `web/components/shell/sidebar.tsx` if present). Asset swap only.
- **P0-A2.** Does NOT add a new top-level component, hook, or import. The existing import statements are byte-identical pre/post.
- **P0-A3.** Does NOT modify `web/e2e/logo-render.spec.ts` or `web/e2e/logo-render-production-build.spec.ts` ASSERTION TEXT. Snapshots updated via the official `--update-snapshots` workflow are the only allowed e2e change.
- **P0-A4.** Does NOT ship more than the four canonical asset paths (`logo-{light,dark}.{svg,png}`). Scratch candidates under `web/public/logo-candidates/` are deleted before merge per AC-1.
- **P0-A5.** Does NOT change the favicon (`.ico` + `apple-touch-icon.png` + `manifest.json` icons) in this slice. Defer to spillover if needed.
- **P0-A6.** Does NOT ship SVGs containing `<script>`, `<foreignObject>`, or external `xlink:href` / `<image href>` references. Threat-model T mitigation.
- **P0-A7.** Does NOT ship SVGs containing personal-data metadata (`<dc:creator>`, source-file paths, external font CDN URLs). Threat-model I mitigation.
- **P0-A8.** Does NOT ship any logo asset whose gzipped size exceeds: SVG ≤ 8 KB, PNG ≤ 16 KB. Threat-model D mitigation.
- **P0-A9.** Does NOT use a vendor-prefixed test token, real brand asset, real company logo, or copyrighted third-party imagery in candidate generation. Original work only.

## Skill mix (3-5)

1. **Designer** — primary; design judgment, candidate generation, light/dark mirror strategy
2. **Engineer** — secondary; SVGO / pngquant pipeline, file-size verification, e2e snapshot regeneration
3. **UIReviewer** (optional) — for the rendering-verification ACs if engineer can't easily produce screenshots in their environment
4. **Security** — already-applied via the inline STRIDE pass; no live invocation needed

## Notes for the implementing agent

**Design-intent guidance (the maintainer's stated dissatisfaction):**

- The current logo "does not show up well." Engineer's first task is to enumerate WHY: low contrast at small sizes? weak silhouette? ambiguous shape vs Tailwind chrome? bad scaling artifacts in the PNG? Inspect the current renders at topbar (~24px), login (~120px), and root (favicon ~16-32px). Photograph or screenshot the failure modes BEFORE designing replacements.
- The redesign should prioritize legibility at the smallest size first (topbar). If it renders crisp at 16-24px, it will render at 120px+ trivially. The inverse is not true.

**JUDGMENT call D1 — redesign approach.** Three viable paths:

- **(a) Generate from scratch.** Engineer (or Designer subagent) produces ≥ 3 fresh candidates from a blank canvas. Highest design freedom; longest wall-clock; uneven quality unless engineer has design chops.
- **(b) Refine existing.** Take the current shipped logo, identify specific failure modes, iterate. Lower risk; preserves any brand recognition the current logo has built; may not address the root issue.
- **(c) Commission external.** File the scope as a maintainer-owned task, hand to a human designer, then integrate. Highest quality; longest wall-clock; only viable if maintainer has the budget.

Engineer picks one + records in decisions log D1. Default recommendation: (a) if engineer has access to a design-capable subagent (Designer skill) or can iterate via a multimodal LLM with image generation; otherwise (b).

**JUDGMENT call D2 — chosen candidate.** When narrowing 3+ candidates to 1, engineer applies these tiebreakers in order:

1. Legibility at 16-24px (the topbar / favicon constraint)
2. Light/dark mirror symmetry (no theme should look like an afterthought)
3. Distinctiveness from generic "shield + check" GRC clichés (the platform's positioning is replacement-grade; the visual identity should match)
4. Recognizability at 1-bit (silhouette only)

**JUDGMENT call D3 — mirror strategy.** Two options for the light/dark pair:

- **Pure invert.** Dark variant is `transform: invert()` of light. Cheap, but often produces muddy intermediate colors.
- **Hand-mirrored.** Two independent SVGs sharing the same shapes but with separate color palettes. More work; cleaner result. Default recommendation: hand-mirrored.

**Provenance.** Surfaced 2026-05-19 via `/idea-to-slice` from the maintainer's qualitative feedback during a continuous-batch session ("the logo does not show up well, I think we should start from scratch and come up with a new design and then replace it in all the spots we are using it today"). This slice supersedes the design landed by slice 153 (logo standalone) which itself was a hotfix-shape repair of 074's candidate selection.

**Spillover triggers to watch for during execution:**

- Wordmark redesign in scope → file slice 168 (`wordmark-redesign`).
- Favicon stack rebuild in scope → file slice 168 (`favicon-redesign`).
- Brand-color palette shift in scope (Tailwind tokens) → file slice 168 (`brand-palette-revision`).
- Marketing-page hero / OG-image / Twitter-card variants → file slice 168 (`marketing-asset-redesign`).
