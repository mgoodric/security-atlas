# 362 — A11y in-progress audit pill dark-mode contrast

**Cluster:** Frontend / a11y
**Estimate:** 0.25d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 331's a11y audit
(`docs/audits/331-a11y-wcag-audit.md` finding A11Y-4, severity
High) surfaced that the in-progress audit pill at
`web/components/shell/in-progress-audit-pill.tsx:90-97` —

```tsx
<div className="... bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-900 rounded-full">
  <span className="w-1.5 h-1.5 bg-amber-500 rounded-full animate-pulse" />
  <span className="text-xs font-medium text-amber-800 dark:text-amber-300">
    {pick.name} in progress
  </span>
</div>
```

— renders `text-amber-300` on `bg-amber-950/40` in dark mode.
Tailwind's `amber-300` is roughly `oklch(0.85 0.17 88)`; against
the `amber-950/40` background (effectively the foreground/dark
background blended with 40% alpha), the contrast ratio computes
to roughly **3.2:1** — BELOW the WCAG SC 1.4.3 AA floor of
**4.5:1** for normal-size text. The text class is `text-xs` —
qualifies as "normal" not "large."

Light mode is OK: `text-amber-800` (~`oklch(0.45 0.13 75)`) on
`bg-amber-50` (~`oklch(0.97 0.02 90)`) computes to roughly
**7.1:1** — well above AA.

The pill is the only "audit in progress" wayfinding affordance on
every authed page (slice 213). Mis-perceiving it in dark mode is a
load-bearing miss.

### What ships

Two viable lifts; engineer chooses based on visual judgment:

**Path A — Lighten the text.** Replace `dark:text-amber-300`
with `dark:text-amber-200` (~`oklch(0.92 0.10 88)`); against the
same bg-amber-950/40, contrast computes to roughly 5.3:1 — passes
AA.

**Path B — Darken the background.** Replace
`dark:bg-amber-950/40` with `dark:bg-amber-900` (no alpha); text
stays `amber-300`; contrast computes to roughly 5.7:1 — passes
AA.

Pick one. Path A is marginally less visual change (text shifts
brighter; background unchanged). Decisions log records the
choice + measured value.

### Why this matters

Dark-mode users are a large fraction of the audience (developers
default to dark; the slice 203 + slice 170 effort exists to
support them). A pill whose text fades into background in dark
mode is a directly-visible barrier.

## Threat model

CSS-only change. STRIDE pass:

- **S / T / R / D / E:** No surface changes.
- **I:** None.

## Acceptance criteria

- [ ] **AC-1.** Dark-mode pill renders with contrast ratio
      ≥4.5:1. Measured value documented in decisions log.
- [ ] **AC-2.** Light-mode pill unchanged (already passes).
- [ ] **AC-3.** `aria-label` + `title` semantics unchanged.
- [ ] **AC-4.** Existing component-level tests pass; manual
      visual-eyeball pass on dark mode confirms readability.
- [ ] **AC-5.** Decisions log records Path A vs B + measured
      contrast.
- [ ] **AC-6.** `pre-commit run --all-files` passes.

## Anti-criteria (P0 — block merge)

- **P0-362-1.** Does NOT remove the `animate-pulse` on the dot
  (that's separately tracked under A11Y-10 / motion bundle).
- **P0-362-2.** Does NOT widen to other amber-/yellow-tinted pills
  in the codebase without measuring them; file separate
  slices for any other failures discovered.
- **P0-362-3.** Does NOT change the pill's role or aria-label.

## Dependencies

- **#331** (a11y audit) — `merged` (closing this slice).
- **#213** (header chrome parity gap) — `merged`. Component
  origin.

## Notes

The `animate-pulse` on the dot is separately flagged in slice 331
finding A11Y-10 (Medium · `prefers-reduced-motion` bundle); not in
scope here.
