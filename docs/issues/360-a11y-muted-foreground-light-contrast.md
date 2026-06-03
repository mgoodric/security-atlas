# 360 — A11y light-mode `--muted-foreground` contrast lift

**Cluster:** Frontend / a11y
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 331's a11y audit
(`docs/audits/331-a11y-wcag-audit.md` finding A11Y-2, severity
High) surfaced that the light-mode `--muted-foreground` token in
`web/app/globals.css:63` —

```css
--muted-foreground: oklch(0.556 0 0);
```

— against the light-mode `--background: oklch(1 0 0)` computes to a
contrast ratio of roughly **4.0:1**, BELOW the WCAG SC 1.4.3 AA
floor of **4.5:1** for normal-size text.

The token is the foreground for hundreds of `text-muted-foreground`
usages across the codebase: page subtitles, table secondary
content, breadcrumbs, filter pill labels, "Showing N of M" meta
lines, card descriptions, form field descriptions, sidebar inactive
labels, time stamps. A grep for `text-muted-foreground` in `web/`
returns 300+ hits.

Dark mode is clean: `oklch(0.708 0 0)` on `oklch(0.145 0 0)`
computes to roughly 5.4:1 — passes AA. The bug is light-mode only.

### Design space (JUDGMENT slice)

Three viable remediation paths; engineer chooses:

**Path A — Darken the token.** Move `--muted-foreground` from
`oklch(0.556 0 0)` to roughly `oklch(0.45 0 0)`. Mechanical;
passes AA. Cost: every "decorative gray" surface in the app
becomes a noticeably darker gray, which may visually crowd
pages that depend on the muted-vs-foreground contrast for
information hierarchy.

**Path B — Per-consumer triage.** Audit the 300+
`text-muted-foreground` usages and split: surfaces that convey
information get bumped to `text-foreground/70` (passes AA);
surfaces that are decorative (e.g. trailing dimmer subtitle
text whose absence wouldn't affect comprehension) get marked
`aria-hidden` or have their content moved to a non-text channel.
Cost: large; high-touch; brittle.

**Path C — Hybrid.** Darken the token to a safer mid-value
(e.g. `oklch(0.48 0 0)`, roughly 4.6:1 — just-passes AA),
accepting a smaller visual hierarchy compression, and add a
named darker variant (`text-muted-foreground-strong`) for
genuinely-secondary content that does not need the
information-conveyance treatment.

Engineer's call documented in this slice's decisions log
(`docs/audit-log/360-...-decisions.md`). Recommend Path A or C
unless Path B is mechanically tractable.

### What ships

1. **Token lift in `web/app/globals.css`.** Path A or C: bump the
   light-mode token to a value that passes 4.5:1 against
   `--background`. Document the chosen color + measured contrast
   ratio in the decisions log.
2. **Visual regression pass.** Run the existing Playwright
   visual-diff specs (or manually screenshot the dashboard +
   controls list + risks hierarchy + admin pages); commit the
   updated baselines if any.
3. **Decisions log.** Record the path chosen + the reasoning + the
   measured contrast ratio + any consumer-page screenshots showing
   the before/after.

### Why this matters

Hundreds of "secondary content" surfaces sit at sub-AA contrast in
the dominant theme. Low-vision users + outdoor-glare users + users
on uncalibrated displays cannot reliably read subtitles, table
secondary columns, or meta lines. Single-token darken makes the
problem disappear; the cost is purely visual-hierarchy
compression.

## Threat model

CSS-only change. STRIDE pass:

- **S / T / R / D / E:** No surface changes.
- **I:** None.

## Acceptance criteria

- [ ] **AC-1.** `--muted-foreground` light-mode token computes to
      a contrast ratio of ≥4.5:1 against `--background`. Measured
      value documented in decisions log.
- [ ] **AC-2.** Dark-mode token unchanged (already passes — slice
      audit finding).
- [ ] **AC-3.** Decisions log records the path chosen (A / B / C)
      and the engineer's reasoning.
- [ ] **AC-4.** Playwright visual-diff baselines (if any) updated
      in the same PR.
- [ ] **AC-5.** `pre-commit run --all-files` passes.
- [ ] **AC-6.** No code path outside `globals.css` modified UNLESS
      Path B or hybrid Path C is chosen and consumer-page lifts
      are required.

## Anti-criteria (P0 — block merge)

- **P0-360-1.** Does NOT touch the dark-mode token (passes AA).
- **P0-360-2.** Does NOT introduce a new color system (Tailwind
  v4 / oklch / OKLab) — keep the existing token shape.
- **P0-360-3.** Does NOT widen the lift to other tokens. The audit
  finding is specific to `--muted-foreground`; if other
  tokens turn out to be sub-AA, file as separate slices.
- **P0-360-4.** Does NOT skip the visual-regression pass.

## Dependencies

- **#331** (a11y audit) — `merged` (closing this slice).
- **#203** (dark-mode wiring) — `merged`. The dark-mode token is
  inherited from slice 203; this slice does not touch it.

## Notes

A future slice may extend the audit to other muted-\* tokens
(`--secondary`, `--accent`) which also use the OKLCh "near
foreground but dimmed" pattern. Out of scope here.
