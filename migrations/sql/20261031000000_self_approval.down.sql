-- Reverse #2101 (20261031000000_self_approval.sql).
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'self_approved' row exists (the #2009 rule). Any
-- 'tenant_self_approval_update' me_audit_log rows must likewise be archived
-- and removed by the operator first, or the narrowed CHECK refuses to apply.
--
-- Restoring the strict exceptions_sod fails while an exception carries a
-- self-approval; that row is audit evidence and is not rewritten here.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log WHERE action = 'self_approved';
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % self_approved row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261031000000.';
    END IF;
END
$$;

DROP TRIGGER IF EXISTS exceptions_self_approval_requires_setting_trg ON exceptions;
DROP TRIGGER IF EXISTS framework_scopes_self_approval_requires_setting_trg ON framework_scopes;
DROP TRIGGER IF EXISTS policies_self_approval_requires_setting_trg ON policies;
DROP FUNCTION IF EXISTS self_approval_requires_setting();
DROP FUNCTION IF EXISTS framework_scopes_backfill_submitted_by();

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_self_approved_has_approver;

CREATE OR REPLACE FUNCTION framework_scopes_bounce_on_predicate_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.predicate_hash IS DISTINCT FROM OLD.predicate_hash
       AND OLD.state IN ('review', 'approved') THEN
        NEW.state := 'draft';
        NEW.approver_user_id := NULL;
        NEW.approved_at := NULL;
        NEW.predicate_hash_at_approval := NULL;
        NEW.approval_evidence_file_url := NULL;
        NEW.approval_evidence_file_hash := NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION framework_scope_applicability_reset_approval()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    scope_id UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        scope_id := OLD.framework_scope_id;
    ELSE
        scope_id := NEW.framework_scope_id;
    END IF;

    UPDATE framework_scopes
       SET state = 'draft',
           approver_user_id = NULL,
           approved_at = NULL,
           predicate_hash_at_approval = NULL,
           approval_evidence_file_url = NULL,
           approval_evidence_file_hash = NULL,
           updated_at = now()
     WHERE id = scope_id
       AND state IN ('review', 'approved');

    RETURN NULL;
END;
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded'));

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified',
        'audit_log_export',
        'audit_periods_export',
        'vendors_export',
        'risk_export',
        'controls_export',
        'controls_history_export',
        'evidence_export',
        'policies_export',
        'exceptions_export',
        'samples_export',
        'anchors_export',
        'dashboard_export',
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        'demo_seed_apply',
        'demo_seed_teardown',
        'demo_seed',
        'demo_teardown',
        'authz_bundle_reload',
        'user_tenant_assign',
        'user_tenant_revoke',
        'tenant_gate_policy_update',
        'oidc_identity_link',
        'tenant_risk_review_cadence_update'
    ));

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_sod;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_sod
    CHECK (approved_by IS NULL OR approved_by <> requested_by);

ALTER TABLE exceptions
    DROP COLUMN IF EXISTS self_approved;

ALTER TABLE framework_scopes
    DROP COLUMN IF EXISTS submitted_by,
    DROP COLUMN IF EXISTS self_approved;

ALTER TABLE policies
    DROP COLUMN IF EXISTS self_approved;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS allow_self_approval;
