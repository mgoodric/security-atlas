# 748 — Deep audit-period snapshot integration for ActionPlan

**Cluster:** Risk register / Audit workflow
**Estimate:** 1d
**Type:** JUDGMENT (follow-up to slice 384)

## Narrative

Surfaced during slice 384 (`feat(risk): ActionPlan primitive — schema + CRUD +
risk/control linkage`), captured as a follow-up per the continuous-batch
policy.

Slice 384 delivered the FOUNDATION of audit-period freezing for action plans
(AC-27): `action_plans.audit_period_id` (NULL FK to `audit_periods(id)`), a
`ListActionPlansSnapshot(frozenAt)` store method, and a snapshot SQL query that
returns only plans with `created_at <= frozen_at`. This is the read-side horizon
that honors invariant #10 (and P0-384-5: a live edit today never mutates what a
past frozen snapshot returns, because the snapshot read is `created_at`-horizoned
and the live `UPDATE` path is independent).

What slice 384 deliberately did NOT do (decisions log D5): wire that snapshot
read into the slice-028 AuditPeriod freeze/materialization path. Today the
slice-028 `internal/audit/period` Freeze computes its frozen view from the live
evidence ledger + control set at freeze time (canvas §8.4, no evidence-snapshot
table — the horizon is a read-side filter against `observed_at`). Action plans
are not yet a participant in that frozen-view join, nor in the OSCAL AP/AR
aggregate path that consumes a frozen period.

This slice integrates ActionPlan into that deeper path so that when an auditor
freezes a period, the period's materialized/exported view of "remediation
commitments in scope at freeze time" draws from
`ListActionPlansSnapshot(period.frozen_at)` rather than the live list — closing
the loop between the slice-384 foundation and the slice-028 freezing primitive.

## Acceptance criteria

- [ ] AC-1: The slice-028 frozen-period read path (the join that materializes a
      period's frozen view) includes action plans via
      `actionplan.Store.ListSnapshot(period.frozen_at)` — only plans with
      `created_at <= frozen_at` appear, reflecting their state at `frozen_at`.
- [ ] AC-2: A plan created or mutated AFTER `frozen_at` is excluded from the
      period's snapshot (live state continues independently).
- [ ] AC-3: If/where the OSCAL AP/AR aggregate surfaces remediation commitments,
      it sources them from the frozen snapshot, not the live list, for a frozen
      period.
- [ ] AC-4: Integration test: freeze a period, create a new plan after the
      freeze, assert the period's snapshot excludes it and includes a
      pre-freeze plan.
- [ ] AC-5: P0-384-5 re-verified end-to-end: editing a plan after a freeze does
      not change the frozen period's snapshot output.

## Constitutional invariants honored

- **Invariant #10 (audit-period freezing):** this slice completes the deep
  integration the slice-384 foundation set up.
- **Invariant #2 (append-only / point-in-time replay):** the snapshot is a
  read-side horizon; no records are mutated to produce the frozen view.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.4 (audit-period freezing)
- `docs/audit-log/384-action-plan-primitive-decisions.md` D5

## Dependencies

- #384 (ActionPlan primitive — provides `audit_period_id` + `ListSnapshot`) —
  must be merged first.
- #028 (AuditPeriod + freezing primitive — provides the freeze/materialization
  path this slice extends) — already on `main`.

## Status

`in-review` — slice 384 has merged on `main`, clearing the blocker; this slice
is built. `period.Store.Snapshot` (new `internal/audit/period/snapshot.go`) draws
action plans through the injected `actionplan.Store.PeriodSnapshotLister()` seam
at the period's `frozen_at` horizon. Read-side wiring only — no migration, no new
route. AC-1/AC-2/AC-4/AC-5 covered by live integration tests; AC-3 is N/A (OSCAL
POA&M does not yet consume action plans — that is slice-384 spillover #1, out of
scope). Decisions log: `docs/audit-log/748-actionplan-audit-period-snapshot-decisions.md`.
