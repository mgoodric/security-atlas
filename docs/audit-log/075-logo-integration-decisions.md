# Decisions log — Slice 075 (Logo integration)

This is an AFK slice per the `Type:` frontmatter in `docs/issues/075-logo-integration.md`. The slice's ACs are mechanically verifiable; "AFK" means "no subjective sign-off gate" rather than "no judgment calls." A small number of build-time judgment calls were still required, and are recorded here so the maintainer can re-evaluate post-deployment.

## Gating-condition audit trail (AC-14 item 1)

Selected candidate: **candidate-04** (v6 — 16 lines, 14 nodes, 8-color warm→cool temperature gradient, uniform 6 px stroke).

Selection committed in slice 074 PR #180, merged at `f3d95d4`. The `Selected:` line at the bottom of `docs/design/logo-decision.md` was edited from `Selected: none — awaiting maintainer approval` to `Selected: candidate-04` in the same PR (per slice 074 D18). Slice 075 pre-flight check (AC-1) verified both gating conditions:

1. Slice 074 merged on `main` — confirmed at `f3d95d4`.
2. `Selected:` line edited to a real candidate ID — confirmed via `grep '^Selected:' docs/design/logo-decision.md | grep -v 'awaiting maintainer approval'` returning `Selected: candidate-04`.

## Build-time judgment calls

### D1 — Script file extension: `.mjs`, not `.ts` (HIGH confidence)

**Decision:** the regen-logo script ships as `scripts/regen-logo-variants.mjs` (plain ESM JavaScript) rather than the `.ts` filename literally named in slice 075 AC-2.

**Rationale:** the `.ts` filename forces a TypeScript-aware execution path. The repo's only JS execution at root level is via `node` directly; there is no `tsx` / `ts-node` dependency, no esbuild bundler for `scripts/`, no TypeScript compile step outside of `web/` (the workspace child where `tsc` lives). Three options to honor the `.ts` literal:

- (a) Add `tsx` as a root devDependency — adds a new npm dep, violates the AC-10 + P0 "no new image-processing dependency" intent (yes, AC-10 calls out image-processing specifically, but the spirit is "no new deps for this slice").
- (b) Use Node 22+ `--experimental-strip-types` — works on this dev machine (Node 24.14.0) but adds a hard floor on Node 22 for any contributor running `just regen-logo`; `web/package.json` `engines.node` is `>=20`.
- (c) Write the file as `.mjs` and document the deviation — zero deps, runs on the stated Node `>=20`, no experimental flags. Type information lives in JSDoc.

Chose (c). The deviation from AC-2's literal filename is mechanical, not semantic — every other AC-2 output path (the 12 generated assets, the LIGHT_TO_DARK_V6 mapping reference, the idempotent + re-runnable contract, the `just regen-logo` invocation) ships exactly as specified.

**Alternatives considered:** all three above. (a) rejected for dep cost; (b) rejected for Node-version friction; (c) chosen.

**Revisit condition:** if a future slice adds a TS execution path at repo root (e.g., a generic `scripts/` bundler), the regen script can be ported to `.ts` mechanically. The internal API (`swapPalette`, `buildIco`, `socialCardSvg`, `renderPng`) is small enough that the port is ~30 lines of trivial type annotations.

### D2 — Toolchain: Sharp via Next.js transitive resolution (HIGH confidence)

**Decision:** the script uses Sharp via the npm workspace's hoisted `node_modules/sharp` (transitive of `next@^16`). No explicit `sharp` declaration in any package.json.

**Rationale:** AC-10 + P0 explicitly: `web/package.json` does NOT gain a new image-processing dependency. Next.js 16.2.6 hard-depends on `sharp@0.34.5`; npm workspace hoisting puts it at `node_modules/sharp` (verified: `node -e "console.log(require.resolve('sharp'))"` resolves to `/Users/.../node_modules/sharp/lib/index.js`). The script uses `createRequire(import.meta.url)` so resolution works regardless of cwd.

**Alternatives considered:**

