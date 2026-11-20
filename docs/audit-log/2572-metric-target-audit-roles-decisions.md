# Issue #2572 — Metric targets: audited, set by admin and grc_engineer

Follow-ups from #2571 (#2341). Setting a metric's target now writes an audit
row; grc_engineer may set one, as for a framework posture target; and the
target dialog stores a percent target on the scale the metric's observations
use.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

All three gaps were found in review of #2571, not by a test. The scale
mismatch is a pure-function bug a `metrics.test.ts` case would have caught
had the points scale been considered; the cases are added here.

## D1 — Audit in `me_audit_log`, action `metric_target_set`

**Decision.** `PUT /v1/metrics/{id}/target` writes one `me_audit_log` row
in the upsert's transaction: `user_id` (uuid.Nil for a caller that is not a
user), `actor_principal` (#2329), `before` (JSON null the first time) and
`after`. Migration `20261267000000_metric_target_audit` extends the action
CHECK, rewritten in full from `20261263000000` with every value kept.

**Why.** The LLM settings, control import settings and the #2609 admin
changes all audit through `me_audit_log` with `actor_principal`; it already
feeds the unified audit log, so no new table, RLS policy or unified-log kind
is needed. Posture targets have their own table because they predate that
pattern settling. The current row is read `FOR UPDATE`
(`GetMetricTargetForUpdate`) so the `before` value is the one the upsert
replaces under concurrent writes, as the posture-target store does.

The audit shape lists every field without `omitempty`, so a threshold the
upsert clears reads as `null` rather than disappearing from `after`.

There is no `DELETE /v1/metrics/{id}/target` route on `main`, so only the
upsert is audited. The ticket named DELETE; the dispatcher confirmed PUT only.

## D2 — admin and grc_engineer set a metric target (Matt, 2026-11-19)

**Decision.** Matt's call on 2026-11-19, relayed by the dispatcher: align
with posture targets. The handler gate is `authz.IsTenantApprover` over the
live tenant roles (was `IsTenantAdmin`). In rego, grc_engineer gets one
admit bound to `PUT /v1/metrics/{id}/target` (resource `metrics`, action
`write`), mirrored byte-identical in `policies/authz` (#2410). `metrics` is
not added to `grc_writable_resources`, so manual input
(`POST /v1/metrics/{id}/inputs`) stays admin's.

**Why.** A metric target and a posture target are the same kind of
threshold configuration, and grc_engineer runs the program's metrics. With
D1 the write is audited, so widening it does not lose who changed what.

The #2428 parity row and the #2447 gate-site row move to the approver gate.
`metric_target_2572_test.go` drives the OPA decision per role (admin and
grc_engineer allowed; control_owner, auditor, viewer and no role denied) and
pins the admit to the one path. The UI guard `canSetMetricTarget` reads
`/v1/me`'s `is_approver`, which is `IsTenantApprover` and true for every
tenant admin.

## D3 — The target's scale follows the latest observation

**Decision.** `metricTargetScale(unit, latest)` picks `fraction`, `points`
or `raw`. A percent or ratio metric whose latest observation is above 1 is
on the points scale: the typed percentage is stored as typed (90), not as a
fraction (0.9). With no observation, or one at or below 1, the evaluators'
fraction scale is assumed, as before.

**Why.** `thresholdBadgeColor` compares raw numbers, so a 0.9 target against
an 85 observation always grades green. Picking the scale from the
observation fixes the badge rather than only warning about it; the dialog's
hint says when the points scale is in use. The "above 1 is points" rule is
the one `formatValue` already uses to display the value.

A metric whose series mixes scales, or whose only observations are at or
below 1 on the points scale (under 1%), still reads as fractions. That is
the same ambiguity `formatValue` has and is not resolved here.

## D4 — A ratio is entered as a percentage

**Decision.** `ratio` joins `percent` in the dialog's conversion and in the
0..100 range check.

**Why.** `formatValue`, the chart axis and the board card all show a ratio
as a percentage. Entering it raw meant typing 0.4 for a value the page
shows as 40%.
