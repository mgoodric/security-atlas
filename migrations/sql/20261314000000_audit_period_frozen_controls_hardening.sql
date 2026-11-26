-- security-atlas -- #3085: harden the frozen-controls snapshot guards.
--
-- Three follow-ups from the #3077 re-review to migration
-- 20261311000000_audit_period_frozen_controls (#3066).
--
--   N-a  audit_periods_frozen_has_controls() becomes SECURITY DEFINER,
--        owned by atlas_migrate (BYPASSRLS), with a pinned search_path.
--        It ran as the committer, under the committer's RLS. An atlas_app
--        session could freeze a period, then clear or switch
--        app.current_tenant before COMMIT: the deferred check no longer saw
--        the row and let the commit through. Run as the owner, it sees the
--        row whatever the session's tenant context. It reads only
--        audit_periods and audit_period_frozen_controls, by NEW's keys, and
--        takes no input but NEW, so it hands the caller nothing.
--
--   N-b  audit_period_frozen_controls_insert_guard() accepts a source other
--        than 'freeze' only when both current_user and session_user are
--        superuser or BYPASSRLS. current_user alone passed for
--        atlas_service_account (BYPASSRLS), which atlas_app reaches with
--        SET LOCAL ROLE (bootstrap 01-roles.sql). The gap is latent: that
--        role holds no INSERT on the table. session_user is the login role
--        and SET ROLE does not change it, so an atlas_app session never
--        passes.
--
--   N-c  audit_period_record_catalog_controls(UUID, UUID, TEXT) loses
--        PUBLIC EXECUTE. Its callers are atlas_migrate (the backfill, the
--        demo seed, the SQL fixtures, dbtest.FreezeRaw) or a superuser, so
--        atlas_migrate alone is granted it. It stays SECURITY INVOKER, so
--        the insert guard still judges the real caller.
--
-- Pinned search_path is pg_catalog, public, pg_temp: pg_temp last, so a
-- temporary table cannot shadow public.audit_periods inside the definer.
--
-- Constitutional invariants:
--   #2  nothing here writes a row; the functions only refuse.
--   #10 a freeze cannot commit without its control set, whatever tenant
--       context the committing session holds.
--
-- Reversible via 20261314000000_audit_period_frozen_controls_hardening.down.sql.

-- ===== N-a. commit check runs as the owner =====

CREATE OR REPLACE FUNCTION audit_periods_frozen_has_controls()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp
AS $$
BEGIN
    -- Deferred to commit, so re-read the row: it may have been reopened or
    -- deleted later in the same transaction. Read as the owner
    -- (atlas_migrate, BYPASSRLS), so the committer's tenant GUC cannot hide
    -- the row (#3085 N-a).
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

ALTER FUNCTION audit_periods_frozen_has_controls() OWNER TO atlas_migrate;

-- ===== N-b. insert guard also checks session_user =====

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
    -- Both the current and the login role: SET ROLE changes current_user
    -- only, so an atlas_app session that switches to a BYPASSRLS role it is
    -- a member of still fails (#3085 N-b).
    IF NEW.source <> 'freeze'
       AND NOT (EXISTS (SELECT 1 FROM pg_roles r
                         WHERE r.rolname = current_user AND (r.rolsuper OR r.rolbypassrls))
                AND EXISTS (SELECT 1 FROM pg_roles r
                             WHERE r.rolname = session_user AND (r.rolsuper OR r.rolbypassrls))) THEN
        RAISE EXCEPTION 'source % is reserved for the migration and the seed; % (session %) may record only freeze', NEW.source, current_user, session_user
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

-- ===== N-c. record function: atlas_migrate only =====

REVOKE EXECUTE ON FUNCTION audit_period_record_catalog_controls(UUID, UUID, TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit_period_record_catalog_controls(UUID, UUID, TEXT) TO atlas_migrate;
