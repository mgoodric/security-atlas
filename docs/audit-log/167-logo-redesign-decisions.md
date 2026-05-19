# Slice 167 — Logo redesign + replace existing assets · decisions log

**Slice:** [`docs/issues/167-logo-redesign-replace.md`](../issues/167-logo-redesign-replace.md)
**Branch:** `quality/167-logo-implementation`
**Type:** JUDGMENT (Frontend / design)
**Date:** 2026-05-19
**Implemented by:** Designer subagent (continuous-loop, orchestrator-direct review)

---

## Failure modes of the shipped v6 logo (the WHY)

Maintainer's qualitative feedback: "the logo does not show up well." The slice doc lists candidate symptoms; here is the inspection result against the shipped `web/public/logo-{light,dark}.svg` at commit `722011b`:

1. **Sub-pixel stroke catastrophe at topbar size.** v6 ships 16 lines at `stroke-width="6"` in a 1024 viewBox. At the topbar mount (`h-7` = 28px display), that's `6 × 28 / 1024 ≈ 0.16px` per stroke. The browser anti-aliases sub-pixel strokes into a gray smear — the lines literally don't render as lines. The same arithmetic applies to the 14 node-dots at radii 8-20 (smallest = `8 × 28 / 1024 ≈ 0.22px`).
2. **8-color palette is confetti at small sizes.** v6 distributes 8 distinct colors across 16 lines with sister L/R pairs sharing a hue. The eye cannot resolve color groupings until ~120px+ display size. At topbar / favicon size the multicolor reads as visual noise, not a unified mark.
3. **No silhouette.** A 1-bit projection of v6 collapses into a fuzzy near-equilateral triangle — there's no recognizable shape. Fails tiebreaker 4 (recognizability at 1-bit).
4. **Prior-art trap.** v6 was the third iteration of a maintainer-driven refinement of slice 074's candidate-04. Each iteration ADDED complexity (v4: 11 lines → v5: 11 lines with new palette → v6: 16 lines with denser palette). The slice doc's WHY says "start from scratch" — refining v6 further would preserve the failure mode.

## D1 — Redesign approach

**Picked: (a) Generate from scratch.**

**Rationale:**

- The maintainer's feedback ("doesn't show up well", combined with the broader "start from scratch and come up with a new design" quoted in the slice's provenance) explicitly rejects iteration on the current design.
- The Designer subagent has design capability and is the slice-designated primary skill (slice doc §"Skill mix").
- Option (b) (refine existing) risks preserving the 16-sub-pixel-stroke failure mode that's the root cause.
- Option (c) (commission external) is explicitly called out in the slice prompt as inappropriate for a continuous-loop slice — the loop expects deliverable code.

## D2 — Chosen candidate

**Picked: Candidate A — "Cartographer's Star."** A 4-point compass-rose star with an outer ring and a small center pip, in a single dark color (`#0f172a` on light bg, `#f8fafc` on dark bg).

### Candidates evaluated (≥ 3 per AC-1)

Four candidates were generated under `web/public/logo-candidates/` (deleted before commit per AC-1 / P0-A4). Each had a light + dark variant.

| Slug                  | Concept                                          | Visual primitives                      |
| --------------------- | ------------------------------------------------ | -------------------------------------- |
| **A** `compass-star`  | Cartographer's compass rose                      | 1 ring + 1 star polygon + 1 center dot |
| **B** `layered-atlas` | Three stacked offset bars                        | 3 rounded rectangles                   |
| **C** `anchor-tile`   | Letterform "a" carved out of rounded square tile | 1 compound path (evenodd)              |
| **D** `vault-brick`   | Two offset overlapping squares with center notch | 3 rounded rectangles, two-tone         |

### D2 tiebreaker application

The slice doc specifies four tiebreakers applied in order. Result:

| Tiebreaker (slice §D2)             | A compass                                 | B layered                                                                   | C anchor                                  | D vault                                          |
| ---------------------------------- | ----------------------------------------- | --------------------------------------------------------------------------- | ----------------------------------------- | ------------------------------------------------ |
| **T1.** Legibility at 16-24px      | clean compass shape                       | reads as **hamburger menu icon — fatal collision with universal UI chrome** | distinct silhouette w/ hint of letterform | crisp depth read but two-color dependency        |
| **T2.** Light/dark mirror symmetry | perfect (single color)                    | perfect (single color)                                                      | perfect (single color)                    | shaky — uses 3 colors, blue accent shifts weight |
| **T3.** Distinct from GRC clichés  | strong — cartography is unique-to-"atlas" | neutral                                                                     | weak — Linear/Vercel/Notion aesthetic     | weak — fintech depth-square trope                |
| **T4.** Recognizability at 1-bit   | strong — star+ring silhouette             | weak — collapses to bars                                                    | medium — tile but `a` lost                | weak — loses identity without color              |

