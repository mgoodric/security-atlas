# 753 — Semantic status-color sweep across the remaining status surfaces

**Cluster:** frontend
**Estimate:** M (1-2d)
**Type:** JUDGMENT (the per-surface status-enum → semantic-token mapping)
**Status:** `ready` (the tokens are on `main` via PR #1427; the badge/alert
semantic variants land in the semantic-status adoption PR that surfaces this slice)

> Surfaced during the semantic-status adoption (PR after #1427), captured as
> follow-up per continuous-batch policy. That PR wired the PR-#1427 muted-pastel
> tokens (`--pass`/`--info`/`--warning`/`--critical`/`--progress`) into THREE
> surfaces — `web/components/ui/badge.tsx` (five semantic variants),
> `web/components/ui/alert.tsx` (`pass` + `info`), and the controls-list `State`
> column. It deliberately scoped OUT the broader migration of the hardcoded
> status palette elsewhere in the app, per the adoption PR's scope discipline.
> This is that follow-on.

## Narrative

**Why.** Several authed surfaces still render status with hardcoded Tailwind
palette utilities (`bg-emerald-*`, `bg-amber-*`, `bg-red-*`, `text-green-*`,
etc.) rather than the semantic `--pass`/`--info`/`--warning`/`--critical`/
`--progress` tokens. The hardcoded palette has two costs the tokens resolve:

1. **Theme inconsistency.** The hardcoded utilities are tuned for the neutral
   light theme; they do not track the dark-mode tint bumps the semantic tokens
   carry (light 15% / dark 24% for pills, per the design canvas), so the same
   "passing" state reads differently across surfaces and across themes.
2. **A11y drift.** The PR-#1427 tokens were each measured ≥4.5:1 as deep
   same-hue text on their soft tint (WCAG AA — the slice-360 floor). The
   ad-hoc `bg-emerald-100 text-emerald-700`-style pairings were never measured
   against that floor and are not guaranteed to clear it, especially in dark
   mode. Routing every status surface through the same measured tokens makes the
   a11y guarantee uniform.

Now that the `badge` + `alert` semantic variants exist (the adoption PR), the
migration is mechanical: swap the hardcoded class strings for the semantic
`Badge` variant (or the token utilities directly), preserving each surface's
existing status enum → color intent.

**What.** Migrate the hardcoded status colors to the semantic tokens across the
remaining status surfaces:

- `app/(authed)/audits/*` — audit / sample / control-workspace status badges.
- `app/(authed)/exceptions/*` — exception lifecycle status (open / approved /
  expired / rejected).
- `app/(authed)/policies/*` — policy status (draft / published / under-review).
- `app/(authed)/action-plans/*` — action-plan / POA&M status + acknowledgement
  state.
- The shared status/formatting helpers that compute the color class today —
  audit/expand the real helper set during the slice (candidates:
  `lib/format.ts`, a `lib/status.ts`, an `ack-rate.ts`-style helper) — so the
  color authority lives in ONE place per status family rather than re-deriving
  the palette per component.

The headline JUDGMENT call per surface is the **status-enum → semantic-variant
mapping** (e.g. exception `expired` → `warning` vs `critical`; policy
`under_review` → `info` vs `progress`). Read each surface's real status enum from
its data model — do NOT invent statuses or assume the controls-list mapping
transfers verbatim — and record each mapping in the decisions log.

**Scope discipline.** Tokens-only consumption: do NOT change `web/app/globals.css`
or `web/.claude-design/`. Do NOT add new badge/alert variants beyond what the
adoption PR shipped (extend the family only if a surface genuinely needs a tone
that has no member — and if so, mirror the soft-tint + deep-same-hue-text +
≥4.5:1 contract). Keep each surface's existing structure; this is a color-token
migration, not a layout change.

## Acceptance criteria

- [ ] **AC-1.** Audits, exceptions, policies, and action-plans status surfaces
      render via the semantic `Badge` variants (or the `--pass`/`--info`/
      `--warning`/`--critical`/`--progress` token utilities), with no remaining
      hardcoded `bg-emerald-*`/`bg-amber-*`/`bg-red-*`/`text-green-*`-style status
      color strings on those surfaces.
- [ ] **AC-2.** Each surface's status-enum → semantic-variant mapping is read
      from the real data model and recorded in the decisions log (the headline
      JUDGMENT call).
- [ ] **AC-3.** The color authority for each status family lives in one shared
      helper, not re-derived per component.
- [ ] **AC-4.** Every migrated pairing is soft-tint background + deep same-hue
      text, ≥4.5:1 (WCAG AA) — no inverted (deep-bg / light-text) pill, no
      unmeasured pairing introduced.
- [ ] **AC-5.** No `globals.css` / `.claude-design/` change; no new badge/alert
      variant unless a surface genuinely needs a tone with no existing member
      (and then it honors the same contract).
- [ ] **AC-6.** Existing e2e specs for the migrated surfaces still pass; any
      spec asserting on a status color/label is updated to the semantic pill
      (testid or role/text), NOT relaxed to pass.
- [ ] **AC-7.** Decisions log (the per-surface mappings) + changelog entry.

## Constitutional invariants honored

- No new product behavior — a presentation-layer token migration only. The
  AI-assist boundary, RLS/tenancy, and evidence/eval separation are untouched.
- A11y floor preserved (slices 331/359/360/361/362/363): every migrated status
  pairing clears the same ≥4.5:1 WCAG AA floor the PR-#1427 tokens were measured
  against.

## Canvas references

- `Plans/canvas/07-metrics.md` — status/KPI presentation surfaces.
- PR #1427 (`web/app/globals.css` semantic token layer) + the semantic-status
  adoption PR (badge/alert variants + controls-list table) this slice extends.

## Notes

- The controls-list `State` column already migrated in the adoption PR; it is the
  reference mapping pattern (`pass→pass`, `fail→critical`,
  `insufficient_evidence→warning`, `not_applicable→secondary`, unknown→`outline`)
  but each surface's enum differs — do not copy it blindly.