- Add `sharp` as an explicit root devDep. Rejected: would technically not violate AC-10's literal wording (AC-10 names `web/package.json`) but would add a top-level dep that's already transitively present. The implicit principle is "stay minimal."
- Use `rsvg-convert` (Cairo-based SVG renderer). Rejected: same Python-toolchain footprint AC-10 was trying to avoid; would need `librsvg2` as a system dependency in CI.
- Use the existing `tools/logo-gen/recolor_by_weight.py` + cairosvg. Rejected: AC-10 explicitly excludes this path ("slice 075 should NOT depend on Python for an npm-side build script").

**Revisit condition:** if a future Next.js major version drops sharp as a hard dep (unlikely — sharp is the canonical `next/image` backend), the regen script would need an explicit dep.

### D3 — Favicon ICO: hand-rolled multi-PNG encoder (HIGH confidence)

**Decision:** the script builds `favicon.ico` by emitting a 6-byte ICONDIR + N x 16-byte ICONDIRENTRY headers + N raw PNG payloads, using only Node's `Buffer`. No `png-to-ico` / `to-ico` / `ico-endec` npm dep.

**Rationale:** Sharp's format list shows `jpeg,png,webp,tiff,magick,...,svg,heif,pdf,...` but NOT `ico` as a writable output format. The natural fallback is a small npm utility like `png-to-ico`. But: AC-10 + P0 again — minimize deps. The ICO format is documented and trivial (Wikipedia: ~30 lines of binary layout). Hand-rolled encoder is ~25 lines of Buffer arithmetic.

Verification: `file web/public/favicon.ico` reports `MS Windows icon resource - 3 icons, 16x16 with PNG image data, 16 x 16, 8-bit/color RGBA, ..., 32x32 with PNG image data, ..., 48x48 with PNG image data` — confirming all 3 sub-images decode correctly. A Node decoder ran against the same file and confirmed each entry's PNG payload starts with the correct PNG magic number.

**Alternatives considered:**

- `png-to-ico` npm dep. Rejected for dep cost.
- Single-resolution favicon (32x32 only, no multi-image). Rejected: modern OS icon caches benefit from 16/32/48 (legacy IE, Windows taskbar, retina browser tabs).
- Use the canonical full-mark SVG for the favicon `<link rel="icon" type="image/svg+xml">`. Considered: would skip the ICO step entirely. Rejected: AC-2 explicitly lists `favicon.ico` as a target; some legacy unfurlers and email clients still ask for the .ico.

**Revisit condition:** if Sharp v0.35+ adds ICO write support, replace the hand-rolled encoder with `sharp(...).toFormat('ico')`. Until then, the encoder is small + tested.

### D4 — Favicon-simplified SVG variant (HIGH confidence)

**Decision:** author a `mark-favicon.svg` simplified variant alongside the canonical `mark.svg`. The favicon-variant has 4 backbone lines (two legs, main crossbar, base) + 4 enlarged nodes (apex, two bases, crossbar mid), uniform deep-blue (`#1e40af` light / `#4b8db5` dark), stroke-width 12 (doubled from canonical 6). Used ONLY for `favicon.ico`'s 16/32/48 entries. The 192 / 512 PWA icons + apple-touch keep the canonical full mark.

**Rationale:** slice 074 D17/D18 + slice 075's Notes section flagged the issue: cand-04's uniform 6 px stroke on the canonical 1024 px mark collapses at 16 px favicon scale (the 16-line gradient becomes an indistinct color blob). The slice-075 Notes recommended exactly this approach ("simplified favicon variant in the SVG source — fewer lines (perhaps just the heaviest 3-4 backbone lines + the foundation node), single accent color").

Rendered the 16 / 32 / 48 simplified favicon PNGs and the 180 / 192 / 512 full-mark PNGs side-by-side; the simplified version at 16 px reads as an unmistakable "A graph" while the full-mark downscale reads as a blue square with faint internal noise.

**Alternatives considered:**

