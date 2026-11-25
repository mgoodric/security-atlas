-- security-atlas -- #3066: the freeze hash's control set is recorded at freeze.
--
-- ADR 0003 hash-input ingredient #2 is the sorted control ids. Until now
-- Freeze and every replay read it from the tenant's live catalog
-- (ListControlIDsForPeriodHash), with no horizon. Creating or deleting any
-- control after a freeze changed ReplayHash for revision 1 and every later
-- revision, so the original freeze no longer replayed (#1742 rule).
--
-- A created_at horizon cannot express deletion: controls are hard-deleted
-- and carry no deleted_at. So the set is recorded instead.
--
--   audit_period_frozen_controls  one row per frozen period: the exact
--                                 control id list the freeze hashed.
--                                 Freeze writes it in its own transaction
--                                 from the list it hashes; ReplayHash and
--                                 every revision hash read it, never the
--                                 live catalog. A frozen period with no row
--                                 fails closed (period.ErrNoControlSnapshot).
--
--   source                        'freeze'             written by Freeze.
--                                 'migration_backfill' written below for a
--                                                      period frozen before
--                                                      this migration.
--                                 'seed'               written by the demo
--                                                      seed and SQL fixtures,
--                                                      which insert frozen
--                                                      periods directly.
--
-- Immutability: atlas_app holds SELECT and INSERT only, and RLS has no
-- UPDATE or DELETE policy. The trigger refuses UPDATE for every role, and
-- DELETE while the row's audit period still exists. Deleting the period
-- itself cascades here; that is the one way a row goes away, and with the
-- period gone there is nothing left to verify.
--
-- Upgrade for already-frozen periods: section 3 backfills each from the
-- tenant's catalog as it stands at migration time, marked
-- 'migration_backfill'. For such a period verification is exactly as strong
-- as before this migration: if its catalog has not changed since the
-- freeze, replay matches and from now on stays matched; if it changed
-- before the upgrade, replay already mismatched and still does, and the
-- source column says the snapshot is not freeze-time. The hash definition
-- (canonical JSON, six keys) is unchanged, so no stored hash changes.
--
-- Constitutional invariants:
--   #2  the backfill writes only this new table; no evidence or period row.
--   #10 the original freeze replays independent of later catalog changes.
--
-- Reversible via 20261311000000_audit_period_frozen_controls.down.sql.

-- ===== 1. the table =====

CREATE TABLE audit_period_frozen_controls (
    tenant_id       UUID NOT NULL,
    audit_period_id UUID NOT NULL,
    control_ids     UUID[] NOT NULL,
    source          TEXT NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

    PRIMARY KEY (tenant_id, audit_period_id),
    CONSTRAINT audit_period_frozen_controls_source_chk
        CHECK (source IN ('freeze', 'migration_backfill', 'seed')),
    CONSTRAINT audit_period_frozen_controls_no_null_ids
        CHECK (array_position(control_ids, NULL) IS NULL),
    FOREIGN KEY (tenant_id, audit_period_id)
        REFERENCES audit_periods(tenant_id, id) ON DELETE CASCADE
);

ALTER TABLE audit_period_frozen_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_period_frozen_controls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON audit_period_frozen_controls
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON audit_period_frozen_controls
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON audit_period_frozen_controls TO atlas_app;

-- ===== 2. write-once =====

CREATE OR REPLACE FUNCTION audit_period_frozen_controls_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'frozen control set of audit period % cannot change', OLD.audit_period_id
            USING ERRCODE = 'check_violation';
    END IF;
    -- DELETE: allowed only once the period is gone, i.e. the ON DELETE
    -- CASCADE from audit_periods.
    IF EXISTS (SELECT 1 FROM audit_periods ap
                WHERE ap.tenant_id = OLD.tenant_id AND ap.id = OLD.audit_period_id) THEN
        RAISE EXCEPTION 'frozen control set of audit period % cannot be deleted while the period exists', OLD.audit_period_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS audit_period_frozen_controls_immutable_trg ON audit_period_frozen_controls;
CREATE TRIGGER audit_period_frozen_controls_immutable_trg
    BEFORE UPDATE OR DELETE ON audit_period_frozen_controls
    FOR EACH ROW
    EXECUTE FUNCTION audit_period_frozen_controls_immutable();

-- ===== 3. backfill already-frozen periods =====
--
-- Runs as atlas_migrate, which is BYPASSRLS, so one statement covers every
-- tenant. The NOT EXISTS makes it idempotent; the integration test
-- (internal/audit/period/frozen_controls_integration_test.go) runs this
-- section verbatim against a period whose snapshot it removed.

INSERT INTO audit_period_frozen_controls (tenant_id, audit_period_id, control_ids, source)
SELECT ap.tenant_id, ap.id,
       ARRAY(SELECT c.id FROM controls c WHERE c.tenant_id = ap.tenant_id ORDER BY c.id),
       'migration_backfill'
FROM audit_periods ap
WHERE ap.status = 'frozen'
  AND NOT EXISTS (SELECT 1 FROM audit_period_frozen_controls fc
                   WHERE fc.tenant_id = ap.tenant_id AND fc.audit_period_id = ap.id);
