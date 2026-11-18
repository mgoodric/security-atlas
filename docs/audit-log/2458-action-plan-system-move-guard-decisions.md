# #2458 — action-plan system-move guard follow-ups — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2458
**Parent:** #2109 (review of #2451)
**Date:** 2026-11-18

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

The guard gap was found in review, not by a test.
`TestReconcile_GuardRefusesOtherOwnerFunctions` now covers it, and was
observed failing against the #2109 guard (down migration applied: the move
through another owner function succeeded with the marker `on`) and passing
with this one.

---

## D1 — a per-call nonce, not a dedicated owner role

The ticket offered two hardenings. A dedicated owner role means a new role in
`migrations/bootstrap/01-roles.sql`, membership for the migrate role so it can
create and replace the function, and the same in every deploy path that
bootstraps roles. A nonce stays inside one migration.

`action_plans_reconcile_verification` draws `gen_random_uuid()` per move,
inserts it into `action_plan_system_move_nonces`, and sets the GUC
`atlas.action_plan_system_move` to it. After the UPDATE and the audit INSERT
it clears the GUC and deletes the row. `action_plan_in_system_move()` is true
only when `current_user` is the function's owner **and** the GUC names a row
in that table.

Why it holds:

- The table has no grant beyond its owner, so the app role (or
  `atlas_service_account`) can neither write a nonce nor read one.
- Another `atlas_migrate`-owned SECURITY DEFINER function (#2362, #2364) runs
  as the owner, but none writes that table, so a GUC the caller set names no
  row. A guessed UUID names no row.
- A row exists only between the INSERT and the DELETE in one call, so it is
  never committed and no other session sees it. An error mid-call rolls it
  back with the rest.

The owner check stays first. The guard is now plpgsql so the table lookup is
planned only after the owner check passes: a caller without the grant gets
`false` (and the trigger's `check_violation`), not a 42501 from the lookup.

## D2 — the trigger is exercised for a granted non-owner

`atlas_service_account` holds no grant on `action_plans`, so the #2109 test
saw 42501 from the grant, and the trigger never ran for it.
`TestReconcile_TriggerRefusesAGrantedNonOwner` grants it `SELECT, UPDATE`
inside a migrate-role transaction that rolls back, sets role, sets the marker,
and requires the trigger's 23514. No grant is committed.

## D3 — a person-verified plan that gains a control is governed by evidence

A plan a person verified with no linked control (#2108) can have a control
linked later; linking is allowed on any live plan. Intended behaviour, pinned
by `TestReconcile_PersonVerifiedPlanGainsAControl`:

- Linking moves nothing. The plan stays `verified`.
- From then on the control's evaluations decide, as for any plan with linked
  controls (#2109 D3): a pass leaves it `verified` with no system row; a fail
  returns it to `completed` under the system actor.

This follows from #2109 D5: a plan with linked controls is verified by its
evidence, not by a person. The person's earlier verification stays in the
plan's audit log.

## D4 — a control with no scope cell does not hand the decision to an old run

The ticket assumed a control whose scope resolves to no cell writes a run with
no rows, leaving the previous run in charge. On `main` it does not:
`eval.Engine` writes one row with a NULL `scope_cell_id` when a control
resolves to zero cells (AC-1, "every control has a queryable pass/fail"), so
the new run exists and decides. `TestReconcile_ControlWithNoScopeCellStillDecides`
pins that: with the scope emptied, a pass verifies the plan, a later fail
returns it to completed, and the latest run is one NULL-cell row. Nothing to
treat as not passing; the reconcile function's comment records it.

## D5 — the ops note is a runbook

Reconcile errors fail `EvaluateControl` (#2109 D4) and so stop evaluation for
that control. `docs/runbooks/action-plan-reconcile-failures.md` lists how that
surfaces for each caller, the likely causes, and the fix.