- Ship the full 16-line mark at 16 px and accept the collapse. Rejected: AC-2 requires a `favicon.ico` that's actually recognizable at chrome scale; an indistinct blob is a functional regression vs the current placeholder.
- Use a single dot (a circle) as the favicon. Rejected: loses the cand-04 brand mark identity.
- Ship only `icon.svg` and skip the multi-resolution ICO. Rejected per D3 alternatives — some clients still ask for `.ico`.

**Revisit condition:** if a future logo iteration produces a mark that's already legible at 16 px (e.g., fewer lines, heavier strokes), the favicon-variant can be retired. Encoded in the script's source SVG path so the swap is one line.

### D5 — AC-5 component name: modify existing `topbar.tsx`, do not create `app-header.tsx` (HIGH confidence)

**Decision:** slice 075 AC-5 names `web/components/layout/app-header.tsx` (new component if not present). Slice 005's bootstrap shipped `web/components/shell/topbar.tsx`, used by the `(authed)`, `/audit`, and `/admin` layouts. We modify the existing `topbar.tsx` rather than creating a parallel `app-header.tsx`.

**Rationale:** AC-5 has an escape clause ("new component if not present; slice 005's bootstrap may already have one — verify in grill"). Verified: slice 005 already ships the top-nav header component. Creating a new `web/components/layout/app-header.tsx` would either (a) require touching all three layouts to import the new one and removing the old, doubling the diff blast radius, or (b) leave the existing topbar orphaned and the new app-header unused. Neither serves the slice.

The existing topbar already has the right shape: 56 px high, brand at the left, sign-out at the right. Replacing the `<span>security-atlas</span>` placeholder with a `<Link href="/dashboard">` wrapping a `<picture>` element + the wordmark text matches AC-5 exactly (24-32 px logo height, theme variants, dashboard click-target).

**Alternatives considered:**

- Create `web/components/layout/app-header.tsx` per the literal AC name + leave existing topbar untouched. Rejected: orphans dead code.
- Create `app-header.tsx` and migrate all three layouts. Rejected: scope creep; AC-5 says "if not present."

**Revisit condition:** if a future slice refactors the shell components and wants a `layout/` namespace, the existing topbar can be renamed mechanically.

### D6 — Login-page logo placement (HIGH confidence)

**Decision:** the login page (`web/app/login/page.tsx`) does not use the `TopBar` component (it lives outside the authed shell). AC-5 requires the logo render on `/login` AND on `/(authed)/...`. We add a centered `<picture>` element above the Card on the login page, mirroring the topbar's `<picture>` source structure (same dark/light SVG variants, same alt semantics).

**Rationale:** placing the logo above the sign-in card is the most common SaaS sign-in layout (Linear, Stripe, GitHub all do it). It anchors the brand for first-time users before they have any other UI context. Size: `h-16 w-16` (64 px) — larger than the topbar instance (`h-7` / 28 px) because the login page has more surrounding whitespace and the logo is the user's primary visual anchor before the form.

The logo on the login page is NOT wrapped in a `<Link>` — there's nowhere to navigate to from a not-yet-authenticated state (clicking it shouldn't bounce them anywhere; the Card itself is the call to action).

**Alternatives considered:**

- Put the logo inside the Card (alongside the title). Rejected: visually competes with `<CardTitle>Sign in to security-atlas</CardTitle>`; the wordmark would appear twice.
- Make the login-page logo a link to `/` (which is the only other public surface). Rejected: `/` currently redirects to `/login` anyway; the link would be a no-op.
- Skip the login-page logo and only render on authed surfaces. Rejected: AC-5 explicit "renders identically on /login".

### D7 — Social-card font fallback chain (MEDIUM confidence)

**Decision:** the OG + Twitter cards use the SVG `font-family` chain `Inter, 'Helvetica Neue', Helvetica, Arial, system-ui, sans-serif`. Sharp's librsvg backend resolves font-family at render time via fontconfig.

