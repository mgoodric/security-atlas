-- security-atlas -- #3094: TRUNCATE cannot remove a frozen audit's rows.
--
-- TRUNCATE fires no row trigger, so it walks past every guard #3050, #3066
-- and #3081 put on a frozen period: the UPDATE guards on audit_periods and
-- populations, the write-once guard on audit_period_frozen_controls, the
-- chain guard on audit_period_freeze_revisions, and the DELETE guard on
-- audit_periods. TRUNCATE ... CASCADE reaches these tables from far away:
-- TRUNCATE controls CASCADE truncates populations, and TRUNCATE frameworks
-- or framework_versions CASCADE truncates all four (measured from the
-- psql NOTICEs; #3094 decisions log D1).
--
--   audit_period_frozen_no_truncate_trg   BEFORE TRUNCATE, FOR EACH
--                                         STATEMENT, on each of the four
--                                         tables. Refuses the TRUNCATE
--                                         while the table holds a frozen
--                                         audit's rows:
--
--     audit_periods                  a period with status = 'frozen'
--     populations                    a population with frozen_at set, or
--                                    attached to a frozen period
--     audit_period_freeze_revisions  any row (a revision exists only for
--                                    a frozen period)
--     audit_period_frozen_controls   any row (a snapshot exists only for
--                                    a frozen period)
--
-- A table with no such rows truncates as before, so a TRUNCATE of a
-- database with no frozen audit (a fresh CI database, a scratch cluster)
-- is unaffected. The trigger fires for every table a CASCADE reaches, so
-- one frozen period anywhere refuses the whole statement, and nothing is
-- truncated.
--
-- Only the owner holds TRUNCATE. The owner path past the guard is the one
-- #3081 set for DELETE: disable the trigger inside the owner's own
-- transaction (ALTER TABLE ... DISABLE TRIGGER is transactional, so no
-- other session sees it disabled), truncate, and enable it again. No
-- product code truncates these tables, so no helper wraps that.
--
-- The function is SECURITY DEFINER, owned by atlas_migrate (BYPASSRLS),
-- with a pinned search_path, for the reason #3085 N-a gave: it must see
-- every tenant's rows whatever role or tenant context the truncating
-- session holds. It takes no input but TG_TABLE_NAME, returns nothing, and
-- PUBLIC holds no EXECUTE on it; firing a trigger does not check EXECUTE.
--
-- Constitutional invariants:
--   #2  nothing here writes a row; the trigger only refuses.
--   #10 a frozen period's commitment, samples, revisions and control
--       snapshot outlive any TRUNCATE.
--
-- Reversible via 20261316000000_audit_period_frozen_no_truncate.down.sql.

CREATE OR REPLACE FUNCTION audit_period_frozen_no_truncate()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp
AS $$
DECLARE
    frozen BOOLEAN;
BEGIN
    CASE TG_TABLE_NAME
    WHEN 'audit_periods' THEN
        frozen := EXISTS (SELECT 1 FROM audit_periods WHERE status = 'frozen');
    WHEN 'populations' THEN
        frozen := EXISTS (SELECT 1 FROM populations p
                           WHERE p.frozen_at IS NOT NULL
                              OR EXISTS (SELECT 1 FROM audit_periods ap
                                          WHERE ap.tenant_id = p.tenant_id AND ap.id = p.audit_period_id
                                            AND ap.status = 'frozen'));
    WHEN 'audit_period_freeze_revisions' THEN
        frozen := EXISTS (SELECT 1 FROM audit_period_freeze_revisions);
    WHEN 'audit_period_frozen_controls' THEN
        frozen := EXISTS (SELECT 1 FROM audit_period_frozen_controls);
    ELSE
        -- Attached to a table this function does not know: fail closed.
        frozen := TRUE;
    END CASE;
    IF frozen THEN
        RAISE EXCEPTION '% holds a frozen audit''s rows: TRUNCATE is refused (#3094)', TG_TABLE_NAME
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

ALTER FUNCTION audit_period_frozen_no_truncate() OWNER TO atlas_migrate;
REVOKE EXECUTE ON FUNCTION audit_period_frozen_no_truncate() FROM PUBLIC;

DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON audit_periods;
CREATE TRIGGER audit_period_frozen_no_truncate_trg
    BEFORE TRUNCATE ON audit_periods
    FOR EACH STATEMENT
    EXECUTE FUNCTION audit_period_frozen_no_truncate();

DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON populations;
CREATE TRIGGER audit_period_frozen_no_truncate_trg
    BEFORE TRUNCATE ON populations
    FOR EACH STATEMENT
    EXECUTE FUNCTION audit_period_frozen_no_truncate();

DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON audit_period_freeze_revisions;
CREATE TRIGGER audit_period_frozen_no_truncate_trg
    BEFORE TRUNCATE ON audit_period_freeze_revisions
    FOR EACH STATEMENT
    EXECUTE FUNCTION audit_period_frozen_no_truncate();

DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON audit_period_frozen_controls;
CREATE TRIGGER audit_period_frozen_no_truncate_trg
    BEFORE TRUNCATE ON audit_period_frozen_controls
    FOR EACH STATEMENT
    EXECUTE FUNCTION audit_period_frozen_no_truncate();
