# Runbook — frozen periods and their recorded control sets

**Scope:** upgrading to migration `20261311000000_audit_period_frozen_controls` (#3066), and finding and repairing a frozen audit period that has no recorded control set.

**Audience:** the self-host operator with the migrate/owner role (`atlas_migrate`, `$DATABASE_URL`).

**Background:** since #3066 a freeze records the exact control list its hash covers in `audit_period_frozen_controls`. Replay and every freeze revision read that row, never the live catalog. A frozen period without one cannot be replayed: `ReplayHash` and a historical import amending it fail with `ErrNoControlSnapshot`. See ADR 0003, amendment #3066.

---

## Upgrading

Deploy the migration and the binary that ships with it together, with no freezes or historical imports in between.

- The migration backfills every period already frozen from the tenant's catalog as it stands at that moment, with `source = 'migration_backfill'`. A period whose catalog has not changed since its freeze replays; one whose catalog had already changed still mismatches, as it did before the upgrade.
- A binary older than the migration freezes without recording the control set. The migration installs a commit-time check (`audit_periods_frozen_has_controls_trg`) that refuses such a freeze, so during the window between migration and rollout (Helm's `pre-upgrade` hook, or Compose's old `atlas` container while `atlas-migrate` runs) an old-binary freeze fails with a `check_violation` naming migration `20261311000000`, and the period stays open. Retry the freeze once the new binary is serving.

## Detecting a frozen period without a control set

While it is enabled, the commit check refuses any commit that would leave such a period. It runs as `atlas_migrate`, not as the committer, so a session that clears or switches `app.current_tenant` before `COMMIT` is refused too (#3085). It can still happen if the owner disabled the trigger, for example during a manual recovery.

Run the query as `atlas_migrate` (`$DATABASE_URL`). That role has BYPASSRLS, so it sees every tenant's rows. As `atlas_app`, RLS hides every row outside the session's tenant, and the query returns 0 rows even when periods are missing their control sets:

```sql
SELECT ap.tenant_id, ap.id, ap.name, ap.frozen_at
FROM audit_periods ap
WHERE ap.status = 'frozen'
  AND NOT EXISTS (SELECT 1 FROM audit_period_frozen_controls fc
                   WHERE fc.tenant_id = ap.tenant_id AND fc.audit_period_id = ap.id);
-- expect 0 rows
```

## Recovering

Re-run section 6 of the migration as `atlas_migrate`. It records each listed period's current catalog with `source = 'migration_backfill'` and leaves every existing row alone:

```sql
SELECT audit_period_record_catalog_controls(ap.tenant_id, ap.id, 'migration_backfill')
FROM audit_periods ap
WHERE ap.status = 'frozen';
```

Then check the result: replay the period (the integration tests call `period.Store.ReplayHash`) and compare it with its `frozen_hash`. A match means the catalog is the one the freeze hashed. A mismatch means the catalog changed after the freeze; the recorded set is not freeze-time, and its `source` says so. Record which periods were recovered, and their outcome, in your program's change records.

`atlas_app` cannot do this. It holds no `EXECUTE` on `audit_period_record_catalog_controls` (#3085), and the insert guard (`audit_period_frozen_controls_insert_guard_trg`) accepts a source other than `freeze` only when both `current_user` and `session_user` are superuser or BYPASSRLS roles, so `SET ROLE atlas_service_account` from an `atlas_app` session does not pass.