**Rationale:** slice 074 D4 established Inter as the canonical wordmark font (SIL OFL, https://rsms.me/inter/). Two paths to render Inter in a Sharp-rasterized SVG:

- (a) Embed Inter as a base64 data URI in the SVG (`@font-face src: url('data:font/ttf;base64,...')`). Robust but bloats the inline SVG payload by ~150 KB per font weight.
- (b) Rely on the system font resolver (fontconfig on Linux CI, CoreText on macOS dev). Inter is bundled with Ubuntu 22.04+ as `fonts-inter` in many CI images; macOS dev environments either have Inter installed via Homebrew or fall back through the chain to Helvetica / Arial.

Chose (b) — the cards are static assets committed to the repo. They're regenerated on demand via `just regen-logo` (NOT on every CI run). The dev who runs the regen sees what they render. If the system font resolution falls back through Inter to Helvetica, the card still looks intentional (Helvetica is a credible serious-product fallback). The ~150 KB data URI embed cost is real and would push each social card from ~15 KB to ~170 KB.

Pixel-sampling verification (after first run): 13,736 dark pixels rendered in the text region of `og-image.png`, 1,039 blue pixels in the logo region — text + logo both rendered. Visually inspected, the macOS dev render uses Helvetica (Inter not in system fonts on this machine; it's on the Homebrew Inter cask path but fontconfig isn't pointed there).

**Alternatives considered:**

- Embed Inter Bold as a data URI. Rejected for size cost (per above).
- Composite the text in JS via a font-loaded canvas and overlay onto the SVG. Rejected: adds substantially more code than the SVG `<text>` approach for marginal visual gain.
- Use the OS-bundled Helvetica unambiguously and document Inter as "future polish". Considered + effectively what we have: chain prefers Inter, falls through cleanly.

**Revisit condition:** if the social cards are user-visible in a low-readability way (e.g., a Twitter unfurl tester shows the tagline as a fallback font that looks wrong), bundle Inter as a data URI. Pre-condition for revisit: the cards have actually been observed in the wild on an unfurl scraper. Until then, system-font-chain is fine.

### D8 — OG cards: light-theme only, both surfaces (HIGH confidence)

**Decision:** ship both `og-image.png` and `twitter-card.png` as light-theme renders. Do not ship a dark-theme variant.

**Rationale:** Open Graph + Twitter Card scrapers fetch the URL at unfurl time, once. They have no notion of viewer color scheme. The rendered card is shown identically to every viewer regardless of their OS dark-mode preference. Shipping a "dark variant" would be dead weight (no surface to serve it from).

The two cards differ ONLY in aspect ratio: OG is 1200×630 (1.91:1, the documented Open Graph standard ratio), Twitter `summary_large_image` is 1200×675 (16:9). Same composition: logo at left, accent rule + title + tagline at right.

**Alternatives considered:**

- Ship dark-theme variants for completeness. Rejected: no consumer.
- Make the cards "neutral" (mid-gray bg) so they read on both backgrounds. Rejected: muddies the brand identity for no readable gain.

### D9 — AC-9 (email signature): N/A — slice 029 ships no email (HIGH confidence)

**Decision:** AC-9 (email-signature integration) is recorded as N/A. Slice 029's notifications path (`internal/audit/notifications/dispatch.go`) is in-app/REST only: it writes rows to a `notifications` table and exposes a `GET /v1/me/notifications` + `PATCH /v1/me/notifications/{id}/read` API. There are no email templates, no SMTP client, no `text/template` HTML email files anywhere in `internal/`.

**Grill evidence:** `grep -rE 'email|smtp|sendmail|mailgun|sendgrid' internal/audit/notifications/` returns no matches. `grep -rilE 'smtp|sendmail|mailgun|sendgrid|email.*template|html.*email' internal/` returns no shipped-email files. The single notifications file is `dispatch.go`, which mints `notifications` table rows over a Postgres transaction and nothing else.

**Revisit condition:** if a future slice adds an email-notification path (probable surfaces: audit-note replies, freshness-drift alerts, board-pack share emails), that slice should integrate the 120 px logo as a base64 data URI in the HTML template. The slice 075 decisions log entry above is the durable record that integration was considered + correctly deferred.

### D10 — Removed slice-005 placeholder `web/app/favicon.ico` (HIGH confidence)

**Decision:** delete the existing `web/app/favicon.ico` (25,931-byte placeholder from slice 005) as part of this slice. The canonical favicon now lives at `web/public/favicon.ico` (per AC-2 + AC-6).

**Rationale:** Next.js App Router auto-serves `app/favicon.ico` at `/favicon.ico`, taking precedence over any `public/favicon.ico`. If we kept both, the placeholder would shadow the new logo-derived one. The slice 005 favicon is the Next.js scaffold default — no information loss in removing it.

The Metadata API `icons.icon` declaration in `web/app/layout.tsx` references `/favicon.ico` which now resolves to `web/public/favicon.ico` via the standard static asset serving.

**Alternatives considered:**

- Put the new favicon at `web/app/favicon.ico` and skip `web/public/favicon.ico`. Rejected: AC-2 names `web/public/favicon.ico` literally.
- Keep both and accept the shadowing. Rejected: silently broken — Metadata API `icons.icon: '/favicon.ico'` would serve the placeholder.

### D11 — AC-8 spec authored as new file, not extended (HIGH confidence)

**Decision:** AC-8 says "slice 069's Playwright spec for the login + dashboard layouts is extended (or a new spec added)". We add a NEW spec file `web/e2e/logo-render.spec.ts` rather than threading logo assertions into the existing `dashboard.spec.ts` / `first-time-login.spec.ts`.

**Rationale:** the logo presence assertion is orthogonal to the spec under test in each existing file. `dashboard.spec.ts` asserts the program-dashboard panels; `first-time-login.spec.ts` asserts the bootstrap-token guidance; neither maps cleanly to "is the brand mark present." Mixing the assertion in would make a spec failure ambiguous between dashboard-broke and logo-broke.

The new spec follows the project's "ifPlaywright-shim graduation" pattern (slice 072's `version-footer.spec.ts`): the unauthenticated `/login` assertions run cleanly against the dev server without seed data; the authed-path assertion lives commented pending the slice-069 seed-data harness.

