# Slice 360 — decisions log

**Slice:** 360 — A11y light-mode `--muted-foreground` contrast lift
**Type:** JUDGMENT
**Closes:** slice 331 a11y audit finding A11Y-2 (WCAG SC 1.4.3 Contrast (Minimum), AA, severity High)
**Date:** 2026-05-29

---

## D1 — Path chosen: Path A (single-token darken)

The slice presented three remediation paths:

- **Path A** — darken the token. Mechanical; passes AA; no consumer edits.
- **Path B** — per-consumer triage of the 300+ `text-muted-foreground` usages.
- **Path C** — hybrid: darken the token to a mid-value AND add a
  `text-muted-foreground-strong` variant.

**Decision: Path A.**

Reasoning:

1. **The finding is token-scoped, not consumer-scoped.** A11Y-2 is a single
   global token below (well, marginally above — see D2) the AA floor. The
   problem is mechanically dissolved by lifting one token. Path B trades a
   one-line CSS fix for a 300-file audit with brittle per-surface
   `aria-hidden` judgment calls — disproportionate, and P0-360-3 forbids
   widening the lift to other tokens anyway.
2. **Path C introduces a new public surface** (`text-muted-foreground-strong`)
   that consumers must then be taught to choose between. That is a design-system
   decision with its own adoption cost and its own future a11y-audit surface;
   it is not justified by a single sub-AA token. P0-360-2 also discourages
   expanding the token system.
3. **Visual-hierarchy compression is acceptable and small.** The lift moves the
   muted gray from #737373 to #636363 — a perceptually modest darkening that
   keeps it clearly distinct from the full `--foreground` (#252525-equivalent,
   `oklch(0.145 0 0)`). Information hierarchy (muted vs. full) is preserved.

Path A satisfies AC-6 (no code path outside `globals.css` modified) cleanly.

---

## D2 — Chosen value and the contrast math

**Chosen value:** `--muted-foreground: oklch(0.52 0 0)` (light mode only).

### Method

OKLCh values with chroma `0` are achromatic. To get a WCAG contrast ratio that
matches what a browser actually paints, the value is run through the same
pipeline the browser uses:

```
OKLab(L, a=0, b=0)
  -> linear sRGB           (OKLab inverse matrix)
  -> sRGB gamma-encode     (display-referred 0..1)
  -> WCAG linearize        (per WCAG relative-luminance definition)
  -> relative luminance    (0.2126 R + 0.7152 G + 0.0722 B)
contrast = (L_hi + 0.05) / (L_lo + 0.05)
```

The pipeline is validated against the canonical WCAG AA boundary gray:
**#767676 on white = 4.54:1** (the well-known normal-text boundary). The
implementation reproduces 4.54:1, confirming the math.

### Computed ratios against `--background: oklch(1 0 0)` (white)

| OKLCh L         | sRGB hex        | Contrast vs. background | AA (≥4.5:1)? |
| --------------- | --------------- | ----------------------- | ------------ |
| 0.556 (old)     | #737373         | **4.74:1**              | pass (thin)  |
| 0.540           | ~#6f6f6f        | ~4.95:1                 | pass         |
| **0.520 (new)** | **#636363**     | **5.51:1**              | **pass**     |
| 0.500           | #636363/#5f5f5f | 6.00:1                  | pass         |
| 0.480           | #5d5d5d         | 6.54:1                  | pass         |
| 0.450           | #555555         | 7.44:1                  | pass         |

### Note on the slice's "~4.0:1" premise

