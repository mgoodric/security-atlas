# Slice 362 — A11y in-progress audit pill dark-mode contrast — decisions log

**Parent slice:** 331 (`docs/audit-log/331-a11y-wcag-audit-decisions.md`) finding **A11Y-4** (severity High)
**Branch:** `frontend/362-a11y-audit-pill-dark-contrast`
**Type:** JUDGMENT (per `Plans/prompts/04-per-slice-template.md` slice types)
**Date:** 2026-05-28

This slice closes the High-severity finding A11Y-4 from slice 331's
WCAG audit: the in-progress audit pill at
`web/components/shell/in-progress-audit-pill.tsx` renders
`text-amber-300` on `bg-amber-950/40` in dark mode and was flagged
as failing the WCAG 1.4.3 AA contrast floor of 4.5:1 for normal text.

This log captures the build-time judgment calls.

---

## D1 — Path chosen + measured contrast ratio

**Decision:** **Path A** — replace `dark:text-amber-300` with
`dark:text-amber-200` on the inner `<span>` at line 95. Background
classes unchanged.

**Rationale:**

- The `bg-amber-50 dark:bg-amber-950/40` + `border-amber-200
dark:border-amber-900` surface pattern is the established dark-mode
  language for amber-tinted advisory chrome. Lightening the text
  preserves that language with the smallest perceptual delta. Dropping
  the `/40` alpha on the background (Path B) would shift the pill's
  visual weight in dark mode in a way that is gratuitous given Path A
  already satisfies AA.
- Both paths exceed the WCAG AA floor; Path A is the lower-disturbance
  choice (per the slice doc's framing).

**Measured contrast — Path A.** The text is `text-amber-200` over the
composited background (`bg-amber-950` at 40% alpha over the dark shell
`--background` token `oklch(0.145 0 0)` ≈ `#0a0a0a`).

Computation method: WCAG 2.1 relative-luminance formula applied to
linear-light sRGB values. Alpha compositing performed in linear-light
space (the WCAG-correct space for compositing). Two cross-checks were
run to defend the number:

| Method (color source)                                                                                                                         | Composited bg    | Contrast ratio |
| --------------------------------------------------------------------------------------------------------------------------------------------- | ---------------- | -------------- |
| Tailwind 4 oklch native (`amber-200` = `oklch(0.962 0.059 95.617)`, `amber-950` = `oklch(0.279 0.077 45.635)`, shell bg = `oklch(0.145 0 0)`) | `#2d1107`        | **15.77:1**    |
| Tailwind 3 sRGB hex sanity-check (`amber-200` = `#fde68a`, `amber-950` = `#451a03`, shell bg = `#0a0a0a`)                                     | (linear-blended) | **14.08:1**    |

Both numbers are **well above the WCAG AA floor of 4.5:1**; the slice
doc's estimated value of 5.3:1 was a conservative under-estimate.

**Baseline cross-check (recorded for posterity, not action).** Slice
331 estimated the pre-fix baseline (`text-amber-300` on the same
composited bg) at roughly **3.2:1**. Using the same two methods the
build computes the baseline at **14.08:1** (Tailwind 4 oklch) or
**12.16:1** (Tailwind 3 hex) — both already passing AA. The slice
331 estimate appears to have been a back-of-envelope number that did
not account for the alpha compositing over a true near-black dark
shell background; the actual pre-fix pill was already AA-compliant.

This finding does NOT change the disposition of slice 362. The user
brief explicitly anticipated the slice estimate might be wrong and
required Path A or B to ship regardless. Path A still produces an
objective contrast lift (+1.69:1 in Tailwind 4 native math) which is
defense-in-depth for any future shell-bg token shift that would
shrink margin. The recorded action is therefore: ship Path A, record
the measured 15.77:1, and surface the baseline-finding in the PR body
so the maintainer can decide whether slice 331's finding A11Y-4 should
be re-graded post-merge.

## D2 — Light-mode pill left untouched (AC-2)

**Decision:** No change to the light-mode pill classes
(`bg-amber-50` + `text-amber-800`).

**Rationale:** The slice doc records the light-mode contrast at
roughly 7.1:1; the build's sanity-check using the published hex
values measures 6.84:1 — both well above the AA floor of 4.5:1 and
above the AAA floor of 7.0:1 (the 6.84 falls a hair short of AAA but
the slice doc's 7.1:1 number was rounded up; the slice doc's target
is AA not AAA and AC-2 says "unchanged" not "lift further").

## D3 — Scope discipline (P0-362-2)

**Decision:** Did NOT widen the change to any other amber/yellow-
tinted surfaces in `web/components/`, even adjacent ones, per
anti-criterion P0-362-2.

**Adjacent amber/yellow surfaces visible in the same component tree
(noted for future audit, not touched here):** none surfaced in the
file under change. A grep across `web/components/` for other
`dark:bg-amber-*` or `dark:text-amber-*` patterns is appropriate for
a follow-on audit slice; deferred per P0-362-2.

## D4 — Semantics + motion not touched

**Decision:** `aria-label`, `title`, the `<span>` with
`animate-pulse`, and the pill's role-equivalent semantics were all
left bit-identical to pre-change (AC-3, P0-362-1).
