-- security-atlas — slice 143: create-tenant flow (DOWN).
--
-- Reverses 20260522000000_tenants_slug_create_flow.sql by:
--
--   1. Restoring `super_admin_audit_log.action` CHECK to the slice-142
--      two-value shape ('super_admin_grant', 'super_admin_revoke').
--   2. Restoring `me_audit_log.action` CHECK to the slice-142 18-value
--      shape (dropping 'tenant_create').
--   3. Dropping `idx_tenants_slug_unique`.
--   4. Dropping `tenants.created_by_user_id`.
--   5. Dropping `tenants.slug`.
--
-- The order matters: drop dependencies before the columns they
-- reference. The CHECK constraints reference no columns of this
-- migration's new objects so the ordering above is conservative
-- rather than strictly required.

-- ===== 0. users_idp_principal_unique restore to global =====
--
-- Reverses the slice-143 per-tenant relaxation.

DROP INDEX IF EXISTS users_idp_principal_unique;

CREATE UNIQUE INDEX users_idp_principal_unique
    ON users (idp_issuer, idp_subject)
    WHERE idp_issuer <> '' AND idp_subject <> '';

-- ===== 1. me_audit_log.action CHECK restore =====

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
        'evidence_export',
        'policies_export',
        'exceptions_export',
        'samples_export',
        'anchors_export',
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke'
    ));

-- ===== 2. super_admin_audit_log.action CHECK restore =====

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN ('super_admin_grant', 'super_admin_revoke'));

-- ===== 3. tenants.slug partial UNIQUE index =====

DROP INDEX IF EXISTS idx_tenants_slug_unique;

-- ===== 4. tenants.created_by_user_id =====

ALTER TABLE tenants
    DROP COLUMN IF EXISTS created_by_user_id;

-- ===== 5. tenants.slug =====

ALTER TABLE tenants
    DROP COLUMN IF EXISTS slug;
