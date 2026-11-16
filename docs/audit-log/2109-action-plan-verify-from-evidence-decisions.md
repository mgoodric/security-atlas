# #2109 — action plans verified from evidence — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2109
**Parent:** https://github.com/mgoodric/security-atlas/issues/2071
**Date:** 2026-11-16

After each control evaluation, a completed plan whose linked controls all pass
becomes verified, and a verified plan whose linked control now fails returns to
completed. Both moves are the system's.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

The bug that turned up during the work: the first version of the down migration
dropped `system_actor`, and re-applying the up migration then failed its
one-actor CHECK on the rows the system had written. A manual down/up round trip
on a scratch cluster caught it. D6 has the fix.

---

## D1 — a SECURITY DEFINER function, not a GUC, gates the system path

`action_plans_reconcile_verification(control_id)` makes both moves. It is
`SECURITY DEFINER` with `search_path = public, pg_temp`, and like #2362's
functions it checks the tenant context, the other way round: this is a tenant
act, so it refuses when `app.current_tenant` is unset. The definer role bypasses
RLS, so every read and write filters on that tenant explicitly.

The transition trigger allows `verified → completed` only when
`action_plan_in_system_move()` is true:

- the transaction-local GUC `atlas.action_plan_system_move` is `on` (the
  function sets it around its UPDATE and audit INSERT, then clears it), **and**
- `current_user` is the owner of `action_plans_reconcile_verification`.

The GUC alone would be worthless: the app role can `set_config` anything. The
owner check is what holds. `atlas_app` can `SET ROLE atlas_service_account`
(the one role it is granted), but never the function's owner (`atlas_migrate`),
so a SQL injection under the app role can set the GUC and still cannot make the
move or forge a system audit row. `TestReconcile_TriggerRefusesUnverifyOutsideTheFunction`
tries both, as `atlas_app` and as `atlas_service_account`, with the GUC set.
Weakening the guard to the GUC alone makes that test fail (observed).

`atlas_app` may call the function, because the evaluator runs as `atlas_app`.
A direct call can only do what an evaluation would: it reads the latest
evaluations and moves plans to match them. It takes no status from the caller.

## D2 — the audit row names the system with `system_actor`, not a sentinel UUID

`action_plan_audit_log.actor_id` was `UUID NOT NULL`. The exception and decision
logs name their system actor in a text column (`system:exception-expiry`); this
log had no such column. It gains `system_actor TEXT`, `actor_id` becomes
nullable, and two CHECKs hold the shape: exactly one of the two is set, and
`system_actor` is `system:action-plan-verification` or NULL. A sentinel user id
would have needed a fake user row and would read as a person in every join.
A BEFORE INSERT trigger refuses a `system_actor` row outside the function (D1),
since the app role's INSERT policy would otherwise let it write one.

## D3 — "passes" is the control's latest evaluation run, every cell `pass`

The ticket says "latest evaluation". Evaluations are per (control, scope cell),
and a cell that stops applying keeps its last row forever, so "latest row per
cell" would let a stale cell decide. The latest _run_ (`eval_run_id` of the
newest row) is the control's current state as one evaluation computed it. The
control passes when every row of that run is `pass`. `excepted` is not a pass:
a waiver does not close a gap (GLOSSARY, Exception). A control with no
evaluation does not pass. A plan with zero linked controls gets NULL and is
never touched: a person verifies it (#2108).

## D4 — the hook runs in the evaluation transaction, after the rows are written

`Engine.EvaluateControl` calls `actionplan.ReconcileVerification` after
appending the run, inside the same transaction and tenant context, so the audit
row commits or rolls back with the evaluation. It locks `action_plans` rows
only, in id order (`FOR UPDATE OF ap ... ORDER BY ap.id`), so two evaluations
of controls sharing plans take their locks in the same order. It touches no
risk row, so the #2335 lock order and the #2407 deadlock retry are not in play.
Replay calls `EvaluateControl` too; its rows become the latest, so the plans
follow them, the same as every other reader of "latest evaluation".

## D5 — the lifecycle table gets one system move, not two

`lifecycle.Table.Validate` refuses a from → to pair listed twice, and
`completed → verified` is already the person's `verify` move. That entry's note
now says the system makes the same move for plans with linked controls. The new
entry is `verified → completed`, action `unverify`, actor `system`, no route.
`verified` is no longer terminal. `AllowedTransition` admits
`verified → completed` so the table and the state machine agree
(`TestLifecycle_MatchesAllowedTransition`); `Store.Update` refuses any status
change out of `verified`, so PATCH cannot make it, and the trigger is the third
gate. The trigger-pairs test makes the move as the owner with the marker set,
which is the function's path.

## D6 — the down migration keeps `system_actor`

Rows the system wrote are the record, and the log is append-only. The down
migration drops the functions and the INSERT guard and restores the old
transition trigger, but keeps `system_actor`, its CHECKs and the nullable
`actor_id`. The up migration uses `ADD COLUMN IF NOT EXISTS` and drops the
CHECKs before adding them, so down → up works with system rows present.

## D7 — the unified audit log is unchanged

`action_plan_audit_log` is not one of the unified log's sources (no kind maps
to it on `main`), so #2379's kind → resource mapping has nothing to add. If the
log joins the unified view later, its system rows carry `system_actor` for the
actor column.

## D8 — "still failing" is a flag on the read, not a status

The ticket's "Completed · linked controls still failing" is `completed` plus
`controls_failing: true`, computed on read from the same
`action_plan_linked_controls_pass` the function uses. It is on
`GET /v1/action-plans/{id}` and on the control's and risk's linked-plan lists,
not on the paginated plan list, which would pay the per-plan evaluation lookup
for every row.
