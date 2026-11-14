-- security-atlas — #2101: one approval rule, an organisation self-approval
-- opt-in, and a visible self-approved flag. ADR-0024.
--
-- The rule (internal/approval): only a person approves, and never the
-- person who requested the approval. An organisation may turn on "Allow
-- self-approval"; an approval that relies on it is flagged self_approved.
--
--   1. tenants.allow_self_approval        the setting, default off. Same
--      home as bundle_gate_mode / risk_review_cadence_days: `tenants` is
--      the per-tenant settings row, already under FORCE RLS (invariant #6).
--   2. self_approved on policies, framework_scopes and exceptions.
--   3. framework_scopes.submitted_by      the person who submitted the
--      scope for review, so approval has someone to compare. Rows already
--      in review are backfilled from the workflow log's last 'submit'
--      actor; a 'key_<uuid>' actor bound to a user (api_keys.issued_by)
--      is mapped to that user. A row left NULL, or holding a 'jwt:'
--      session id, must be re-submitted before it can be approved.
--   4. exceptions_sod is relaxed to `approved_by <> requested_by OR
--      self_approved`. A CHECK cannot read tenants at write time, so the
--      setting is denormalised onto the row: the application sets
--      self_approved only after reading allow_self_approval in the same
--      transaction. The CHECK still refuses a same-person approval that
--      does not carry the flag, so a write that skips the application
--      rule cannot hide a self-approval. exceptions_denied_by_sod is
--      unchanged: the opt-in covers approval only. A second CHECK,
--      exceptions_self_approved_has_approver, keeps the flag off a row
--      nobody approved.
--   7. self_approval_requires_setting, a BEFORE INSERT OR UPDATE trigger
--      on policies, framework_scopes and exceptions: a row may newly
--      become self_approved only while its tenant's allow_self_approval
--      is on. It checks the setting alone; who may approve stays in Go
--      (internal/approval). With it, a direct write that skips the
--      application cannot mark a self-approval the organisation has not
--      allowed.
--   5. Audit actions: me_audit_log gains 'tenant_self_approval_update'
--      (the setting change); policy_audit_log gains 'self_approved' (one
--      row per self-approved policy). Every existing value is kept. The
--      framework-scope workflow log carries self_approved in its row
--      images, and an exception's 'approved' audit row names it in reason.
--   6. Both framework-scope approval-reset triggers also clear
--      self_approved, with the approver they already clear.
--
-- Reversible via 20261031000000_self_approval.down.sql.

-- ===== 1. the setting =====

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS allow_self_approval BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN tenants.allow_self_approval IS
    '#2101: when true, the requester of a policy, framework-scope or exception approval may approve it themselves. Such approvals are flagged self_approved. Default false; admin-only; changes are written to me_audit_log.';

-- ===== 2 + 3. the flag and the scope submitter =====

ALTER TABLE policies
    ADD COLUMN IF NOT EXISTS self_approved BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE framework_scopes
    ADD COLUMN IF NOT EXISTS self_approved BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS submitted_by TEXT NULL;

ALTER TABLE exceptions
    ADD COLUMN IF NOT EXISTS self_approved BOOLEAN NOT NULL DEFAULT false;

-- The backfill is a function so its mapping can be tested
-- (internal/frameworkscope/self_approval_integration_test.go); it touches
-- only review rows with no submitter, so a second call changes nothing.
-- The workflow log's actor is a credential id: a 'key_<uuid>' key bound to
-- a user (api_keys.issued_by) maps to that user; anything else is kept
-- verbatim, and a 'jwt:' session id is then refused until re-submitted.
-- The CASE guards the uuid cast, which a WHERE clause would not order.
CREATE OR REPLACE FUNCTION framework_scopes_backfill_submitted_by()
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
    n BIGINT;
BEGIN
    UPDATE framework_scopes fs
    SET submitted_by = (
        SELECT COALESCE(
            (SELECT k.issued_by::text
             FROM api_keys k
             WHERE k.tenant_id = fs.tenant_id
               AND k.issued_by IS NOT NULL
               AND k.id = CASE
                   WHEN l.actor ~ '^key_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
                   THEN substr(l.actor, 5)::uuid
               END),
            l.actor)
        FROM framework_scope_workflow_log l
        WHERE l.tenant_id = fs.tenant_id
          AND l.framework_scope_id = fs.id
          AND l.action = 'submit'
        ORDER BY l.occurred_at DESC, l.id DESC
        LIMIT 1)
    WHERE fs.state = 'review'
      AND fs.submitted_by IS NULL;
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END;
$$;

SELECT framework_scopes_backfill_submitted_by();

-- ===== 4. exceptions segregation of duties =====

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_sod;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_sod
    CHECK (approved_by IS NULL OR approved_by <> requested_by OR self_approved);

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_self_approved_has_approver;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_self_approved_has_approver
    CHECK (NOT self_approved OR approved_by IS NOT NULL);

-- ===== 5. audit actions =====

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
        'tenant_risk_review_cadence_update',
        -- #2101 (organisation Allow self-approval changed):
        'tenant_self_approval_update'
    ));

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved'));

-- ===== 6. approval resets clear the flag =====

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
        NEW.self_approved := false;
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
           self_approved = false,
           updated_at = now()
     WHERE id = scope_id
       AND state IN ('review', 'approved');

    RETURN NULL;
END;
$$;

-- ===== 7. the setting, enforced in the database =====
--
-- Fires when a row newly becomes self_approved (an INSERT carrying the
-- flag, or an UPDATE turning it on). The lookup runs as the writing role:
-- under atlas_app RLS shows only the current tenant's row, and a missing
-- or invisible row reads as the default, off, so the write is refused.
-- Rows already flagged are left alone, so turning the setting off later
-- does not block unrelated updates to them.

CREATE OR REPLACE FUNCTION self_approval_requires_setting()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    newly_flagged BOOLEAN;
BEGIN
    IF TG_OP = 'INSERT' THEN
        newly_flagged := NEW.self_approved;
    ELSE
        newly_flagged := NEW.self_approved AND NOT COALESCE(OLD.self_approved, false);
    END IF;
    IF newly_flagged AND NOT COALESCE(
        (SELECT t.allow_self_approval FROM tenants t WHERE t.id = NEW.tenant_id),
        false) THEN
        RAISE EXCEPTION '%: self_approved requires the organisation''s Allow self-approval setting (#2101)', TG_TABLE_NAME
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS policies_self_approval_requires_setting_trg ON policies;
CREATE TRIGGER policies_self_approval_requires_setting_trg
    BEFORE INSERT OR UPDATE ON policies
    FOR EACH ROW
    EXECUTE FUNCTION self_approval_requires_setting();

DROP TRIGGER IF EXISTS framework_scopes_self_approval_requires_setting_trg ON framework_scopes;
CREATE TRIGGER framework_scopes_self_approval_requires_setting_trg
    BEFORE INSERT OR UPDATE ON framework_scopes
    FOR EACH ROW
    EXECUTE FUNCTION self_approval_requires_setting();

DROP TRIGGER IF EXISTS exceptions_self_approval_requires_setting_trg ON exceptions;
CREATE TRIGGER exceptions_self_approval_requires_setting_trg
    BEFORE INSERT OR UPDATE ON exceptions
    FOR EACH ROW
    EXECUTE FUNCTION self_approval_requires_setting();