Slice 331's audit estimated the old token at "roughly 4.0:1" and the slice
brief repeated it. The browser-faithful computation puts the old
`oklch(0.556 0 0)` (#737373) at **4.74:1** — it _marginally_ clears the 4.5:1
floor. The audit estimate was conservative (it likely used a linear-luminance
shortcut or a different oklch→sRGB rounding). This does **not** invalidate the
slice: a 4.74:1 token is a thin, at-risk margin — oklch→sRGB rounding across
browser engines, subpixel antialiasing, and uncalibrated/low-gamma displays can
push the effective contrast under the floor, and the audit correctly flagged it
as the foreground for 300+ information-conveying surfaces. The remediation
intent (a comfortable, defensible margin over 4.5:1) stands.

### Why 0.52 and not a smaller or larger lift

- The slice brief suggested aiming for "a small margin over 4.5:1, e.g.
  ~4.6–5.0:1, so it stays muted" — but that guidance was written against the
  audit's 4.0:1 baseline. Against the true 4.74:1 baseline, a 4.6–5.0:1 target
  would be a negligible (or zero) improvement and would leave the at-risk
  margin essentially intact.
- `oklch(0.52)` = **5.51:1** is the smallest round step that delivers a
  _meaningful_ headroom increase (+0.77 ratio points, ~16% more contrast) while
  remaining unambiguously "muted": it is still 0.375 L lighter than the full
  `--foreground` (0.145), preserving the muted-vs-foreground hierarchy the app
  relies on for visual information layering.
- Larger lifts (0.48 → 6.5:1, 0.45 → 7.4:1) were rejected as over-darkening:
  they start to crowd the perceptual gap between "muted secondary" and "full
  primary" text, which is exactly the visual-hierarchy compression the slice
  warns against under Path A.

  5.51:1 is the chosen balance: robustly clear of the floor, still clearly muted.

---

## D3 — Dark mode untouched (P0-360-1)

The `.dark` block's `--muted-foreground: oklch(0.708 0 0)` on
`--background: oklch(0.145 0 0)` computes to ≈5.4:1 — already AA-compliant per
the audit. It is **not** modified. The regression test asserts the dark-mode
token literal is unchanged so a future edit cannot silently regress it.

---

## D4 — No other token touched (P0-360-3)

Only `--muted-foreground` in the `:root` (light-mode) block changed. The audit's
note about other muted-`*` tokens (`--secondary`, `--accent`) is explicitly out
of scope; if those turn out sub-AA they file as separate slices.

---

## D5 — Test surface: node-env vitest, not Playwright pixel-diff (AC-4)

The slice's AC-4 asks for updated Playwright visual-diff baselines "if any". A
scan of `web/e2e/` found **no `*-snapshots` directories and no committed `.png`
baselines** — the e2e specs assert DOM structure and behavior, not pixel
screenshots (`toHaveScreenshot` is not used). There are therefore no baselines
to regenerate; AC-4 is vacuously satisfied.

In place of a (non-existent) pixel baseline, the contrast guarantee is pinned by
a deterministic unit test (`web/lib/a11y-muted-foreground-contrast.test.ts`):

- reads the two tokens directly out of `app/globals.css` (so it tracks the real
  shipped value, not a hard-coded copy),
- computes the WCAG contrast via the browser-faithful pipeline above,
- asserts ≥4.5:1 (AC-1),
- asserts the token stays muted (lighter than `--foreground` by >0.15 L),
- asserts the dark-mode token literal is unchanged (P0-360-1),
- self-validates its math against #767676 = 4.54:1.

The test lives in `web/lib/`, runs under the existing node-env vitest runner
(no jsdom, no new dependency), and does not introduce any covered _source_
module, so it has no effect on the slice-347 per-file coverage ratchet.

---

## AC verdict

| AC   | Verdict | Evidence                                                          |
| ---- | ------- | ----------------------------------------------------------------- |
| AC-1 | PASS    | New token `oklch(0.52 0 0)` = 5.51:1 ≥ 4.5:1; pinned by vitest    |
| AC-2 | PASS    | Dark-mode token unchanged; asserted by vitest                     |
| AC-3 | PASS    | This log (Path A, D1; value + math, D2)                           |
| AC-4 | PASS    | No Playwright pixel baselines exist; vacuously satisfied (D5)     |
| AC-5 | PASS    | `pre-commit run --all-files` green (see PR)                       |
| AC-6 | PASS    | Only `globals.css` + decisions log + CHANGELOG + new test touched |
