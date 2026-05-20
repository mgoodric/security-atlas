# 176 — Logo variant follows app theme + README/docs asset refresh

**Cluster:** Frontend (theme coupling) + Docs (asset refresh)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

**WHY.** Surfaced 2026-05-19 via maintainer report: "We are using the light colored logo on light colored background now. I cannot see the logo basically at all. I think we need to be using the dark logo we generated."

Two distinct bugs introduced by slice 167's design landing without follow-through:

**Bug A — logo variant driven by OS preference, not app theme.** All three mount sites (`web/components/shell/topbar.tsx`, `web/app/login/page.tsx`, `web/app/layout.tsx`) use the standard `<picture>` element with `media="(prefers-color-scheme: dark|light)"` source matching. This tracks the **operating system's** dark/light preference, NOT the application's own theme picker (which slice 070/170 made an operator-controlled value persisted in localStorage).

Failure mode: operator with OS in dark mode + app theme set to "light" (or "system" resolved to light via localStorage post-slice-170) renders against a LIGHT app background, but the `<picture>` matches `prefers-color-scheme: dark` first → browser serves `/logo-dark.svg` (light/near-white ink, `#f8fafc`) → invisible against the light app background.

The fix is to drive the logo variant from the app's actual rendered theme — Tailwind's `dark:` class on `<html>` — rather than the OS preference. Pattern: a `<ThemeAwareLogo />` (or similar) component that reads the current theme state (whatever slice 170 exposes; likely via the `useTheme()` hook, or by reading the class on `<html>` via `matchMedia` / a small custom hook) and renders the matching `<img>` with the correct `src`.

