-- security-atlas -- reverse 20261314000000_audit_period_frozen_controls_hardening.sql.
--
-- Restores the 20261311000000 definitions: the commit check runs as the
-- committer under its RLS, the insert guard checks current_user only, and
-- audit_period_record_catalog_controls is executable by PUBLIC again. Down
-- is for development only.

GRANT EXECUTE ON FUNCTION audit_period_record_catalog_controls(UUID, UUID, TEXT) TO PUBLIC;
REVOKE EXECUTE ON FUNCTION audit_period_record_catalog_controls(UUID, UUID, TEXT) FROM atlas_migrate;

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

-- CREATE OR REPLACE assigns every property the command states or implies,
-- so this drops SECURITY DEFINER and the pinned search_path. Ownership is
-- kept.
CREATE OR REPLACE FUNCTION audit_periods_frozen_has_controls()
RETURNS trigger
LANGUAGE plpgsql
SECURITY INVOKER
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