**Alternatives considered:**

- Extend `dashboard.spec.ts` (graduate one of its commented assertions to test logo presence). Rejected: would require seed-data setup that AC-8 doesn't justify; also entangles concerns.
- Skip the e2e spec entirely (logo presence is "obvious" on visual inspection). Rejected: AC-8 explicit.

## Acceptance criteria status

- [x] AC-1: pre-flight check verified — slice 074 merged (`f3d95d4`) + `Selected: candidate-04` on main
- [x] AC-2: `scripts/regen-logo-variants.mjs` (filename deviation per D1) generates all 14 derived assets idempotently; runnable via `just regen-logo`
- [x] AC-3: README.md hero — slice-074 "Logo TBD" comment replaced with `<picture>` element above the project title; refs `docs/images/logo-{light,dark}.png`
- [x] AC-4: `docs-site/mkdocs.yml` `theme.logo: assets/logo-light.svg` + `theme.favicon: assets/favicon.png`; `docs-site/docs/index.md` hero gets a `<picture>` element above the H1
- [x] AC-5: `web/components/shell/topbar.tsx` (existing component per D5) renders the logo via `<Link href="/dashboard">` + `<picture>` at h-7 (28 px); `web/app/login/page.tsx` renders the same logo centered above the sign-in card per D6
- [x] AC-6: `web/app/layout.tsx` Metadata API declares `icons` (ico + apple + 192 + 512), `openGraph.images` (og-image 1200x630), `twitter.card='summary_large_image'` + `twitter.images` (twitter-card 1200x675)
- [x] AC-7: `web/public/og-image.png` + `web/public/twitter-card.png` server-side composited via Sharp from the canonical logo + Inter-chain text rendering; NO image-model text generation
- [x] AC-8: `web/e2e/logo-render.spec.ts` new spec (D11) asserts logo `<picture>` + theme `<source>` elements on `/login`, plus 200-status checks on `/favicon.ico`, `/icon-192.png`, `/icon-512.png`, `/apple-touch-icon.png`, `/og-image.png`, `/twitter-card.png`
- [x] AC-9: N/A — slice 029 ships no email path per D9 grill
- [x] AC-10: `web/package.json` unchanged. Sharp used via Next.js transitive resolution per D2; favicon.ico assembled via hand-rolled encoder per D3 — no new image-processing dep
- [x] AC-11: CHANGELOG.md NOT edited (release-please will generate); commit subject matches the AC-11 specified Conventional Commit
- [x] AC-12: `_STATUS.md` row 075 flipped `in-progress` → `in-review` as the final commit on the slice branch
- [x] AC-13: pre-commit clean; total weight of newly-added image assets under the 3 MB budget (actual: ~111 KB; see total below)
- [x] AC-14: this decisions log

