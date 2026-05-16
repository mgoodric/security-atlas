# 075 — Logo integration (post-approval of slice 074)

**Cluster:** Frontend
**Estimate:** 1d
**Type:** AFK

## Narrative

Slice 074 shipped logo candidates and a design-doc page with a single human-edited `Selected:` line. **The maintainer selected `candidate-04` in PR #180 (slice 074).** This slice takes the approved candidate and integrates it across every surface where a logo belongs: the README hero, the docs site (mkdocs Material `theme.logo`), the web UI top-nav header (`web/components/layout/app-header.tsx`), the browser favicon set, the social-share preview cards (`og:image` + Twitter Card), and the email signature template (if present in slice 029's notification path).

**Selected candidate — what's being integrated:**

- **ID:** `candidate-04`
- **Concept:** Node-graph "A" with warm→cool 8-color temperature gradient (16 lines + 14 dots; uniform 6 px stroke; color carries the hierarchy)
- **Source-of-truth (canonical):** `docs/design/logo-candidates/candidate-04/mark.svg` — hand-authored SVG, NOT a rasterization output. Every derived asset MUST be generated from this SVG; do not derive from the PNGs (which are themselves rasterizations of the SVG).
- **Pre-rendered variants ready to copy or re-render:** `mark-1024.png`, `mark-512.png`, `mark-1024-dark.png`, `mark-512-dark.png` (light variant against `#fafafa`; dark variant against `#0a0a0a`)
- **Color palette (dark variant):** `#f2a2b3` / `#f9c3c3` / `#f7d4c0` / `#f9e6c1` / `#d1e7e0` / `#a0d1e8` / `#7ab8e1` / `#4b8db5` (pastel spectrum, warm→cool)
- **Color palette (light variant):** Tailwind 700-800 family complements — `#9f1239` rose-800 / `#be185d` pink-700 / `#9a3412` orange-800 / `#854d0e` amber-700 / `#065f46` emerald-800 / `#075985` sky-800 / `#0369a1` sky-700 / `#1e40af` blue-800 (same temperature gradient, mirrored by value)
- **Accessibility:** all 16 color slots clear WCAG SC 1.4.11 (3:1) AND SC 1.4.3 (4.5:1) on target backgrounds
- **Full per-version provenance:** `docs/design/logo-candidates/candidate-04/notes.md` (v1-v6 iteration history with every prompt + design call)

