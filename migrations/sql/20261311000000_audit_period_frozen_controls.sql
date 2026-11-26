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
--                                 'migration_backfill' written by section 6
--                                                      for a period frozen
--                                                      before this migration.
--                                 'seed'               written by the demo
--                                                      seed and SQL fixtures,
--                                                      which insert frozen
--                                                      periods directly.
--
-- Guards (sections 2, 3 and 5):
--   write-once     atlas_app holds SELECT and INSERT only, and RLS has no
--                  UPDATE or DELETE policy. A trigger refuses UPDATE for
--                  every role, and DELETE while the row's period exists.
--                  Deleting the period cascades here.
--   insert guard   a row is accepted only for a period that is frozen, and
--                  a source other than 'freeze' only from a superuser or
--                  BYPASSRLS role (atlas_migrate: the backfill, the demo
--                  seed and the SQL fixtures). atlas_app writes 'freeze'.
--   commit check   a deferred constraint trigger refuses any commit that
--                  leaves a frozen period without its row. A binary that
--                  predates this migration freezes without writing one, so
--                  in the window between this migration and the new binary
--                  such a freeze fails at commit instead of leaving a period
--                  that can never be replayed (#3077 review B1). Deploy the
--                  migration and the binary together.
--
-- Upgrade for already-frozen periods: section 6 backfills each from the
-- tenant's catalog as it stands at migration time, marked
-- 'migration_backfill'. For such a period verification is exactly as strong
-- as before this migration: if its catalog has not changed since the
-- freeze, replay matches and from now on stays matched; if it changed
-- before the upgrade, replay already mismatched and still does, and the
-- source column says the snapshot is not freeze-time. The hash definition
-- (canonical JSON, six keys) is unchanged, so no stored hash changes.
-- Detection and recovery: docs/runbooks/audit-period-frozen-controls.md.
--
-- No function here is SECURITY DEFINER. Each trigger reads audit_periods
-- or audit_period_frozen_controls as the role that fired it, under that
-- role's RLS. That is sound because the row being checked belongs to the
-- tenant the writer's RLS context already admits (the INSERT/UPDATE WITH
-- CHECK policies require it), and a BYPASSRLS writer sees every row.
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
    -- CASCADE from audit_periods. Read as the deleter (INVOKER): atlas_app
    -- holds no DELETE grant, so only the cascade (run as the table owner)
    -- or an owner/BYPASSRLS role reaches this check, and both see the
    -- period row whatever the tenant GUC.
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

-- ===== 3. insert guard =====

CREATE OR REPLACE FUNCTION audit_period_frozen_controls_insert_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- Read as the inserter (INVOKER): RLS WITH CHECK already requires the
    -- row's tenant to be the inserter's, so the period is visible to it.
    IF NOT EXISTS (SELECT 1 FROM audit_periods ap
                    WHERE ap.tenant_id = NEW.tenant_id AND ap.id = NEW.audit_period_id
                      AND ap.status = 'frozen') THEN
        RAISE EXCEPTION 'audit period % is not frozen: it has no control set to record', NEW.audit_period_id
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.source <> 'freeze'
       AND NOT EXISTS (SELECT 1 FROM pg_roles r
                        WHERE r.rolname = current_user AND (r.rolsuper OR r.rolbypassrls)) THEN
        RAISE EXCEPTION 'source % is reserved for the migration and the seed; % may record only freeze', NEW.source, current_user
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS audit_period_frozen_controls_insert_guard_trg ON audit_period_frozen_controls;
CREATE TRIGGER audit_period_frozen_controls_insert_guard_trg
    BEFORE INSERT ON audit_period_frozen_controls
    FOR EACH ROW
    EXECUTE FUNCTION audit_period_frozen_controls_insert_guard();

-- ===== 4. record a period's control set from the catalog =====
--
-- The one shape every writer that is not Freeze uses: the backfill, the
-- demo seed and the SQL fixtures. Freeze records the exact list it hashed
-- instead. ON CONFLICT DO NOTHING keeps reruns (fixtures, the backfill)
-- idempotent.

CREATE OR REPLACE FUNCTION audit_period_record_catalog_controls(
    p_tenant_id UUID, p_audit_period_id UUID, p_source TEXT)
RETURNS VOID
LANGUAGE sql
AS $$
    INSERT INTO audit_period_frozen_controls (tenant_id, audit_period_id, control_ids, source)
    VALUES (p_tenant_id, p_audit_period_id,
            ARRAY(SELECT c.id FROM controls c WHERE c.tenant_id = p_tenant_id ORDER BY c.id),
            p_source)
    ON CONFLICT DO NOTHING;
$$;

-- ===== 5. commit check: a frozen period has its control set =====

CREATE OR REPLACE FUNCTION audit_periods_frozen_has_controls()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- Deferred to commit, so re-read the row: it may have been reopened or
    -- deleted later in the same transaction. Read as the committer
    -- (INVOKER), whose RLS context admits the row it wrote.
    IF EXISTS (SELECT 1 FROM audit_periods ap
                WHERE ap.tenant_id = NEW.tenant_id AND ap.id = NEW.id AND ap.status = 'frozen')
       AND NOT EXISTS (SELECT 1 FROM audit_period_frozen_controls fc
                        WHERE fc.tenant_id = NEW.tenant_id AND fc.audit_period_id = NEW.id) THEN
        RAISE EXCEPTION 'audit period % is frozen without a recorded control set (#3066); is the binary older than migration 20261311000000?', NEW.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS audit_periods_frozen_has_controls_trg ON audit_periods;
CREATE CONSTRAINT TRIGGER audit_periods_frozen_has_controls_trg
    AFTER INSERT OR UPDATE ON audit_periods
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION audit_periods_frozen_has_controls();

-- ===== 6. backfill already-frozen periods =====
--
-- Runs as atlas_migrate, which is BYPASSRLS, so one statement covers every
-- tenant, and idempotent. This is also the recovery for a frozen period
-- found without a row (docs/runbooks/audit-period-frozen-controls.md). The
-- integration test (internal/audit/period/frozen_controls_integration_test.go)
-- runs this section verbatim.

SELECT audit_period_record_catalog_controls(ap.tenant_id, ap.id, 'migration_backfill')
FROM audit_periods ap
WHERE ap.status = 'frozen';
