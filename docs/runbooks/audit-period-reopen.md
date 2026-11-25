# Runbook — reopening a frozen audit period

**Scope:** putting a frozen `audit_periods` row back to `open` so it can be frozen again. The product has no reopen verb. This is an owner-only database recovery for a freeze made in error, such as a period frozen at the wrong cutoff before anything has been drawn or delivered from it.

**Audience:** the self-host operator with the migrate/owner role (`atlas_migrate`, `$DATABASE_URL`). `atlas_app` cannot do this, by design.

**Constitutional context:** invariant #10 (audit-period freezing). Since #3050, the original freeze is immutable in the database (migration `20261307000000_audit_period_freeze_immutable`):

- `audit_periods_freeze_immutable_trg` refuses changes to `frozen_at`, `frozen_hash` and `frozen_by` once they are set. On a frozen row it also refuses changes to `id`, `tenant_id`, `period_start`, `period_end` and `framework_version_id`.
- `populations_frozen_at_immutable_trg` refuses changes to `populations.frozen_at` once it is set.
- `audit_period_frozen_controls_immutable_trg` (#3066, migration `20261311000000_audit_period_frozen_controls`) refuses any UPDATE of the control set the freeze recorded, and its DELETE while the period exists. Freeze records one row per period, so a reopen must remove it or the next freeze fails on the primary key.

These triggers apply to every role, the owner included. A reopen has to disable them inside its own transaction.

---

## Never on a period that has freeze revisions

Check first:

```sql
SELECT count(*) FROM audit_period_freeze_revisions WHERE audit_period_id = '<period id>';
```

If the count is not 0, **stop**. Each revision's `previous_hash` links back to the original `frozen_hash`. Re-freezing writes a new original, which orphans the chain: `ReplayHash` can no longer reproduce any revision. `audit_period_freeze_revisions` is also append-only (no UPDATE or DELETE grant, FORCE RLS), so the chain cannot be repaired afterwards. A period that has been amended by import is past the point where a reopen is safe.

Also stop if any sample has been drawn from the period's populations (`samples`), or if the frozen period has been exported or handed to an auditor. Those records cite the current freeze.

## Procedure

Run it as the owner in one transaction. `ALTER TABLE ... DISABLE TRIGGER` is transactional: other sessions never see the trigger disabled, and the `ACCESS EXCLUSIVE` lock it takes blocks writers on the table until `COMMIT`.

```sql
BEGIN;

ALTER TABLE audit_periods DISABLE TRIGGER audit_periods_freeze_immutable_trg;

UPDATE audit_periods
SET status = 'open', frozen_at = NULL, frozen_hash = NULL, frozen_by = NULL, updated_at = now()
WHERE id = '<period id>' AND status = 'frozen';
-- expect UPDATE 1

ALTER TABLE audit_periods ENABLE TRIGGER audit_periods_freeze_immutable_trg;

ALTER TABLE audit_period_frozen_controls DISABLE TRIGGER audit_period_frozen_controls_immutable_trg;

DELETE FROM audit_period_frozen_controls WHERE audit_period_id = '<period id>';
-- expect DELETE 1

ALTER TABLE audit_period_frozen_controls ENABLE TRIGGER audit_period_frozen_controls_immutable_trg;

COMMIT;
```

Attached populations keep the `frozen_at` they were stamped with, because `populations_frozen_at_immutable_trg` refuses a reset. Freeze stamps only populations whose `frozen_at` is NULL. If the new freeze must move their horizon, reset them in the same transaction the same way: disable `populations_frozen_at_immutable_trg` on `populations`, set `frozen_at = NULL` for that period's populations, then re-enable it.

Then freeze again through the product (`POST /v1/audit-periods/{id}/freeze`) so the new freeze is hashed, logged and stamped under the tenant evidence-freeze lock.

## Verify

```sql
SELECT tgname, tgenabled FROM pg_trigger
WHERE tgname IN ('audit_periods_freeze_immutable_trg', 'populations_frozen_at_immutable_trg',
                 'audit_period_frozen_controls_immutable_trg');
-- every row tgenabled = 'O'
```

The reopen leaves no `audit_period_audit_log` row: it bypasses the product. Record the reason and the old `frozen_hash` wherever your program keeps its change records.