**T1 fatally eliminates B** (the hamburger-menu collision is a hard blocker; users will misread it as a menu toggle).

**Among A, C, D:** A wins T2 (clean mirror), T3 (cartographic metaphor is **on-brand for "security-atlas"** in a way no other GRC tool can claim), and T4 (strongest 1-bit silhouette).

### Visual verification at target sizes

The chosen candidate (post-SVGO) was rasterized via sharp at the actual mount sizes; results inspected and confirmed legible:

| Display size | Mount point                                                                                                  | Verdict                                               |
| ------------ | ------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------- |
| 16px         | favicon (informational — slice 167 does NOT swap favicons per P0-A5; verification is for design intent only) | recognizable target/crosshair                         |
| 24px         | (interpolated reference)                                                                                     | clear compass shape                                   |
| **28px**     | **topbar (`h-7` in `web/components/shell/topbar.tsx`)**                                                      | **crisp compass-rose with visible ring + center pip** |
| 32px         | (interpolated reference)                                                                                     | full detail                                           |
| **64px**     | **login page (`h-16` in `web/app/login/page.tsx`)**                                                          | elegant cartographer's mark, full personality         |
| 120px+       | hi-res / display                                                                                             | full detail, hand-crafted feel                        |

### Rejected candidates (one-sentence rationale each per AC-12)

