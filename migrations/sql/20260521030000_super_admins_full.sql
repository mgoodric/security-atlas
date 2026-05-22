-- security-atlas — slice 142: super_admin role full schema + management surface.
--
-- Promotes the slice-198 super_admins stub to a full management-grade
-- schema and ships the audit-log primitive that records grant + demote
-- events forensically.
--
-- BACKGROUND:
--
--   Slice 198 introduced `super_admins(user_id, granted_at, granted_via)`
--   with exactly one allowed `granted_via` value ('bootstrap_first_install')
--   and write-grant only to the BYPASSRLS `atlas_migrate` pool. That is
--   sufficient for first-install but offers no runtime path to grant or
--   revoke super_admin to a second identity. Slice 142 closes that gap:
--
--     1. Extends the `granted_via` CHECK to admit 'manual_grant' — the
--        provenance value written by POST /v1/admin/super-admins.
--     2. Grants INSERT + DELETE on `super_admins` to `atlas_app` (the
--        RLS-bound pool that backs handlers running under a tenant
--        context). The grant + demote handlers run under atlas_app so
--        the per-request tenant GUC is set for the parallel
--        `me_audit_log` write — but `super_admins` itself is NOT under
--        RLS, so the writes succeed regardless of GUC value.
--     3. Adds the `super_admin_audit_log` primitive — append-only,
--        platform-global (no tenant_id, no RLS), super_admin-only
--        readable. The slice-036 append-only pattern is applied via the
--        absence of UPDATE/DELETE grants rather than via RLS policies
--        because the table is not tenant-scoped.
--     4. Extends `me_audit_log.action` CHECK to admit two new values
--        ('super_admin_grant', 'super_admin_revoke') so the same
--        transaction that writes the platform-global audit row also
--        writes a tenant-scoped row anchored to the actor's session
--        tenant. The slice-124 unified aggregator picks the tenant-
--        scoped row up via the existing `kind='me'` UNION branch
--        without any aggregator change (AC-8 satisfied by anchoring,
--        not by introducing a new UNION-ALL leg that would violate the
--        slice-124 RLS-routing contract).
--
-- LOAD-BEARING DESIGN CHOICES (also captured in
-- docs/audit-log/142-super-admin-role-surface-decisions.md):
--
--   D1: super_admin_audit_log is PLATFORM-GLOBAL (no tenant_id, no
--       RLS). Adding it as a 10th UNION-ALL leg in the slice-124
--       aggregator would violate the aggregator's tenant-isolation
--       contract (P0-A4: "MUST run as atlas_app under tenant GUC").
--       Instead, every grant/demote handler dual-writes:
--         - one super_admin_audit_log row (platform-global, the
--           forensic anchor);
--         - one me_audit_log row tagged with the actor's session
--           tenant and action='super_admin_grant' or
--           'super_admin_revoke'. The unified aggregator surfaces
--           THIS row to admins/auditors/grc_engineers of that tenant
--           via the existing kind='me' branch.
--       Net behaviour: super_admins see the platform-global row via a
--       (separate, future) SELECT path; tenant operators see the
--       tenant-scoped row via the existing unified aggregator. The
--       audit story is complete; the RLS model is preserved.
--
--   D2: Last-super_admin safety rail is application-layer, not schema-
--       layer. The DELETE handler wraps the DELETE in a
--       `SELECT count(*) FROM super_admins FOR UPDATE` that serializes
--       concurrent demotes. A pure-schema constraint ("at least one row
--       must exist") is not expressible in standard SQL without a
--       trigger; the application-layer guard is simpler and explicit.
--       Integration test (AC-10) asserts both the single-demote 409
--       and the concurrent-demote serialization.
--
--   D3: The `granted_via` CHECK extension admits 'manual_grant' as a
--       SUPERSET of the slice-198 single-value CHECK. Existing rows
--       conform to the larger set; the migration is purely additive.
--
--   D4: super_admin_audit_log has its own UUID PK column (audit_id) so
--       future cross-tenant pagination paths can resume by row id
--       independent of timestamp ordering. Mirrors the slice-181
--       me_audit_log shape.
--
-- CONSTITUTIONAL INVARIANTS:
--
--   #2  Ingestion + evaluation separated — N/A; super_admin grants are
--       identity, not evidence.
--   #6  Tenant isolation at the DB layer — `super_admins` and
--       `super_admin_audit_log` are explicit exceptions: platform-global
--       by definition. The slice-198 migration header already documents
--       super_admins as the exception; this migration extends that
--       documented carve-out by one sibling table. Every OTHER table
--       continues to be FORCE RLS.
--   #10 Audit-period freezing — N/A; super_admin events are identity,
--       not evidence.
--
-- ANTI-CRITERIA HONORED AT THE SCHEMA LAYER (P0):
--
--   - P0-SA-2: super_admin_audit_log has SELECT + INSERT grants only;
--     no UPDATE/DELETE grants → functionally append-only.
--   - P0-SA-3: NO tenant_id column on super_admins (preserved from
--     slice 198). NO mechanism in this migration to add super_admin to
--     user_tenants — that is application-layer enforced.
--   - P0-SA-4: NO `expires_at` column on super_admins. Time-bounded
--     grants out of scope at v1.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   ALTER + CREATE TABLE IF NOT EXISTS so re-applying is a no-op.
--   The granted_via CHECK is DROP IF EXISTS + recreate (superset). The
--   me_audit_log.action CHECK is DROP IF EXISTS + recreate (superset:
--   adds 2 values). Down migration restores both CHECKs to their pre-
--   slice-142 shape and drops the super_admin_audit_log table.

-- ===== 1. super_admins.granted_via CHECK extension =====
--
-- Slice 198 admitted exactly 'bootstrap_first_install'. Slice 142 adds
-- 'manual_grant' (the value written by POST /v1/admin/super-admins).
-- Future slices that add other provenance values (e.g., 'cli_grant')
-- extend this CHECK in their own migration.

ALTER TABLE super_admins
    DROP CONSTRAINT IF EXISTS super_admins_granted_via_chk;

ALTER TABLE super_admins
    ADD CONSTRAINT super_admins_granted_via_chk
    CHECK (granted_via IN ('bootstrap_first_install', 'manual_grant'));

-- ===== 2. super_admins runtime mutation grants =====
--
-- atlas_app: gains INSERT + DELETE. The grant + demote handlers run
-- under atlas_app within a per-request tenant GUC (the tenant context
-- is set for the parallel me_audit_log write). super_admins itself is
-- NOT under RLS, so the writes succeed regardless of GUC value.
--
-- atlas_service_account: keeps SELECT only (slice 198 grant).
-- atlas_migrate: keeps SELECT + INSERT only (slice 198 grant) for the
-- bootstrap path. No DELETE on atlas_migrate — the bootstrap path
-- never deletes; runtime demote runs under atlas_app.

GRANT INSERT, DELETE ON super_admins TO atlas_app;

-- ===== 3. super_admin_audit_log table =====
--
-- Append-only forensic record of every grant + demote event. Platform-
-- global (no tenant_id, no RLS) because super_admin is platform-global.
-- The actor's session tenant is captured in `actor_tenant_id` as a
-- payload field — not as a foreign key — so the audit row survives
-- tenant deletion.

CREATE TABLE IF NOT EXISTS super_admin_audit_log (
    audit_id           UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    action             TEXT NOT NULL,
    target_user_id     UUID NOT NULL,
    actor_user_id      UUID NOT NULL,
    actor_tenant_id    UUID NOT NULL,
    occurred_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload_json       JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- The two action values shipped at v1. Future slices that add new
    -- super_admin lifecycle events (e.g., 'super_admin_renamed' for an
    -- identity-rotation flow) extend this CHECK in their migration.
    CONSTRAINT super_admin_audit_log_action_chk
        CHECK (action IN ('super_admin_grant', 'super_admin_revoke')),

    -- Nonempty target/actor UUIDs. Defense-in-depth on top of NOT NULL.
    CONSTRAINT super_admin_audit_log_target_nonzero
        CHECK (target_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT super_admin_audit_log_actor_nonzero
        CHECK (actor_user_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

COMMENT ON TABLE super_admin_audit_log IS
    'Slice 142: append-only forensic record of super_admin grant + demote events. Platform-global (no tenant_id, no RLS) because super_admin itself is platform-global. The grant + demote handlers dual-write a tenant-scoped me_audit_log row anchored to the actor''s session tenant so the unified slice-124 aggregator surfaces the event to that tenant''s admins/auditors without breaching RLS.';

COMMENT ON COLUMN super_admin_audit_log.actor_tenant_id IS
    'Slice 142: the actor''s session tenant at grant/demote time. Captured as data, not FK — the audit row survives tenant deletion.';

-- Lookup-by-target hot path (e.g., "show me every event that touched
-- target X"). Lookup-by-actor is exercised by future audit-log views
-- but is not hot today.
CREATE INDEX IF NOT EXISTS idx_super_admin_audit_log_target
    ON super_admin_audit_log (target_user_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_super_admin_audit_log_occurred_at
    ON super_admin_audit_log (occurred_at DESC);

-- ===== 4. super_admin_audit_log grants =====
--
-- atlas_app: SELECT + INSERT only. The grant + demote handlers write
-- the row in the same transaction as the super_admins INSERT/DELETE
-- and the parallel me_audit_log row. SELECT is for the future read
-- path that surfaces the platform-global record to super_admins.
--
-- atlas_service_account: SELECT only. Future read paths (super_admin-
-- gated listing) can use either pool; atlas_service_account is more
-- common for non-tenant-scoped reads.
--
-- atlas_migrate: no grants. The bootstrap path does NOT write to this
-- table (slice 198's bootstrap writes the super_admins row but not an
-- audit-log row because no actor exists yet — the operator IS the
-- target of bootstrap).
--
-- Functionally append-only via the absence of UPDATE/DELETE grants.

GRANT SELECT, INSERT ON super_admin_audit_log TO atlas_app;
GRANT SELECT ON super_admin_audit_log TO atlas_service_account;

-- ===== 5. me_audit_log.action CHECK extension =====
--
-- Slice 198 extended the slice-144 14-value CHECK to 15 values. Slice
-- 142 adds two more: 'super_admin_grant' + 'super_admin_revoke'. The
-- grant + demote handlers write ONE me_audit_log row per event in the
-- same transaction as the super_admin_audit_log row + the
-- super_admins mutation. P0-SA-2: the audit rows are non-optional.

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
