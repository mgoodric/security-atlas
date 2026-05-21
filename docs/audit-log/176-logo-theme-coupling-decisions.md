# Slice 176 — Logo theme coupling + README/docs asset refresh — decisions log

**Slice doc:** [`docs/issues/176-logo-theme-coupling-and-readme-refresh.md`](../issues/176-logo-theme-coupling-and-readme-refresh.md)
**PR:** gh#TBD (filled at PR open)
**Worktree:** `security-atlas-176`
**Branch:** `frontend/176-logo-theme-coupling`
**Type:** AFK · Frontend (theme coupling) + Docs (asset refresh)
**Estimate:** 0.5d

---

## Context

Two bugs surfaced by maintainer report 2026-05-19 and filed as slice 176:

- **Bug A** — the three logo mount sites used `<picture media="(prefers-color-scheme: ...)">`, which keys off the operating-system preference, NOT the application's theme picker (slice 070/170's persisted localStorage value, written as `data-theme` on `<html>`). Operators on OS=dark with explicit app theme "light" were being served `/logo-dark.svg` (near-white ink, `#f8fafc`) onto a light app background → invisible.
- **Bug B** — `docs/images/logo-light.png` + `docs/images/logo-dark.png` were the pre-slice-167 design. Slice 167's anti-criterion P0-A4 limited that slice to `web/public/`; docs/images/ were intentionally out of scope, deferred here.

Both bugs are mechanical. Decisions below cover the load-bearing implementation choices.

---

## D1 — Picker pattern: read `<html data-theme>` via useEffect + MutationObserver

**Decision:** Pattern A from the slice doc's three options (D1-A = Tailwind dark class watcher) was eliminated because slice 170's AppearanceSelector does NOT write a Tailwind `dark` class — it writes a `data-theme` attribute on `<html>` (line 510 of `web/app/(authed)/settings/page.tsx`). Pattern C (React Context) was rejected: the topbar lives under `(authed)/layout.tsx`, the login page lives at `/login` (not inside the authed layout), and the slice 167 mount sites declined a wrapping provider — introducing a Context would touch surfaces this slice cannot afford to widen and would still need a localStorage reader at the provider boundary.

**Chosen:** a hybrid of D1-A and D1-B — the new `<ThemeAwareLogo>` component reads `<html data-theme>` directly via `document.documentElement.getAttribute("data-theme")` (then normalizes via the slice 103 `parseTheme()` import, so the picker inherits the same three-value contract slice 170 owns). It listens for changes via a `MutationObserver(attributes, attributeFilter: ["data-theme"])` so a /settings change triggers an immediate re-render without a page reload. Falls back to `window.matchMedia("(prefers-color-scheme: dark)")` only when the resolved theme is `"system"`.

**Why this satisfies P0-A7** ("Does NOT introduce a `useTheme()` hook contract that diverges from slice 070/170's existing pattern"): the component reuses slice 103's `Theme` type + `parseTheme()` + `DEFAULT_THEME` constants. No new theme-state machinery; the picker is a read-only consumer of the attribute slice 170 writes.

**Why this satisfies P0-A8** (SSR-safe): initial state seeds `appTheme = DEFAULT_THEME` + `prefersDark = false`. `resolveLogoSrc("system", false)` returns `/logo-light.svg` — byte-identical to the slice 075 `<picture>` element's fallback `<img src>` so hydration is silent. The useEffect runs post-mount only; on the SSR pass the component renders deterministically with no DOM access.

## D2 — Component name + colocation: `<ThemeAwareLogo>` at `web/components/shell/`

**Decision:** Named `ThemeAwareLogo`. Lives at `web/components/shell/theme-aware-logo.tsx` next to `topbar.tsx` (the primary mount site). The pure picker function `resolveLogoSrc(appTheme, prefersDark)` lives at `web/lib/theme-aware-logo.ts` so it can be unit-tested in vitest's `node` environment (web has no jsdom / `@testing-library/react` per slice 069 P0-A3); the React component is a thin shell that wires DOM observers and delegates to the helper.

