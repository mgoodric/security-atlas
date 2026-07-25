# Slice 753 — Semantic Status Color Sweep Decisions

Date: 2026-07-25

## Scope

Swept the named authed status surfaces under `web/`:

- `web/app/(authed)/audits`
- `web/app/(authed)/exceptions`
- `web/app/(authed)/policies`
- `web/app/(authed)/action-plans`
- `web/components/action-plans`

Shared status mapping now lives in `web/lib/status-variants.ts`. Surfaces render
status pills with `Badge` semantic variants where possible. The policy
acknowledgment-rate progress fill uses semantic token utilities directly because
it is a bar, not a pill.

## Enumerated Hardcoded Status-Palette Sites

Converted:

| Site                                                  | Previous status palette use                                        | New authority                                                   |
| ----------------------------------------------------- | ------------------------------------------------------------------ | --------------------------------------------------------------- |
| `web/app/(authed)/audits/format.ts`                   | audit pill/dot amber, sky, slate class strings                     | `auditStatusVariant`, `auditStatusDotClass`                     |
| `web/app/(authed)/audits/page.tsx`                    | audit pill, frozen lock, urgent cue amber/sky classes              | `Badge` variants plus `text-info`, `bg-warning`, `text-warning` |
| `web/app/(authed)/exceptions/page.tsx`                | exception lifecycle pill emerald/amber/rose/slate classes          | `Badge` + `exceptionStatusVariant`                              |
| `web/app/(authed)/policies/page.tsx`                  | policy lifecycle pill emerald/amber/rose classes                   | `Badge` + `statusPillVariant`                                   |
| `web/app/(authed)/policies/[id]/page.tsx`             | detail page had a separate non-semantic status variant switch      | shared `statusPillVariant`                                      |
| `web/app/(authed)/policies/ack-rate.ts`               | ack-rate progress fill emerald/amber/rose and failing caption rose | `bg-pass`, `bg-warning`, `bg-critical`, `text-critical`         |
| `web/app/(authed)/action-plans/status.ts`             | action-plan lifecycle pill emerald/sky/amber/rose/slate classes    | `actionPlanStatusVariant`                                       |
| `web/app/(authed)/action-plans/page.tsx`              | action-plan list pill composed from class helper                   | `Badge` + `statusPillVariant`                                   |
| `web/app/(authed)/action-plans/[id]/page.tsx`         | action-plan detail pill composed from class helper                 | `Badge` + `statusPillVariant`                                   |
| `web/components/action-plans/linked-action-plans.tsx` | duplicate linked-plan pill switch                                  | `Badge` + `actionPlanStatusVariant`                             |

Left unchanged:

| Site                                                        | Reason                                                                                                    |
| ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `web/app/(authed)/action-plans/new/action-plan-form.tsx`    | `text-rose-600` marks validation errors, not lifecycle/status state.                                      |
| `web/app/(authed)/action-plans/new/entity-multi-select.tsx` | `text-rose-600` and `text-amber-600` mark form validation/selection warnings, not lifecycle/status state. |
| `web/components/action-plans/linked-action-plans.tsx`       | `text-rose-600` marks a load error, not lifecycle/status state.                                           |

Out of scope for this slice (enumerated by the same grep, staying hardcoded
here because slice 753's scope is the four surfaces above; each is a follow-up
slice candidate):

| Site (grouped)                                                                                                                                                                                                                                                                 | Reason for staying                                                                                                          |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| `web/app/(authed)/controls/[id]/_sections/bodies.tsx`, `web/components/control/strm.ts`, `web/components/control/confidence-band.ts`, `web/components/control/coverage-table.tsx`                                                                                              | Controls surfaces; the controls-list `State` column migrated in the adoption PR, the rest is a separate controls follow-up. |
| `web/app/(authed)/risks/filters.ts`, `web/components/risk-hierarchy/decision-timeline-panel.tsx`                                                                                                                                                                               | Risk severity/decision surfaces; severity banding needs its own enum-to-token judgment pass.                                |
| `web/app/(authed)/board-packs/page.tsx`, `web/components/board-pack/*`                                                                                                                                                                                                         | Board-pack surfaces (freshness, treatment, posture tiles); a distinct status-family sweep.                                  |
| `web/components/dashboard/*`                                                                                                                                                                                                                                                   | Dashboard KPI/freshness panels; threshold-band judgment pass of their own.                                                  |
| `web/components/calendar/*`                                                                                                                                                                                                                                                    | Calendar type-legend colors are categorical (event type), not status semantics; no semantic token maps.                     |
| `web/components/shell/in-progress-audit-pill.tsx`                                                                                                                                                                                                                              | Slice 362's audit pill, already measured at 15.77:1 in that slice; migrating it re-opens that measurement deliberately.     |
| `web/components/shell/sidebar-counts.tsx`, `web/components/auth/tenant-switcher.tsx`, `web/components/attest/AttestForm.tsx`, `web/components/questionnaire/*`, `web/components/audit/comment-thread.tsx`, `web/app/(authed)/framework-scopes/[framework_version_id]/page.tsx` | Mixed alert/urgency/validation cues outside the four scoped status families.                                                |

