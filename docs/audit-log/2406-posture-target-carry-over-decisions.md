# Issue #2406 — Carry a posture target forward to a new framework version

Posture targets are stored per framework version (#2136), so a new version's
tile started neutral. Activation now copies the prior version's target, and
the Set target dialog prefills from it.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Copy on activation, server-side

**Decision.** `frameworkscope.Store.Activate` copies the target in its own
transaction, after the activation is logged. The dispatcher chose this over
a dialog-only prefill so the tile is graded right after an upgrade.

**Why.** #2136 D1 left re-setting to the operator, to avoid grading a new
requirement set against an old number silently. The copy is not silent: it
writes its own audit row (D3), and the dashboard shows the target, which
can be edited as before.

## D2 — "Prior version" is the version-migration queue's source

**Decision.** The source is the one version the version-migration queue
(ADR 0019) leads into the activated version from, the rule #1793 uses. No
queue rows, rows from more than one version, or a source the tenant cannot
see: nothing is copied. `frameworkscope.PriorFrameworkVersions` holds the
rule, so the carry and the dialog's prefill (D4) cannot disagree.

**Why.** The queue is the estate's record of which version follows which.
Ordering versions by `effective_from` or label would guess, and a guess that
copies a target is worse than no copy.

## D3 — Copy only into an empty version; a system actor

**Decision.** `INSERT … SELECT … ON CONFLICT DO NOTHING`. A target already on
the new version stays, and a second activation (a later revision) writes
nothing. The row and its audit entry record `system:posture-target-carry-over`
(displayed as "System", #2554). The audit `after` carries the four values,
`carried_over_from` (version id and label), the activating person as
`activated_by`, and the `framework_scope_id`; `before` is NULL. The action
CHECK gains `carry_over` (migration `20261259000000`); the down migration
refuses to drop audit evidence, as `20261226000000`'s down does.

## D4 — Dialog prefill from `prior_target`

**Decision.** Each `GET /v1/frameworks/posture` row with no target carries
`prior_target`, the prior version's target, or null. The dialog seeds from it
and says it was prefilled.

**Why.** It covers versions activated before this change, and costs one read
of the queue's distinct (to, from) pairs. Nothing is saved until the operator
saves.