**Bug B — README and docs/images/ carry the OLD logo.** Slice 167 (PR #367 at `516e043`) shipped the new "Cartographer's Star" hand-authored design but **only swapped `web/public/logo-*.{svg,png}`**. The README + any rendered docs that reference `docs/images/logo-light.png` + `docs/images/logo-dark.png` (file dates 2026-05-15, pre-slice-167) STILL serve the old design. README is the GitHub-rendered front door of the project — it carries the brand impression for every new visitor.

Slice 167's anti-criterion P0-A4 said "Does NOT ship more than the four canonical asset paths." That was the right constraint for slice 167's scope (keep blast radius tight). But the consequence is that docs/images/ and README assets diverged from the actual product logo — slice 176 closes that gap.

**WHAT.** Two surfaces, both mechanical:

1. **Frontend (Bug A fix)**: Add a small `<ThemeAwareLogo size variant?>` component (or extend the existing inline `<picture>` blocks) that reads the app theme state and picks the variant. Update the three mount sites to use it. Default behavior when app theme is `system`: fall back to `prefers-color-scheme`. Default behavior when app theme is `light` or `dark` (explicit): pick the corresponding variant regardless of OS.

2. **Docs (Bug B fix)**: Regenerate `docs/images/logo-light.png` + `docs/images/logo-dark.png` from the slice-167 SVG sources at `web/public/logo-{light,dark}.svg`. Use the same `rsvg-convert` (or `magick` / `inkscape`) command the slice-167 Designer documented in `docs/audit-log/167-logo-redesign-decisions.md`. Optimize with `pngquant` + `optipng` per slice 167 AC-7.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT redesign the logo. Slice 167's "Cartographer's Star" stays.
- Does NOT change the four `web/public/logo-*.{svg,png}` files. Slice 167 owns those.
- Does NOT change the SVG markup or color values. The variant SVGs are correct as designed; the bug is in the picker logic, not the assets.
- Does NOT alter the prefers-color-scheme fallback for users who haven't set an explicit app theme. System = OS-following is the right default.
- Does NOT modify Tailwind dark-mode config or add new theme tokens.
- Does NOT swap favicon (`.ico` + `apple-touch-icon.png` + manifest icons). Defer per slice 167 P0-A5 — a separate slice handles favicon refresh if needed.
- Does NOT update the mockups under `Plans/mockups/`. That directory is iteration-1 reference; slice 075 retired it from production.

## Threat model

Pure visual asset + frontend picker logic. STRIDE pass produces minimal threats (slice 167's threat model carries forward almost entirely):

**S — Spoofing.** N/A. No new auth surface.

**T — Tampering.** The new `<ThemeAwareLogo />` reads `<html>` class state via `useEffect` or `matchMedia`. No user input crosses a trust boundary. The regenerated PNGs go through SVGO/pngquant per slice 167's anti-criteria; metadata stripping preserves slice 167's STRIDE-I + STRIDE-T mitigations.

**R — Repudiation.** N/A.

**I — Information disclosure.** Regenerated PNGs MUST be re-stripped of metadata (no `<dc:creator>`, no source paths) per slice 167's STRIDE-I anti-criterion P0-A7. Operators copying the regenerator command verbatim from the slice-167 decisions log should inherit the stripping.

**D — Denial of service.** Inherited from slice 167: gzipped size caps SVG ≤ 8 KB / PNG ≤ 16 KB. Regenerated docs/images/ PNGs MUST also pass under these caps.

**E — Elevation of privilege.** N/A.

**Verdict.** has-mitigations (T + I + D produce inherited anti-criteria from slice 167; no new threats introduced).

## Acceptance criteria

### Bug A — theme coupling (frontend)

- **AC-1.** NEW small component (e.g., `web/components/shell/theme-aware-logo.tsx`) OR extension of an existing utility that selects logo variant based on the app's theme state (Tailwind `dark:` class on `<html>`, slice 170's persisted theme state, or `useTheme()`-equivalent hook).
- **AC-2.** When app theme is `light`: serves `/logo-light.svg` regardless of OS `prefers-color-scheme`.
- **AC-3.** When app theme is `dark`: serves `/logo-dark.svg` regardless of OS `prefers-color-scheme`.
- **AC-4.** When app theme is `system` (explicit) OR unset: falls back to `prefers-color-scheme` (current behavior). Test both OS-dark + OS-light branches.
- **AC-5.** `web/components/shell/topbar.tsx` swaps the inline `<picture>` for the new component.
- **AC-6.** `web/app/login/page.tsx` swaps the inline `<picture>` for the new component.
- **AC-7.** `web/app/layout.tsx` (if it renders the logo — verify) swaps to the new component, OR documents that it doesn't render the logo directly.
- **AC-8.** Vitest unit test for the new component: each of the 4 theme/OS-pref combinations renders the expected `src`.
- **AC-9.** Playwright e2e (under existing logo-render specs): screenshot test in light-mode confirms the logo's actual rendered pixel contrast against the background ≥ a minimum threshold (e.g., 30% delta). This catches the original Bug A in CI.

### Bug B — docs/README refresh

- **AC-10.** `docs/images/logo-light.png` regenerated from `web/public/logo-light.svg`. Engineer documents the exact regenerator command in the decisions log.
- **AC-11.** `docs/images/logo-dark.png` regenerated from `web/public/logo-dark.svg`. Same command pattern.
- **AC-12.** Both regenerated PNGs pass through `pngquant` + `optipng` per slice 167 AC-7. Each ≤ 16 KB gzipped (P0-A8 inherited from slice 167).
- **AC-13.** Visual diff: regenerated `docs/images/logo-{light,dark}.png` match the slice-167 SVG sources byte-equivalently when re-rendered (modulo lossless PNG compression artifacts).

### Documentation

- **AC-14.** Decisions log at `docs/audit-log/176-logo-theme-coupling-decisions.md`: D1 (which picker pattern — Tailwind `dark:` class watcher vs `useTheme()` hook vs `matchMedia` listener), D2 (component naming), D3 (regenerator-command choice — rsvg-convert vs magick vs inkscape).
- **AC-15.** CHANGELOG entry under `[Unreleased] / Fixed`: "Logo variant now follows app theme (Tailwind dark class), not OS preference, so the light-on-light invisibility bug is closed. README + docs/images/ regenerated to match the slice-167 Cartographer's Star design (#176)."

## Constitutional invariants honored

- **CLAUDE.md "Style"**: no emojis in code or docs.
- **Slice 075's integration contract**: the logo's mount points get a small wrapper component swap (≤ 6 lines per site) — surrounding code byte-identical otherwise.
- **Slice 167's anti-criteria carry forward**: P0-A1 (no layout change), P0-A6 (no `<script>` in SVGs — we don't touch SVGs), P0-A8 (size caps), P0-A9 (original work — regenerated from slice-167 originals).

