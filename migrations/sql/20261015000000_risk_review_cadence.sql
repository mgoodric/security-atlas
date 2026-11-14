-- #2059: a risk's review_due_at defaults from an organisation review cadence.
--
-- Before this, review_due_at was stored only when the caller sent it. The
-- new-risk form never did and nothing computed it, so every register row read
-- "Pending evaluation" under Review due.
--
--   1. tenants.risk_review_cadence_days: the organisation's risk review
--      cadence, default 365, settable via PATCH /v1/tenants/{id} beside
--      bundle_gate_mode.
--   2. A BEFORE INSERT trigger on risks: a risk inserted without a review
--      date gets created_at + cadence. It sits on the table, not in one Go
--      path, because risks are inserted by the store, the aggregation path,
--      the MCP write-proposal applier and the demo seed. An explicit date is
--      kept. Changing the cadence later touches no existing row, so it
--      affects new risks only.
--   3. Backfill: existing risks with no review date get created_at + their
--      tenant's cadence (365 for a bare-UUID tenant with no tenants row).
--   4. me_audit_log.action gains 'tenant_risk_review_cadence_update'; the
--      list below mirrors the #1944 form (20260920000000) plus the new value.
--
-- Reversible via 20261015000000_risk_review_cadence.down.sql. The down does
-- not un-backfill: the backfilled dates are indistinguishable from dates a
-- user set, and a schema rollback does not delete data.

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS risk_review_cadence_days INTEGER NOT NULL DEFAULT 365;

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_risk_review_cadence_days_chk;

ALTER TABLE tenants
    ADD CONSTRAINT tenants_risk_review_cadence_days_chk
    CHECK (risk_review_cadence_days BETWEEN 1 AND 3650);

COMMENT ON COLUMN tenants.risk_review_cadence_days IS
    '#2059: days between a risk''s creation and its default review_due_at. Default 365, range 1..3650. Set via PATCH /v1/tenants/{id}; a change affects new risks only.';

-- ===== default review_due_at on insert =====
--
-- The trigger runs as the inserting role under its tenant GUC, so RLS on
-- tenants shows it only its own row. No row (a bare-UUID tenant predating
-- slice 144) falls back to the column default, 365.
CREATE OR REPLACE FUNCTION risks_review_due_default()
RETURNS TRIGGER AS $$
DECLARE
    v_days INTEGER;
BEGIN
    IF NEW.review_due_at IS NULL THEN
        SELECT risk_review_cadence_days INTO v_days
        FROM tenants
        WHERE id = NEW.tenant_id;
        NEW.review_due_at := NEW.created_at + make_interval(days => COALESCE(v_days, 365));
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS risks_review_due_default_trg ON risks;

CREATE TRIGGER risks_review_due_default_trg
    BEFORE INSERT ON risks
    FOR EACH ROW
    EXECUTE FUNCTION risks_review_due_default();

-- ===== backfill =====

UPDATE risks r
SET review_due_at = r.created_at + make_interval(days => COALESCE(
    (SELECT t.risk_review_cadence_days FROM tenants t WHERE t.id = r.tenant_id),
    365))
WHERE r.review_due_at IS NULL;

-- ===== me_audit_log.action CHECK extension =====

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
        -- #2059 (organisation risk review cadence changed):
        'tenant_risk_review_cadence_update'
    ));