## Status Mapping

| Surface         | Source enum/status       | Semantic token/variant | Rationale                                                                 |
| --------------- | ------------------------ | ---------------------- | ------------------------------------------------------------------------- |
| Audits          | `open`                   | `progress`             | The period is actively underway, not failing.                             |
| Audits          | `in_progress`            | `progress`             | Same active workflow meaning as `open`; dot still pulses.                 |
| Audits          | `frozen`                 | `info`                 | Locked deterministic replay state, informational rather than pass/fail.   |
| Audits          | `closed`                 | `secondary`            | Terminal neutral state; no pass/fail claim.                               |
| Audits          | `planned`                | `secondary`            | Future neutral state.                                                     |
| Audits          | unknown                  | `outline`              | Backend extension fallback without implying meaning.                      |
| Exceptions      | `active`                 | `pass`                 | Approved waiver is currently in force.                                    |
| Exceptions      | `requested`              | `progress`             | Workflow is pending review.                                               |
| Exceptions      | `approved`               | `progress`             | Approved but not yet active; still in lifecycle motion.                   |
| Exceptions      | `denied`                 | `critical`             | Adverse terminal outcome.                                                 |
| Exceptions      | `expired`                | `warning`              | Waiver is no longer in force and deserves attention, but is not a denial. |
| Exceptions      | unknown                  | `outline`              | Fallback without implying meaning.                                        |
| Policies        | `published`              | `pass`                 | Current effective policy state.                                           |
| Policies        | `under_review`           | `progress`             | Review workflow is underway.                                              |
| Policies        | `approved`               | `info`                 | Approved-but-not-published is informational/readiness, not current pass.  |
| Policies        | `draft`                  | `secondary`            | Neutral work-in-prep state.                                               |
| Policies        | `retired` / `superseded` | `warning`              | Not current; needs operator awareness without marking it failed.          |
| Policies        | unknown                  | `outline`              | Fallback without implying meaning.                                        |
| Policy ack rate | `green` (`>=95%`)        | `pass`                 | Meets the acknowledgment threshold.                                       |
| Policy ack rate | `amber` (`70-94%`)       | `warning`              | Below target but not severe.                                              |
| Policy ack rate | `red` (`<70%`)           | `critical`             | Severe acknowledgment gap.                                                |
| Policy ack rate | `none`                   | muted                  | No data; no status claim.                                                 |
| Action plans    | `verified`               | `pass`                 | Completed and verified.                                                   |
| Action plans    | `completed`              | `info`                 | Work completed, pending verification distinction preserved.               |
| Action plans    | `in_progress`            | `progress`             | Active remediation work.                                                  |
| Action plans    | `blocked`                | `critical`             | Operator-blocking adverse state.                                          |
| Action plans    | `draft`                  | `secondary`            | Neutral pre-workflow state.                                               |
| Action plans    | unknown                  | `outline`              | Fallback without implying meaning.                                        |

## Contrast Measurements

Measured with Chromium/Playwright against the actual OKLCH token values from
`web/app/globals.css`, using the `Badge` semantic tint contract after this
slice: light `bg-*/[0.03]`, dark `dark:bg-*/20`, composited over light
`--background` and dark `--card`. WCAG contrast was computed from rendered
computed styles.

| Variant     | Light contrast | Dark contrast |
| ----------- | -------------: | ------------: |
| `pass`      |         4.55:1 |        5.00:1 |
| `info`      |         4.54:1 |        4.86:1 |
| `warning`   |         4.55:1 |        4.76:1 |
| `critical`  |         4.59:1 |        4.66:1 |
| `progress`  |         4.54:1 |        4.69:1 |
| `secondary` |        16.42:1 |       14.48:1 |
| `outline`   |        19.79:1 |       17.16:1 |

The pre-slice semantic badge tint (`bg-*/15`, `dark:bg-*/24`) failed measured
contrast in multiple variants, so this slice adjusted the existing semantic
badge variants rather than changing status meanings.

## Test Assertion Check

No Playwright spec asserted on the removed hardcoded color classes. The affected
unit tests that did assert palette classes were updated:

- `web/app/(authed)/audits/format.test.ts`
- `web/app/(authed)/policies/ack-rate.test.ts`
- `web/app/(authed)/action-plans/status.test.ts`