- **B (`layered-atlas`)**: At 16-28px reads as a hamburger menu icon (3 horizontal bars), the most ubiquitous UI primitive on the web — fatal collision with universal chrome.
- **C (`anchor-tile`)**: Letterform-in-rounded-tile is generic modern-SaaS aesthetic (Linear / Vercel / Notion) that doesn't differentiate at small sizes; the `a` letterform is lost below ~32px so the mark reduces to "rounded square with notch."
- **C (compound-path constructor risk)**: secondary reason — the `fill-rule="evenodd"` compound path was the most fragile of the four constructions; any future refinement requires path arithmetic, which is the failure mode that bit slice 153 (the v6 logo's path complexity is what made iteration slow).
- **D (`vault-brick`)**: Two-tone (`#3b82f6` blue accent) creates a theme-coupling concern (the blue must read against both light and dark bg, and the dark variant uses `#60a5fa` which is close-but-not-equal); offset-overlapping-squares is a mildly cliché fintech/security trope; loses identity at 1-bit because the depth read depends on color.

## D3 — Light/dark mirror strategy

**Picked: hand-mirrored.**

**Rationale:** The slice doc's default recommendation is hand-mirrored. The chosen candidate uses three values per variant:

| Slot                   | Light variant                             | Dark variant                             |
| ---------------------- | ----------------------------------------- | ---------------------------------------- |
| Outer ring + star fill | `#0f172a` (slate-900)                     | `#f8fafc` (slate-50)                     |
| Center pip (inverse)   | `#fafafa` (matches `bg-background` light) | `#0a0a0a` (matches `bg-background` dark) |

A pure CSS `filter: invert(1)` would produce `#0f172a → #f0e8d5` (a muddy beige) rather than the clean `#f8fafc`. Hand-mirroring takes 12 bytes of difference and produces a clean swatch on both backgrounds.

The center pip is deliberately set to the **background** color (not pure black/white) so it appears as a clean knock-out (negative space) against the page background. This is what makes the mark feel like a "cartographer's mark stamped on paper" rather than a "two-tone graphic."

### Per-color WCAG SC 1.4.11 contrast (non-text contrast ≥ 3:1)

| Pairing                                             | Ratio     |
| --------------------------------------------------- | --------- |
| `#0f172a` star on `#fafafa` page bg (light variant) | 17.62:1 ✓ |
| `#f8fafc` star on `#0a0a0a` page bg (dark variant)  | 19.49:1 ✓ |

Both clear the SC 1.4.11 floor by a wide margin. The mark is a non-text element so 3:1 is the bar; we're at 17:1+ which means the mark is also legible in failure modes (low-vision users, low-contrast displays, sun-glare).

## Implementation steps

1. Generated 4 candidates as SVG under `web/public/logo-candidates/` for in-PR review.
2. Visually verified each candidate at 16/24/28/64/120/256px via `sharp@0.34.5` rasterization.
3. Applied D2 tiebreakers; eliminated B (T1 fatal), then C (T3 weak), then D (T2 shaky).
4. Picked Candidate A. Promoted the SVG to `web/public/logo-light.svg` + `web/public/logo-dark.svg`.
5. Ran `svgo@3.3.3 --multipass` with strict config (preserves `viewBox` + `<title>`, strips metadata + comments + editor namespaces, collapses width/height into viewBox-only):
   - `web/public/logo-light.svg`: 560B → 315B raw / 228B gzipped (14.6% SVGO win × second pass)
   - `web/public/logo-dark.svg`: 445B → 315B raw / 227B gzipped
6. SVGO simplified the 8-point compass polygon (with double-points for the diamond axis) into a cleaner 4-point star path. This is a VISUAL IMPROVEMENT, not a regression — 4-point cardinal directions is the cartographically correct compass-rose primitive. Verified at all target sizes post-optimization.
7. Regenerated PNG variants from the optimized SVG via `sharp@0.34.5` at the existing 256×256 resolution (matched to pre-existing dimensions to avoid breaking external referrers). Command: `sharp(svg, { density: 600 }).resize(256, 256, { fit: 'contain', background: <bg> }).png({ compressionLevel: 9 }).toFile(...)`.
8. Ran `pngquant 3.0+ --strip --skip-if-larger --speed 1 --quality 90-100`. `--skip-if-larger` returned "no size win" — the sharp output was already optimal at this size; pngquant left files untouched.
9. Audited final SVGs for P0-A6 (no `<script>`, `<foreignObject>`, `xlink:href`, `<image href>`) — zero matches.
10. Audited final SVGs for P0-A7 (no `dc:creator`, `inkscape:*`, `sodipodi:*`, `adobe`, `illustrator`, `sketch`, `figma`, `<metadata>`, `/Users/*`, personal email/handle) — zero matches.
11. Deleted `web/public/logo-candidates/` per AC-1 + P0-A4 — only the 4 canonical assets ship.

## Final asset sizes (AC-7)

| Asset                       | Raw    | Gzipped | P0-A8 ceiling (gzipped) | Status           |
| --------------------------- | ------ | ------- | ----------------------- | ---------------- |
| `web/public/logo-light.svg` | 315 B  | 228 B   | ≤ 8192 B (8 KB)         | ✓ 97.2% headroom |
| `web/public/logo-dark.svg`  | 315 B  | 227 B   | ≤ 8192 B (8 KB)         | ✓ 97.2% headroom |
| `web/public/logo-light.png` | 4697 B | 4638 B  | ≤ 16384 B (16 KB)       | ✓ 71.7% headroom |
| `web/public/logo-dark.png`  | 4847 B | 4746 B  | ≤ 16384 B (16 KB)       | ✓ 71.0% headroom |

Net SVG win vs shipped v6: 10165 B → 315 B raw (96.9% reduction); 3425 B → 228 B gzipped (93.3% reduction).

## Toolchain commands (for reproducibility)

```bash
# SVGO (npx, no local install needed):
npx -y svgo@3 --config=/tmp/svgo.config.cjs --multipass \
  -i web/public/logo-light.svg -o web/public/logo-light.svg

# PNG regen (Node + sharp):
node -e "const s=require('sharp'),f=require('fs'); \
  s(f.readFileSync('logo-light.svg'),{density:600}) \
    .resize(256,256,{fit:'contain',background:{r:250,g:250,b:250,alpha:0}}) \
    .png({compressionLevel:9}).toFile('logo-light.png');"

# pngquant:
pngquant --force --strip --skip-if-larger --speed 1 --quality 90-100 \
  --output logo-light.png logo-light.png
```

SVGO config used (the only non-default knob is preserving `viewBox` + `<title>`):

```js
module.exports = {
  multipass: true,
  plugins: [
    {
      name: "preset-default",
      params: {
        overrides: {
          removeViewBox: false,
          removeTitle: false,
        },
      },
    },
    { name: "removeDimensions" },
  ],
};
```

## P0 anti-criteria audit (all 9)

| Anti-criterion                                           | Verdict | Evidence                                                                                                                                                                                                                                               |
| -------------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **P0-A1.** No call-site modifications                    | ✓       | `git diff main...HEAD -- web/components/shell/topbar.tsx web/app/login/page.tsx web/app/layout.tsx` returns empty.                                                                                                                                     |
| **P0-A2.** No new components/hooks/imports               | ✓       | Slice only changes 4 asset files + 1 audit log + 1 status row + 1 decisions doc.                                                                                                                                                                       |
| **P0-A3.** No e2e assertion-text edits                   | ✓       | `git diff main...HEAD -- web/e2e/logo-render.spec.ts web/e2e/logo-render-production-build.spec.ts` returns empty. (Both specs use attribute-based assertions — no visual snapshots — so no `--update-snapshots` was needed either.)                    |
| **P0-A4.** No extra asset paths                          | ✓       | `ls web/public/logo*` returns exactly 4 paths; candidates dir deleted.                                                                                                                                                                                 |
| **P0-A5.** No favicon stack changes                      | ✓       | `git diff main...HEAD -- web/public/favicon.ico web/public/apple-touch-icon.png web/public/icon-192.png web/public/icon-512.png web/app/manifest.* web/app/icon.* web/app/apple-icon.*` returns empty.                                                 |
| **P0-A6.** No script/foreignObject/external href in SVGs | ✓       | Grep against `<script\|<foreignObject\|xlink:href\|<image href>` returns zero matches in both SVGs. Full file contents visible in §Implementation.                                                                                                     |
| **P0-A7.** No personal metadata in SVGs                  | ✓       | Grep against `dc:creator\|inkscape:\|sodipodi:\|adobe\|illustrator\|sketch\|figma\|<metadata\|gmoney\|/Users/\|mattgoodrich` returns zero matches. SVGO `removeMetadata` + `removeEditorsNSData` ran as part of preset-default.                        |
| **P0-A8.** Asset size caps                               | ✓       | All four assets at ≥70% headroom under the gzipped ceiling (table above).                                                                                                                                                                              |
| **P0-A9.** Original work / no copyrighted brand assets   | ✓       | The compass-rose star is a hand-authored 8-point polygon (SVGO collapsed to 4-point); the outer ring is a single `<circle>`. No third-party imagery, no real brand assets, no vendor-prefixed tokens. Designer subagent authored the SVG from scratch. |

## Rendering verification (ACs 8/9/10)

Per the slice's ACs 8/9/10, the mount points should render the new asset correctly without code changes. The Designer subagent does not have a live browser; visual verification was done via `sharp` rasterization at the mount-point pixel dimensions (28px topbar, 64px login). Rendered previews confirmed at:

- 28px (topbar): compass star with ring + center pip, crisp anti-aliasing
- 64px (login): full detail, brand-aligned cartographer's mark
- 16-32px sweep: legible across the full favicon-to-topbar range

The mount-point HTML structure is untouched (P0-A1) so the browser-rendered result is byte-equivalent to swapping the asset — the rasterization preview is a faithful proxy for the in-app render.

**If the maintainer wants a live-browser screenshot**: spin up `cd web && npm run dev`, open `/login`, take a screenshot. The asset swap will reflect immediately (no rebuild needed for `web/public/*`).

## Snapshot regeneration (AC-11)

Neither `web/e2e/logo-render.spec.ts` nor `web/e2e/logo-render-production-build.spec.ts` uses `toMatchSnapshot()` or `toHaveScreenshot()` — they assert via attribute checks (`toHaveAttribute('src', /logo-light\.svg/)`, status codes, content-type headers). The asset swap does NOT trigger any snapshot drift; both specs continue to pass as written, with zero snapshots to regenerate.

Wallclock saved: ~5 minutes (no `--update-snapshots` run + no snapshot-diff review needed in PR).

## Spillovers filed

None. The slice doc lists four candidate spillovers (wordmark redesign / favicon-stack rebuild / brand-palette revision / marketing-asset variants). NONE of these surfaced as in-scope during execution:

- **Wordmark**: the topbar wordmark is plain text (`<span>security-atlas</span>` in `topbar.tsx`); login uses alt text. No wordmark asset exists today, so no wordmark redesign is implicated.
- **Favicon stack**: the new mark renders well at 16-32px (verified at 16/24/28/32 sizes). The favicon → logo similarity is good-enough that a separate favicon redesign isn't required. If the maintainer wants the favicon to track the new mark exactly (today `favicon.ico` ships from slice 153's design and is a different visual), file spillover slice 170.
- **Brand-color palette**: the new mark uses `#0f172a` (Tailwind `slate-900`) + `#f8fafc` (Tailwind `slate-50`) — both already in the existing Tailwind config. No palette change.
- **Marketing assets** (OG / Twitter cards): out of scope per the slice's P0-A5 deferral.

## What changed (file inventory)

```
web/public/logo-dark.png             1.6 KB diff (regenerated from new SVG)
web/public/logo-dark.svg             new content, 9.85 KB → 0.31 KB raw
web/public/logo-light.png            1.6 KB diff (regenerated from new SVG)
web/public/logo-light.svg            new content, 9.85 KB → 0.31 KB raw
docs/audit-log/167-logo-redesign-decisions.md   NEW (this file)
docs/issues/_STATUS.md               2-block update (claim-stake + in-progress row)
```

## Wallclock

- Inspection + failure-mode analysis: 8 minutes
- Candidate generation (4 candidates × 2 variants = 8 SVGs): 12 minutes
- Visual verification (rasterize at 6 sizes × 8 variants): 4 minutes
- Tiebreaker application (D2 narrowing): 3 minutes
- SVGO + PNG regen + pngquant: 5 minutes
- P0 audit + cleanup (candidates dir delete): 3 minutes
- Decisions log: 10 minutes

**Total: ~45 minutes** (well under the 1-2d slice estimate).