**Alternative considered + rejected:** "Logo" alone (too generic; the slice 075 inline `<picture>` was also conceptually "the logo" and a single-word import would mislead). "BrandMark" (introduces vocabulary the project doesn't use elsewhere). "Logo075" (couples component name to a slice number, anti-pattern).

## D3 — Regenerator command: sharp@0.34 + pngquant (same pipeline slice 167 used)

**Decision:** Use the slice 167 sharp+pngquant pipeline verbatim, swapping output paths from `web/public/logo-{light,dark}.png` to `docs/images/logo-{light,dark}.png`. Alternative rasterizers (`rsvg-convert`, `magick`, `inkscape`) were not on the engineer's machine; sharp is the documented slice 167 toolchain and is the toolchain operators reproducing this slice will already have if they followed slice 167's regen instructions.

**Command (reproducible):**

```bash
# 1. PNG regen via sharp@0.34 (transparent bg, density 600, 256x256).
node -e "
const sharp = require('sharp');
const fs = require('fs');

async function regen(svgPath, pngPath, bg) {
  const svg = fs.readFileSync(svgPath);
  await sharp(svg, { density: 600 })
    .resize(256, 256, { fit: 'contain', background: bg })
    .png({ compressionLevel: 9 })
    .toFile(pngPath);
}

(async () => {
  await regen(
    'web/public/logo-light.svg',
    'docs/images/logo-light.png',
    { r: 250, g: 250, b: 250, alpha: 0 }
  );
  await regen(
    'web/public/logo-dark.svg',
    'docs/images/logo-dark.png',
    { r: 10, g: 10, b: 10, alpha: 0 }
  );
})();
"

# 2. pngquant strip + recompress.
pngquant --force --strip --skip-if-larger --speed 1 --quality 90-100 \
  --output docs/images/logo-light.png docs/images/logo-light.png
pngquant --force --strip --skip-if-larger --speed 1 --quality 90-100 \
  --output docs/images/logo-dark.png  docs/images/logo-dark.png
```

**Observation worth recording:** the regenerated `docs/images/logo-{light,dark}.png` end up byte-identical (SHA-256 matched) to `web/public/logo-{light,dark}.png` — sharp+pngquant are deterministic for the same SVG inputs. This is desirable: it means the two paths can never visually drift if both are regenerated from the same SVG source, which makes future slice 167 / 176 follow-ons cheap. Cross-path sha audit:

```
283205e6315e5bec3248c6b802a800ad06f3aba96e778d8b9b76f8f01c7fad92  docs/images/logo-light.png
283205e6315e5bec3248c6b802a800ad06f3aba96e778d8b9b76f8f01c7fad92  web/public/logo-light.png
39a2b833703c36e44698ea14af885bf8f2ea6616ad93161422202d87d2bfaf10  docs/images/logo-dark.png
39a2b833703c36e44698ea14af885bf8f2ea6616ad93161422202d87d2bfaf10  web/public/logo-dark.png
```

## D4 — AC-7 audit: `web/app/layout.tsx` does not render the logo `<img>`

**Decision:** Document that the RootLayout's only logo-touching surface is the `Metadata` export (`icons`, `openGraph.images`, `twitter.images`). None of those are theme-coupled at runtime — the favicon stack is a single set served to every theme, OG/Twitter scrapers freeze a single variant at unfurl time. So AC-7 is satisfied by an in-file comment block explaining the audit result rather than by a swap (there is nothing to swap). The two mount sites that DO render an `<img>` — `web/components/shell/topbar.tsx` (AC-5) and `web/app/login/page.tsx` (AC-6) — were updated to use `<ThemeAwareLogo>`.

---

## Final asset sizes (AC-12 + P0-A5 inherited from slice 167)

| Asset                        | Raw    | Gzipped | P0-A5 cap (gzipped) | Status           |
| ---------------------------- | ------ | ------- | ------------------- | ---------------- |
| `docs/images/logo-light.png` | 4697 B | 4638 B  | ≤ 16384 B (16 KB)   | ✓ 71.7% headroom |
| `docs/images/logo-dark.png`  | 4847 B | 4746 B  | ≤ 16384 B (16 KB)   | ✓ 71.0% headroom |

(Matches slice 167's `web/public/` numbers byte-for-byte — same SVG sources, same sharp+pngquant pipeline, deterministic output.)

## STRIDE-I metadata audit (AC-13 + P0-A5)

`strings docs/images/logo-{light,dark}.png | grep -iE "gmoney|mattgoodrich|/users/|tEXt|zTXt|iTXt|sharp|libpng|sketch|figma|adobe|illustrator|inkscape"` returns zero matches. `pngquant --strip` removed all ancillary text chunks; the regenerated files carry no personal identifiers, no source-path leakage, and no tooling identifiers.

---

## P0 anti-criteria audit (all 10)

| Anti-criterion                                                  | Verdict | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| --------------------------------------------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **P0-A1.** No logo redesign                                     | ✓       | `git diff main...HEAD -- web/public/logo-light.svg web/public/logo-dark.svg` returns empty. SVG markup is byte-identical to slice 167's output.                                                                                                                                                                                                                                                                                                                                                                                              |
| **P0-A2.** No third logo variant                                | ✓       | `ls web/public/logo*` returns exactly four paths (light + dark × svg + png). No `logo-system.svg`.                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| **P0-A3.** No favicon stack swap                                | ✓       | `git diff main...HEAD -- web/public/favicon.ico web/public/apple-touch-icon.png web/public/icon-192.png web/public/icon-512.png web/app/manifest.* web/app/icon.* web/app/apple-icon.*` returns empty.                                                                                                                                                                                                                                                                                                                                       |
| **P0-A4.** No Tailwind dark-mode config change                  | ✓       | `git diff main...HEAD -- web/tailwind.config.ts web/tailwind.config.* web/app/globals.css` returns empty. `<ThemeAwareLogo>` reads `<html data-theme>` directly (the attribute slice 170 already writes), not a Tailwind `dark:` class.                                                                                                                                                                                                                                                                                                      |
| **P0-A5.** Regenerated PNGs honor slice 167 STRIDE-I + STRIDE-D | ✓       | (a) gzipped sizes 4638 + 4746 — both well under the 16 KB cap (table above). (b) Metadata audit clean: zero matches for personal identifiers / tooling tags in either PNG.                                                                                                                                                                                                                                                                                                                                                                   |
| **P0-A6.** No mockups touched                                   | ✓       | `git diff main...HEAD -- Plans/mockups/` returns empty.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| **P0-A7.** No divergent `useTheme()` machinery                  | ✓       | Component imports slice 103's `Theme` type + `parseTheme()` + `DEFAULT_THEME` from `@/app/(authed)/settings/theme`. No new theme-state contract.                                                                                                                                                                                                                                                                                                                                                                                             |
| **P0-A8.** SSR-safe                                             | ✓       | Initial state: `useState<Theme>(DEFAULT_THEME)` + `useState<boolean>(false)`. Both reads are inside `useEffect(() => {…}, [])`. SSR render resolves to `/logo-light.svg`, byte-identical to the prior `<picture>` element's fallback `<img src>` — zero hydration mismatch surface.                                                                                                                                                                                                                                                          |
| **P0-A9.** Extend existing `logo-render.spec.ts`                | ✓       | `git diff main...HEAD -- web/e2e/` shows only `web/e2e/logo-render.spec.ts` modified (two existing tests rewritten to assert against the new `<img data-testid="theme-aware-logo">` element; one new test "logo variant follows app theme (data-theme on `<html>`)" added inside the existing `describe` block). No new spec file. `web/e2e/logo-render-production-build.spec.ts` was reviewed and required no changes — its assertions key on the rendered HTML's `/logo-light.svg` reference + asset 200 status, both of which still hold. |
| **P0-A10.** Neutral test tokens                                 | ✓       | Vitest + Playwright assertion strings audited: no vendor-prefixed tokens; only the canonical `/logo-{light,dark}.svg` asset paths, the slice 075 alt-text `"security-atlas"`, and the slice 176 `data-testid="theme-aware-logo"`.                                                                                                                                                                                                                                                                                                            |

---

## Wall-clock

~30 minutes engineer time end-to-end (read slice doc + scan slice 170 source → write helper + tests + component → swap two mount sites + document layout audit → regen 2 PNGs + verify → extend Playwright spec → decisions log + CHANGELOG → push + PR).

The fix was mechanical once D1 was settled. The non-obvious call was that slice 170 writes `data-theme` (NOT a Tailwind `dark:` class) — that drove D1's choice of MutationObserver + attribute reading over the slice-doc-recommended Tailwind class watcher.