## Canvas references

- `Plans/canvas/01-vision.md` §3 — brand presentation.
- Slice 167 doc + decisions log — design source of truth.
- Slice 170 doc — theme picker hydration; defines where the app theme state lives.

## Dependencies

- **#167** (logo redesign + 4-asset swap) — `merged` at `516e043`. This slice depends on the new SVGs slice 167 shipped.
- **#170** (theme picker hydration fix) — `merged` at `2c89eb3`. This slice depends on the theme picker's persisted state being a reliable signal.

## Anti-criteria (P0 — block merge)

- **P0-A1.** Does NOT redesign the logo. SVG markup stays byte-identical to slice 167's output.
- **P0-A2.** Does NOT add a third logo variant (no `logo-system.svg`). Two variants is the contract.
- **P0-A3.** Does NOT swap favicon stack (.ico, apple-touch-icon, manifest icons). Defer.
- **P0-A4.** Does NOT modify Tailwind dark-mode config (`tailwind.config.ts`, `globals.css`). Read-only consumer of the existing theme state.
- **P0-A5.** Regenerated PNGs MUST pass slice 167's STRIDE-I metadata-strip + STRIDE-D size-cap (SVG ≤ 8 KB / PNG ≤ 16 KB gzipped). Engineer verifies via `gzip -c <file> | wc -c`.
- **P0-A6.** Does NOT modify mockups under `Plans/mockups/`. Iteration-1 reference; slice 075 retired it.
- **P0-A7.** Does NOT introduce a `useTheme()` hook contract that diverges from slice 070/170's existing pattern. Reuse whatever slice 170 exposes; do NOT invent parallel theme-state machinery.
- **P0-A8.** Component wrapper MUST be SSR-safe: must NOT throw on the SSR pass (matches slice 170's hydration discipline — `useState(DEFAULT_THEME)` initial; `useEffect` to sync to actual theme).
- **P0-A9.** AC-9 visual-diff Playwright test MUST run under the existing `web/e2e/logo-render.spec.ts` file (extend; do NOT add a new spec file).
- **P0-A10.** Neutral test tokens / no vendor-prefixed strings.

## Skill mix (3-5)

1. **Engineer** — primary; React component + Vitest + Playwright e2e extension + PNG regeneration toolchain
2. **Designer** (not needed for impl; engineer can re-run the slice-167 PNG regen pipeline solo)

## Notes for the implementing agent

**Where slice 167 left things:**

- 4 canonical SVG/PNG assets at `web/public/logo-{light,dark}.{svg,png}` are the "new" Cartographer's Star design (slice 167).
- 2 docs/images/ PNGs are the OLD pre-slice-167 design (file dates 2026-05-15).
- 3 mount sites use `<picture>` + `prefers-color-scheme` — the OS-coupled picker that ignores app theme.
- Slice 170 made theme state persistable to localStorage + read via `useEffect` post-mount. There's a `useTheme()`-equivalent OR the read path the engineer should reuse.

**Recommended workflow at impl time:**

1. Look at slice 170's AppearanceSelector (`web/app/(authed)/settings/page.tsx` ~line 475) for how theme state is read.
2. Build `<ThemeAwareLogo />` that consumes the same source-of-truth + falls back to `prefers-color-scheme` when state is "system."
3. Run the slice-167 PNG regeneration command (documented in `docs/audit-log/167-logo-redesign-decisions.md`) against `web/public/logo-light.svg` → `docs/images/logo-light.png`. Same for dark.
4. Verify byte sizes under gzipped caps.
5. Extend `web/e2e/logo-render.spec.ts` with a contrast-delta assertion.

**Surfaced 2026-05-19 via /idea-to-slice from maintainer report**: "We are using the light colored logo on light colored background now. I cannot see the logo basically at all. I think we need to be using the dark logo we generated. I also noticed we have not updated thinks like the README with the new logo."
