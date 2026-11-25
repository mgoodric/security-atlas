-- security-atlas -- #3081: a frozen audit period cannot be deleted.
--
-- atlas_app holds DELETE on audit_periods, under the tenant_delete RLS
-- policy (20260511000020_audit_periods.sql). #3050's
-- audit_periods_freeze_immutable_trg guards UPDATE only. A frozen period
-- with no ON DELETE RESTRICT children (populations, audit notes,
-- walkthroughs, freeze revisions, historical imports) could therefore be
-- deleted by the app, which erased its frozen_hash and cascaded away the
-- control set its freeze recorded (audit_period_frozen_controls, #3066).
-- No product code deletes a period today.
--
--   audit_periods_frozen_no_delete   BEFORE DELETE on audit_periods.
--                                    Refuses a row with status = 'frozen',
--                                    for every role, the owner included.
--                                    An open period can still be deleted
--                                    by any role that could before.
--
-- Why a trigger and not REVOKE DELETE FROM atlas_app: a revoke leaves the
-- owner and every BYPASSRLS role free to delete a frozen period by a stray
-- statement, and #3050 already set the rule that the original freeze is
-- immutable for every role. Teardown that must remove frozen periods (the
-- demo teardown, integration-test cleanups) runs as the owner and disables
-- the trigger inside its own transaction, the same way the reopen runbook
-- disables audit_periods_freeze_immutable_trg. See
-- docs/runbooks/audit-period-reopen.md.
--
-- audit_period_frozen_controls keeps ON DELETE CASCADE. With frozen deletes
-- refused, the cascade runs only when the owner has disabled this trigger
-- to tear a period down, and removing the snapshot with its period is what
-- that teardown wants. RESTRICT would add no protection the trigger does
-- not already give, and would make every teardown also disable
-- audit_period_frozen_controls_immutable_trg and delete the snapshot by
-- hand first.
--
-- Side effect: the unbound-rows purge (internal/auth/oauthclient) counts a
-- BEFORE DELETE row trigger as an append-only signal, so it now retains
-- all-zeros-tenant audit_periods rows, and the rows they reference, rather
-- than deleting them. That fails closed.
--
-- The function is not SECURITY DEFINER and reads nothing but OLD.
--
-- Constitutional invariants:
--   #2  nothing here writes a row; the trigger only refuses.
--   #10 a frozen period's commitment and control snapshot outlive any
--       application DELETE.
--
-- Reversible via 20261313000000_audit_period_frozen_no_delete.down.sql.

CREATE OR REPLACE FUNCTION audit_periods_frozen_no_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'frozen' THEN
        RAISE EXCEPTION 'audit period % is frozen: a frozen period cannot be deleted', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS audit_periods_frozen_no_delete_trg ON audit_periods;
CREATE TRIGGER audit_periods_frozen_no_delete_trg
    BEFORE DELETE ON audit_periods
    FOR EACH ROW
    EXECUTE FUNCTION audit_periods_frozen_no_delete();