**Existing tooling shipped by slice 074 (reuse, don't reimplement):**

- `tools/logo-gen/recolor_by_weight.py` — SVG → PNG rasterization with per-tier color mapping. The `LIGHT_TO_DARK_V6` mapping is active for the selected candidate. Requires `cairosvg` + `pillow`; on macOS needs `DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib`. The script's filename is historical — v6 recolors by line midpoint position, not stroke weight.
- `tools/logo-gen/contrast.py` — WCAG SC 1.4.11 / SC 1.4.3 contrast measurement against `#fafafa` and `#0a0a0a` via per-pixel sampling. Use to verify any new variant (favicon, og-card, email-signature thumbnail) clears the floor.

**Gating constraint (load-bearing — do NOT bypass):** this slice does NOT run until two conditions are both true:

1. Slice 074 is merged on `main`.
2. The `Selected:` line in `docs/design/logo-decision.md` (committed on `main`) reads `Selected: candidate-<NN>` where `<NN>` is one of the candidate IDs from slice 074 — NOT `none — awaiting maintainer approval`. As of slice 074 PR #180 the line reads `Selected: candidate-04`.

The engineer detects condition 2 via:

```bash
grep '^Selected:' docs/design/logo-decision.md \
  | grep -v 'awaiting maintainer approval'
```

…returning a non-empty line. The `_STATUS.md` row for this slice stays `not-ready` until that command returns a match; flipping to `ready` is a maintainer act (either edit `_STATUS.md` directly, OR rely on the next slice-cleanup pass to pick it up — same convention as the deletion follow-on slice from 071).

The integration is mechanical, not subjective: the canonical SVG is at `docs/design/logo-candidates/candidate-04/mark.svg`. Derive every variant (light / dark / favicon-set / og-image-1200x630 / og-image-square) from THAT SVG via `scripts/regen-logo-variants.ts` (new), commit each derived asset at its canonical path, and update every surface reference in one PR.

**Simplification vs. the original slice 075 spec:** the spec was written when the selection was unknown. Knowing cand-04 is SVG-native (not raster-only like the other 9 candidates would have been) eliminates several derivation concerns:

- No raster→raster recoloring needed (the SVG can be re-rendered at any size, any color, deterministically).
- Favicon set: SVG → ICO/PNG conversion is well-trodden (e.g., `rsvg-convert` or Sharp's SVG renderer). No fidelity loss at any target size.
- The light + dark variants are TWO renderings of the SAME SVG with different color maps — not two separately-authored assets. Variant generation = recolor + rasterize, fully deterministic via `recolor_by_weight.py`.

**Six integration surfaces:**

1. **README hero** — replace the slice-074 "(Logo TBD)" HTML comment with a `<picture>` element (light + dark variants, slice-057 pattern) rendered above the project tagline.
2. **mkdocs Material site** — `docs-site/mkdocs.yml` `theme.logo` and `theme.favicon` populated; logo also embedded on `docs-site/docs/index.md` hero.
3. **web UI top-nav header** — `web/components/layout/app-header.tsx` renders the logo at the left edge of the nav bar at 24-32px height with `<Link href="/dashboard">` wrapper. Both light and dark theme variants via `<picture>` semantics (same accessibility pattern as slice 057's screenshots).
4. **Favicon set** — `web/public/favicon.ico` (32×32 multi-resolution), `web/public/apple-touch-icon.png` (180×180), `web/public/icon-192.png` and `web/public/icon-512.png` (PWA manifest sizes — added even though no PWA manifest exists yet; cheap to generate, future-proof). `web/app/layout.tsx` declares them via the Next.js Metadata API `icons` field.
5. **Social-share preview** — `web/public/og-image.png` (1200×630, standard Open Graph aspect), `web/public/twitter-card.png` (1200×675). Both reference the logo + project name + one-line tagline composited via a server-side render step (NOT image-model text generation; same constraint as slice 074's P0-A2). `web/app/layout.tsx` Metadata API `openGraph` and `twitter` blocks declare them.
6. **Email signature (conditional)** — IF slice 029's audit-hub notifications include outbound email templates (the engineer's grill checks this), the email signature gets a small 120px-wide logo embedded as a base64 data URI in the HTML email template. If slice 029 doesn't ship email, this AC is dropped per the per-slice template's grill protocol (AC-N: N/A — record in decisions log).

## Acceptance criteria

- [ ] AC-1: Pre-flight check: the engineer's first action is to verify the gating condition (slice 074 merged + `Selected:` line edited to a real candidate ID). If either is false, the slice exits cleanly with a one-paragraph PR-body note and a `not-ready` status — no further work in this PR. (The orchestrator's `not-ready` filter SHOULD prevent this from being picked up, but the pre-flight makes the failure mode explicit.)
- [ ] AC-2: `scripts/regen-logo-variants.ts` (new) takes the canonical source SVG at `docs/design/logo-candidates/candidate-04/mark.svg` and generates: `web/public/logo-light.svg` (light-palette SVG), `web/public/logo-dark.svg` (dark-palette SVG), `web/public/logo-light.png` (256×256), `web/public/logo-dark.png` (256×256), `web/public/favicon.ico`, `web/public/apple-touch-icon.png`, `web/public/icon-192.png`, `web/public/icon-512.png`, `docs-site/docs/assets/logo-light.svg`, `docs-site/docs/assets/logo-dark.svg`, `docs/images/logo-light.png`, `docs/images/logo-dark.png`. The two SVG outputs are recolor-only transformations of the source (use the `LIGHT_TO_DARK_V6` mapping from `tools/logo-gen/recolor_by_weight.py` as a reference); the PNG and ICO outputs are rasterizations of those SVGs. Script is idempotent + re-runnable via `just regen-logo`.
- [ ] AC-3: README.md hero — the slice-074 "(Logo TBD)" comment is replaced by a `<picture>` element with light/dark variants, rendered above the project tagline. Image references use the canonical paths in `docs/images/`.
- [ ] AC-4: `docs-site/mkdocs.yml` `theme.logo: assets/logo-light.svg` and `theme.favicon: assets/favicon.png`. `docs-site/docs/index.md` hero gets a `<picture>` element above the heading.
- [ ] AC-5: `web/components/layout/app-header.tsx` (new component if not present; slice 005's bootstrap may already have one — verify in grill) renders the logo with `<Link href="/dashboard">` wrapper, 24-32px height, light/dark variants via the same `<picture>` semantics. Renders identically on `/login` (where the user isn't yet authed) and `/(authed)/...` routes.
- [ ] AC-6: `web/app/layout.tsx` Metadata API `icons` field declares the favicon set (favicon.ico + apple-touch-icon + icon-192 + icon-512); `openGraph.images` declares `og-image.png` (1200×630); `twitter.card` set to `summary_large_image` with `twitter.images` declaring `twitter-card.png` (1200×675).
- [ ] AC-7: `web/public/og-image.png` and `web/public/twitter-card.png` are server-side composited (Pillow / sharp / Resvg — engineer picks based on existing toolchain) from the canonical logo + the project name + the one-line tagline. NO image-model text rendering (slice 074's P0-A2 constraint continues to apply for derived assets).
- [ ] AC-8: Visual regression — slice 069's Playwright spec for the login + dashboard layouts is extended (or a new spec added) that asserts the logo `<img>` (or `<picture>`) renders on both pages, at the expected viewport, with the `<source media="prefers-color-scheme: dark">` element present. Mocked-network where needed (slice 069 fixture pattern).
- [ ] AC-9: Conditional email-signature integration: the engineer's grill identifies whether slice 029's audit-hub notification path includes email templates. If yes, the templates get the 120px logo embed; if no, this AC is recorded as N/A in the decisions log.
- [ ] AC-10: `web/package.json` does NOT gain a new image-processing dependency unless absolutely necessary. Since cand-04 is SVG-native, derivation is simpler than the original spec assumed:
  - **Variant SVG generation** (light-palette → dark-palette): text-substitution on the SVG source (the 16 color values + 1 dot color). Plain Node `fs` + string-replace; no library needed.
  - **SVG → PNG rasterization** (all PNG outputs, all favicon sizes): use Next.js's bundled Sharp (already on disk in any built `web/` install — `next/image` depends on it). Sharp's `.svg()` input + `.png({size})` output handles every size in this AC.
  - **SVG → ICO conversion** (the multi-resolution `favicon.ico`): Sharp handles this via `.ico` output or via a one-shot `to-ico` npm utility. Engineer's grill picks.
  - The existing `tools/logo-gen/recolor_by_weight.py` (Python + cairosvg) is the slice-074-era rendering path; slice 075 should NOT depend on Python for an npm-side build script. Use Sharp.
- [ ] AC-11: `CHANGELOG.md` is NOT edited by this slice directly — release-please generates the entry from the Conventional Commit. Commit message: `feat(infra): integrate approved logo across README, docs, web UI, favicon, and social cards (#075)`.
- [ ] AC-12: `_STATUS.md` row 075 is flipped `in-progress` → `in-review` as the final commit on the slice branch (the slice 4-template Step 9 — uniform with every other slice).
- [ ] AC-13: Pre-commit clean. CI green. Total weight of newly-added image assets ≤ 3 MB combined.
- [ ] AC-14: A `docs/audit-log/075-logo-integration-decisions.md` records: (1) the selected candidate ID and the commit SHA where the `Selected:` line was edited (gating-condition audit trail), (2) the variant-generation toolchain choice (AC-10), (3) whether AC-9 applied (slice 029 grill finding), (4) any tradeoffs in the social-card composition (font choice for the tagline, layout decisions).

## Constitutional invariants honored

- **AI-assist boundary**: the canonical logo source is the approved AI-generated candidate from slice 074 (with explicit human approval recorded in `docs/design/logo-decision.md`). All derived assets (variants, social cards) are mechanical transformations of that source, NOT new AI generations — they go through `scripts/regen-logo-variants.ts` with deterministic processing.
- **Working norms — Markdown over prose** (CLAUDE.md): the README hero is the logo, NOT decorative ASCII art or a multi-paragraph visual description
- **Working norms — Ask before destructive operations**: gated on slice 074's `Selected:` line being a real candidate ID, not a placeholder

## Canvas references

- `Plans/canvas/10-roadmap.md §10.1` — v1 binary success test's surfaces (README + UI + docs site all need to look like a real product)

## Dependencies

- **074** (logo design candidates) — MUST be merged AND `Selected:` line edited
- **058** (user docs scaffold) — mkdocs site `theme.logo` and `theme.favicon`
- **057** (README screenshots) — `<picture>` semantics + light/dark variant pattern this slice mirrors
- **005** (frontend bootstrap) — top-nav header location

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT begin work if the gating condition (AC-1) is not satisfied. The slice's first agent action is the pre-flight check; failure exits cleanly without any image-asset generation.
- **P0-A2**: Does NOT re-generate the logo via an AI model. The canonical source is the slice-074 candidate; this slice ONLY transforms (resize, recolor for theme, composite onto social card) — never re-renders.
- **P0-A3**: Does NOT use image-model-rendered text in the social-card composition. The tagline + project-name pass uses a real font with explicit licensing (Inter / JetBrains Mono / etc — engineer picks, records in decisions log) and a server-side text-render API.
- **P0-A4**: Does NOT integrate a "second, slightly different" logo into one surface vs another. The single source of truth is the slice-074 selected candidate; all six surfaces use mechanically-derived variants of THAT.
- **P0-A5**: Does NOT add a recurring CI gate that re-runs `scripts/regen-logo-variants.ts` on every PR. The script runs on demand (`just regen-logo`); the generated assets are committed; freshness is a maintainer act tied to logo updates, not a per-PR check.
- **P0-A6**: Does NOT exceed 3 MB total for the integration assets. Social cards at 1200×whatever should compress to ≤ 500 KB each; favicons are tiny; the logo SVGs are vector. If a derived asset breaks the budget, regenerate with tighter compression.
- **P0-A7**: Does NOT change `web/package.json`'s `"version"` field (slice 072 P0-A4 continues to apply — that field is a workspace artifact, NOT the user-facing version).

## Skill mix (3–5)

- shadcn/ui composition (top-nav header positioning, `<picture>` element rendering with theme-aware sources)
- Next.js Metadata API (icons, openGraph, twitter — the canonical home for favicon + social-card declarations)
- Image processing toolchain (sharp / Pillow / rsvg-convert — getting all variant sizes from one canonical source without per-variant manual touch-up)
- Server-side text compositing (real font, deterministic output, no AI text rendering)
- `simplify` (the README + docs-site hero copy stays tight; the logo replaces visual emptiness, doesn't add new prose)

## Notes for the implementing agent

- **The canonical source is `mark.svg`, not a PNG.** Every derived asset (variant SVGs, favicons, social cards, web header img) MUST descend from the SVG. Do not re-rasterize from one of the pre-rendered PNGs — fidelity loss compounds.
- **The 8-color palettes are exact.** Dark variant: `#f2a2b3` / `#f9c3c3` / `#f7d4c0` / `#f9e6c1` / `#d1e7e0` / `#a0d1e8` / `#7ab8e1` / `#4b8db5`. Light variant: `#9f1239` / `#be185d` / `#9a3412` / `#854d0e` / `#065f46` / `#075985` / `#0369a1` / `#1e40af`. The dark-to-light mapping (positionally — line midpoint y-coordinate) is encoded in `tools/logo-gen/recolor_by_weight.py`'s `LIGHT_TO_DARK_V6` dictionary. Mirror that mapping in your `scripts/regen-logo-variants.ts` so the recolor logic is reproducible.
- AC-10's image-toolchain choice is simpler than the original spec: Sharp (bundled with Next.js) handles SVG input → PNG output at every target size. No Python, no cairosvg, no rsvg-convert binary needed in CI. The `tools/logo-gen/recolor_by_weight.py` script is a build-time helper from slice 074 — kept in the repo for `contrast.py` verification value, but slice 075 should NOT depend on it for the user-facing build pipeline.
- The grill for AC-9 (slice 029 email templates) needs a real `grep -rE 'email|smtp|sendmail' internal/audit/notifications/` to determine reality. If no email shipping in current code, the AC is N/A. Don't speculate.
- The social cards (AC-7) are the highest-craft asset in this slice. Bad social cards look obviously AI-generated; good ones look intentional. Spend extra iteration time on the composition: logo position, tagline font size, color discipline (matches the canonical mark's pastel / sky-family palette).
- **Favicon-scale consideration:** cand-04 v6 uses uniform 6px stroke at the 1024px native size. Direct downscale to 16px favicon will collapse the line detail. Consider authoring a **simplified favicon variant** in the SVG source — fewer lines (perhaps just the heaviest 3-4 backbone lines + the foundation node), single accent color (the deep blue `#4b8db5` / `#1e40af`). Record the decision in the decisions log. The full 16-line gradient mark is for medium+ sizes (top-nav header, README hero, social cards); favicon gets the simplified version.
- The `Selected:` line on `main` reads `Selected: candidate-04` (committed in slice 074 PR #180). Verify before opening this slice's PR — if a teammate started 075 against a stale checkout, the pre-flight (AC-1) catches it.
- This slice WILL touch six distinct surfaces. The risk is partial integration ("README done, header done, but forgot favicon"). Use the AC checklist as the literal merge gate: every AC PASS before opening the PR.
- The existing `contrast.py` from slice 074 should be used to verify any new variant (favicon at all sizes, og-card composition, email-signature thumbnail) clears WCAG SC 1.4.11 on its rendered background.
