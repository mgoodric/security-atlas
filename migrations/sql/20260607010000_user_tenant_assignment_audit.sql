-- security-atlas — slice 478: user↔tenant↔role assignment audit actions.
--
-- Slice 478 ships the super-admin-gated REST surface that creates + revokes
-- user↔tenant memberships and their roles (including super-admin
-- self-assignment). The membership write itself reuses the existing `users`
-- (per-tenant membership row) + `user_roles` tables — NO new table is added
-- (see docs/audit-log/478-user-tenant-assignment-decisions.md D1/D2). The
-- ONLY schema change this slice needs is to admit two new audit-log action
-- values so the assign/revoke handlers can dual-write the slice-142
-- super_admin_audit_log + me_audit_log forensic record (AC-7).
--
-- New action values:
--   - 'user_tenant_assign'  — written on a successful assign (membership +
--     role grant), including super-admin self-assign.
--   - 'user_tenant_revoke'  — written on a successful revoke (role removal,
--     optional membership soft-disable).
--
-- Both values extend the me_audit_log.action CHECK (every assign/revoke
-- writes a tenant-scoped me_audit_log row anchored to the actor's session
-- tenant, surfaced by the slice-124 unified aggregator via the existing
-- kind='me' branch) and the super_admin_audit_log.action CHECK (the
-- super-admin cross-tenant path also writes a platform-global forensic row).
--
-- LOAD-BEARING NOTE — local-auth identity (slice 478 D1, NOT a schema change
-- here): a local user assigned to a SECOND tenant gets a synthetic, stable,
-- per-user IdP tuple (idp_issuer='urn:atlas:local', idp_subject=<origin
-- users.id>) written to both the new membership row and (backfill) the origin
-- row. This is a DATA write performed by the handler, flowing through the
-- EXISTING users_idp_principal_unique partial index (active only when both
-- idp_issuer<>'' AND idp_subject<>''). No index/column change is needed — the
-- synthetic tuple is non-empty so it satisfies the existing partial index and
-- the slice-192 resolver's non-empty guard, and is unique per local user so it
-- can NEVER over-match another local user's empty-tuple rows (P0-478-2).
--
-- CONSTITUTIONAL INVARIANTS:
--   #6  Tenant isolation at the DB layer — unchanged. me_audit_log stays
--       FORCE RLS; super_admin_audit_log stays the documented platform-global
--       carve-out (slice 142/198). This migration only widens two CHECKs.
--
-- IDEMPOTENCY / REVERSIBILITY:
--   Both CHECKs are DROP IF EXISTS + recreate as supersets (additive). The
--   down migration restores both CHECKs to their pre-slice-478 shape. No data
--   migration; no table create/drop.

-- ===== 1. me_audit_log.action CHECK extension =====

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
        -- Slice 478 (user↔tenant↔role assignment):
        'user_tenant_assign',
        'user_tenant_revoke'
    ));

-- ===== 2. super_admin_audit_log.action CHECK extension =====

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN (
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        'demo_seed_apply',
        'demo_seed_teardown',
        'demo_seed',
        'demo_teardown',
        'authz_bundle_reload',
        -- Slice 478 (user↔tenant↔role assignment):
        'user_tenant_assign',
        'user_tenant_revoke'
    ));