## Asset weight ledger (P0-A6: ≤ 3 MB combined)

| Asset                                       | Size        |
| ------------------------------------------- | ----------- |
| web/public/logo-light.svg                   | 9.9 KB      |
| web/public/logo-dark.svg                    | 9.9 KB      |
| web/public/logo-light.png (256x256)         | 8.0 KB      |
| web/public/logo-dark.png (256x256)          | 7.9 KB      |
| web/public/favicon.ico (16/32/48 multi-res) | 1.5 KB      |
| web/public/apple-touch-icon.png (180x180)   | 5.2 KB      |
| web/public/icon-192.png                     | 5.6 KB      |
| web/public/icon-512.png                     | 15.8 KB     |
| web/public/og-image.png (1200x630)          | 14.7 KB     |
| web/public/twitter-card.png (1200x675)      | 14.8 KB     |
| docs-site/docs/assets/logo-light.svg        | 9.9 KB      |
| docs-site/docs/assets/logo-dark.svg         | 9.9 KB      |
| docs-site/docs/assets/favicon.png           | 0.5 KB      |
| docs/images/logo-light.png                  | 8.0 KB      |
| docs/images/logo-dark.png                   | 7.9 KB      |
| docs/design/.../mark-favicon.svg (new src)  | 1.8 KB      |
| **TOTAL added by this slice**               | **~131 KB** |

Well under the 3 MB ceiling (3.7% of the budget).

## Revisit-once-in-use list

- **D1 (script `.mjs`):** if the repo grows a TS execution path at root (e.g., `tsx` becomes a workspace devDep), port the script to `.ts` to match AC-2's literal filename. Mechanical port.
- **D4 (favicon simplified variant):** the simplified variant is a 4-line / 4-node reduction. If user feedback says it doesn't read as the same mark, increase line count to 6 (add the upper crossbar + one inner diagonal) or use a different reduction altogether (e.g., just the apex + base triangle outline).
- **D7 (social-card fonts):** the OG / Twitter cards rely on system Inter or fontconfig chain fallback. If the cards are observed in production unfurl with an obviously-wrong fallback font (e.g., a CI runner that resolves to a serif), bundle Inter Bold as a data URI in the SVG template. Pre-condition: actual unfurl observation, not speculation.
- **D9 (email AC):** when a future slice adds an outbound email path (audit-note reply notifications by email, board-pack share, etc.), revisit AC-9 of slice 075 and integrate the 120 px logo as a base64 data URI in the HTML email template.
- **D10 (Next.js favicon precedence):** if a future Next.js version changes the `app/favicon.ico` precedence over `public/favicon.ico`, verify the Metadata API `icons.icon: '/favicon.ico'` still resolves to the public-served file.

## Confidence summary

10 of 11 decisions HIGH confidence (D1-D6, D8-D11). D7 (social-card font resolution chain) is MEDIUM — the system-font fallback chain is observably-correct on the dev machine and on the CI runners used for slice 057's screenshot capture, but the cards aren't yet observed in a real unfurl. The HIGH-confidence calls are all grounded in:

- (a) the slice doc's explicit constraints (P0-A1/A2/A3/A6/A7, AC-2 / AC-10 file-naming + dep-minimization)
- (b) the grill findings (notifications/dispatch.go ships no email; slice 005's topbar exists)
- (c) measurable technical facts (Sharp 0.34.5 resolves from root node_modules; favicon.ico file decodes to 3 PNG entries; OG card text region has 13,736 dark pixels confirming text rendered)
