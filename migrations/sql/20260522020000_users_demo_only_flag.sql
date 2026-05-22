-- security-atlas — slice 205: demo seed dataset (users.demo_only +
-- audit-log action CHECK extensions).
--
-- Three schema changes:
--
--   1. Add `users.demo_only BOOLEAN NOT NULL DEFAULT FALSE`. Tags every
--      user row written by the slice-205 `atlas-cli demo seed` command.
--      Default FALSE for every existing row + every non-demo user
--      created in the future. Slice 142's super_admin grant surface
--      will reject promoting a `demo_only=TRUE` user to super_admin
--      (application-layer invariant added by slice 205's seeder).
--
--   2. Extend `super_admin_audit_log.action` CHECK constraint to admit
--      two new values: `demo_seed_apply` and `demo_seed_teardown`. The
--      seeder writes one platform-global forensic row at the start of
--      each apply/teardown so the platform operator's audit history
--      records demo activity at the same level as super_admin
--      grant/revoke events.
--
--   3. Extend `me_audit_log.action` CHECK constraint to admit the same
--      two values. Mirrors the slice 142 / 143 dual-write pattern: every
--      demo-seed event is recorded BOTH platform-globally (in
--      super_admin_audit_log) AND tenant-scoped to the actor's session
--      tenant (in me_audit_log), so the slice-124 unified aggregator
--      surfaces it via the existing kind='me' UNION branch.
--
-- LOAD-BEARING DESIGN CHOICES (also captured in
-- docs/audit-log/205-demo-seed-data-decisions.md):
--
--   D-MIG-1: `demo_only` is column on `users`, NOT a role/RBAC entry.
--       The slice-doc 205 threat-model row "E EoP" calls out a demo
--       user being promoted to super_admin in some other tenant as a
--       backdoor risk. A column on `users` is the simplest enforcement
--       primitive — slice 142's super_admin grant handler can read it
--       in one SELECT and reject the grant. A role/RBAC entry would
--       require a separate enforcement surface per consumer.
--
--   D-MIG-2: BOOLEAN NOT NULL DEFAULT FALSE — same shape as
--       `is_bootstrap_tenant` on the `tenants` table (slice 144). The
--       column on `users` ships unrestricted at v1; the demo seeder
--       is the only writer of TRUE values via the BYPASSRLS auth pool
--       (mirroring deploy/docker/bootstrap/seed.sql).
--
-- CONSTITUTIONAL INVARIANTS:
--
--   #6  Tenant isolation at the DB layer — `users` already ships under
--       FORCE RLS (slice 034). The new column is tenant-isolated by
--       the existing policy; no policy change needed.
--   #10 Audit-period freezing — N/A; user identity is not evidence.
--
-- ANTI-CRITERIA HONORED AT THE SCHEMA LAYER (P0):
--
--   - P0-A1 (no auto-bootstrap): the seeder's env-var gate is at the
--     application layer (cmd/atlas-cli/cmd_demo.go); the schema simply
--     records that a row IS a demo row.
--   - P0-A2 (no hard-coded credentials): no password column added; the
--     seeder generates a fresh password per invocation and prints it
--     once.
--   - P0-A8 (no reproducible-across-invocations audit-log entries):
--     the new action values are admit-list extensions, not data; each
--     apply/teardown writes a NEW row with a NEW id + new timestamp.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   ALTER ... ADD COLUMN IF NOT EXISTS so re-applying is a no-op. The
--   CHECK extensions DROP IF EXISTS + recreate (supersets, mirroring
--   slice 143's pattern). Down migration drops the column + restores
--   the previous CHECK shape (the post-slice-175 baseline).

-- ===== 1. users.demo_only column =====
--
-- BOOLEAN NOT NULL DEFAULT FALSE — every existing row gets FALSE on
-- ADD COLUMN. The seeder writes TRUE only for the one user it creates
-- per demo tenant.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS demo_only BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN users.demo_only IS
    'Slice 205: tags a user row written by the atlas-cli demo seed command. Slice 142''s super_admin grant handler MUST reject promoting a demo_only=TRUE user. Default FALSE for every non-demo row. Only the demo-seed BYPASSRLS write path sets TRUE.';

-- ===== 2. super_admin_audit_log.action CHECK extension =====
--
-- Slice 143 extended the slice-142 two-value list to admit
-- 'tenant_create'. Slice 205 adds 'demo_seed_apply' and
-- 'demo_seed_teardown'. Append-only audit semantics preserved via the
-- table's no-UPDATE/no-DELETE grant footprint.

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN (
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        'demo_seed_apply',
        'demo_seed_teardown'
    ));

-- ===== 3. me_audit_log.action CHECK extension =====
--
-- Slice 175 last extended this list (adding 'controls_history_export').
-- Slice 205 adds 'demo_seed_apply' and 'demo_seed_teardown'. The
-- seeder writes one me_audit_log row per apply/teardown anchored to
-- the actor's session tenant so the slice-124 unified aggregator
-- surfaces the event via the existing kind='me' UNION branch.

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
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        'demo_seed_apply',
        'demo_seed_teardown'
    ));
