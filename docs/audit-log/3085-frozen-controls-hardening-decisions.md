# #3085 — frozen-controls snapshot hardening: decisions

- detection_tier_actual: manual_review
- detection_tier_target: integration

Source: the #3077 re-review's non-blocking items N-a to N-e
(https://github.com/mgoodric/security-atlas/pull/3077#issuecomment-7168616763).

Each new test failed against the pre-change schema (down file applied) and passes after:

- `TestFrozenControls_CommitCheckIgnoresTenantSwitch/{cleared,switched}`: `want check_violation (23514), got <nil>`.
- `TestFrozenControls_InsertGuardRefusesRoleSwitch`: `want check_violation (23514), got <nil>`.
- `TestFrozenControls_RecordFunctionNotPublic`: `want insufficient_privilege (42501), got <nil>`.

| #   | Decision                                                                                                                                                                | Why                                                                                                                                                                                                                                  |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| D1  | N-a: make the check function `SECURITY DEFINER`, rather than soften the docs.                                                                                           | The review offered either. The definer form closes the bypass. The function takes no input but `NEW`, reads two tables by `NEW`'s keys, and returns nothing, so running it as the owner hands the caller nothing.                    |
| D2  | `search_path = pg_catalog, public, pg_temp`, not the ticket's `pg_catalog, public`.                                                                                     | Without `pg_temp` listed, Postgres searches it first for relations, so a session's temporary `audit_periods` could shadow the real one inside the definer. Listing it last is the form the Postgres docs give for definer functions. |
| D3  | N-b: require both `current_user` and `session_user` to pass, not "exclude roles atlas_app is a member of".                                                              | `SET ROLE` never changes `session_user`, so the check holds for any role atlas_app might be granted later. It needs no membership walk.                                                                                              |
| D4  | The N-b test grants `atlas_service_account` SELECT on `audit_periods` and INSERT on `audit_period_frozen_controls` for its own run only.                                | Without those grants the INSERT fails with a privilege error before the guard runs, so the test would not show that the guard refuses. It grants only what the role lacks and revokes the same in cleanup.                           |
| D5  | N-c: grant `EXECUTE` to `atlas_migrate` only.                                                                                                                           | Every caller (backfill, demo seed, SQL fixtures, `dbtest.FreezeRaw`) runs as `atlas_migrate` or a superuser. The function stays `SECURITY INVOKER`, so the insert guard still judges the real caller.                                |
| D6  | N-d: the test helper `reopenFrozenPeriod` now follows the runbook's step order. It reopens the period, fires the deferred check, and only then removes the control set. | The review asked for the helper to match the runbook. The runbook is what an operator runs, so the test now exercises that exact sequence.                                                                                           |
