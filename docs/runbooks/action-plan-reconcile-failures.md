# Runbook — action-plan reconcile failures fail control evaluation

## TL;DR

Since #2109, every control evaluation ends by moving the action plans linked
to that control between `completed` and `verified`
(`action_plans_reconcile_verification`). The move runs in the evaluation's
transaction (#2109 D4), so **if the reconcile errors, the whole evaluation
rolls back**: no `control_evaluations` rows are written for that run, and the
caller sees the error. A broken reconcile looks like evaluation itself has
stopped.

## Symptom

The error text carries `actionplan: reconcile verification for control <uuid>`.
Where it shows up depends on who asked for the evaluation:

| Caller                                               | What you see                                                                                                                      |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Ingest subscriber (`internal/eval/consumer.go`)      | `eval subscriber: evaluate; will redeliver` at WARN, repeating every ~2s for the same `control`: the message is NAKed and retried |
| Scheduled sweep / replay (`internal/eval/engine.go`) | `eval: evaluate control` at ERROR per control; the sweep carries on with the other controls and returns the joined errors         |
| `POST /v1/controls/{id}/evaluate`                    | a 5xx to the caller                                                                                                               |
| Attestation (`internal/api/controls/attest.go`)      | the attestation request fails                                                                                                     |

The control's latest evaluation (`GET /v1/controls/{id}/history`) stops
advancing, while its evidence keeps arriving.

## Likely causes

- **A migration left the function or its guard out of step.** The trigger
  `action_plans_status_transition_guard` admits `verified -> completed` only
  through `action_plan_in_system_move()`, which checks the function's owner
  and the per-call nonce in `action_plan_system_move_nonces` (#2458). A
  half-applied up/down of `20261223000000` or `20261264000000` produces
  `illegal action_plan status transition: verified -> completed`
  (SQLSTATE 23514) or `relation "action_plan_system_move_nonces" does not exist`.
- **The function lost its owner.** Ownership moved off the migrate role
  (`ALTER FUNCTION ... OWNER TO`) means `current_user` inside the function no
  longer has the grant on `action_plan_system_move_nonces`: SQLSTATE 42501.
- **No tenant context.** The function refuses without `app.current_tenant`
  (42501, `refused without a tenant context`). A new caller of
  `EvaluateControl` that does not run under `tenancy.WithTenant` hits this.
- **Lock waits.** The function takes `FOR UPDATE` on the linked plans; a
  long-held plan lock shows up as an evaluation timeout, not a reconcile error.

## Check

```sql
-- Function and guard present, owned by the migrate role:
SELECT p.proname, r.rolname AS owner, l.lanname
FROM pg_proc p
JOIN pg_roles r ON r.oid = p.proowner
JOIN pg_language l ON l.oid = p.prolang
WHERE p.proname IN ('action_plans_reconcile_verification', 'action_plan_in_system_move');

-- The nonce table exists, is owner-only, and holds no committed row:
SELECT relacl FROM pg_class WHERE relname = 'action_plan_system_move_nonces';
SELECT count(*) FROM action_plan_system_move_nonces;  -- always 0
```

## Fix

Re-apply the migrations (`just migrate-up`) as the migrate role, so the
function, the guard and the nonce table are recreated together and owned by
that role. Do not work around a failing reconcile by catching its error in
the evaluator: plan status would then drift from the evaluations that decide
it. Once fixed, NAKed ingest messages redeliver on their own; re-run a
scheduled sweep or `POST /v1/controls/{id}/evaluate` for anything else.
