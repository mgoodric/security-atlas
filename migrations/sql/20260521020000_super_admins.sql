-- security-atlas — slice 198: super_admins table + OIDC-first-install
-- bootstrap audit-log action.
--
-- Closes slice 192 AC-11/AC-12 (deferred via the 191 D6 partial-cutover-
-- with-spillover pattern).
--
-- BACKGROUND:
--
--   Through v2, the `atlas:super_admin` JWT claim existed only as:
--     - a column on the `oauth_auth_codes` snapshot (slice 189)
--     - a column on the `oauth_device_codes` approval snapshot (slice 188)
--     - a JSON tag in the in-memory `jwt.AtlasClaims` struct (slice 187)
--   There was NO persistent storage. Slice 192's user_resolver explicitly
--   left `super_admin = false` and referenced this slice as the storage-
--   filler:
--     "super_admin = false (no super_admins table at v2; spillover
--      slice 198 ships the OIDC-first-install bootstrap path)."
--
--   Slice 198 adds the storage primitive: a `super_admins` table holding
--   one row per platform-global admin identity. The DBUserResolver
--   (slice 192) reads it when populating the JWT claim.
--
-- LOAD-BEARING DESIGN CHOICES:
--
--   D1: Single-column PRIMARY KEY (user_id) — super_admin is platform-
--       global by definition. No tenant_id; the table is NOT under RLS.
--       Slice 198 P0-198-5 codifies this. Storing the grant per-tenant
--       would create N rows for one identity (one per tenant the user
--       has logged into); that is a representation bug, not a feature.
--   D2: granted_via TEXT NOT NULL — provenance is auditable inline.
--       'bootstrap_first_install' is the only v2 value; future
--       maintainer-CLI grants would add their own ('cli_grant', etc.)
--       via this column. CHECK constraint admits 'bootstrap_first_install'
--       only at v2 — future grants extend the CHECK in their slice's
--       migration.
--   D3: NO RLS — platform-global table by design. The application-layer
--       write surface is exactly one: the bootstrap branch in
--       internal/auth/users.Store.BootstrapFirstInstallOrUpsert, which
--       writes via the BYPASSRLS atlas_migrate pool. Read surface is the
--       DBUserResolver lookup, which reads via atlas_app via a SELECT
--       grant. No DELETE / UPDATE grants; the table is functionally
--       append-only by the absence of mutation grants.
--   D4: me_audit_log.action CHECK extension — slice 144's 14-value
--       CHECK gets one more value: 'bootstrap_first_install'. The
--       bootstrap branch writes ONE me_audit_log row in the same
--       transaction as the tenants/users/user_roles/super_admins
--       inserts. P0-198-4: the audit row is non-optional.
--   D5: The me_audit_log row's tenant_id is the NEWLY created tenant's
--       UUID — the bootstrap event is anchored to the tenant that was
--       brought into being by it. The user_id is the newly-provisioned
--       OIDC user. before/after JSONB captures the grant snapshot for
--       forensics.
--
-- CONSTITUTIONAL INVARIANTS:
--
--   #6 Tenant isolation at the DB layer — `super_admins` is the
--      explicit exception: platform-global by definition. The model
--      reflects the reality; tenant isolation continues to apply to
--      every OTHER table including users/user_roles/me_audit_log
--      that the bootstrap branch also writes.
--   #10 Audit-period freezing — N/A; super_admins is identity, not
--      evidence.
--
-- ANTI-CRITERIA HONORED AT THE SCHEMA LAYER (P0):
--
--   - P0-198-5: NO tenant_id column on super_admins (this migration).
--   - P0-198-1: atomicity is an application-layer invariant (the
--     bootstrap branch holds a single transaction across all writes).
--   - P0-198-2: count-gate is application-layer (the branch checks
--     count(*) before writing); the partial UNIQUE index on
--     tenants.is_bootstrap_tenant is the second leg of defense.
--   - P0-198-3: serialization via slice 144's
--     idx_tenants_bootstrap_singleton — provisioned in 20260521010000.
--     This slice does NOT re-provision; the index already exists.
--   - P0-198-4: this migration extends me_audit_log.action CHECK to
--     admit 'bootstrap_first_install'.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   CREATE TABLE IF NOT EXISTS so re-applying is a no-op. The
--   me_audit_log.action CHECK is dropped + recreated (DROP CONSTRAINT
--   IF EXISTS) — the slice-144 14-value CHECK is replaced by a 15-value
--   superset; rows that conformed to the smaller set continue to
--   conform to the larger. Reversible via 20260521020000_super_admins.down.sql.

-- ===== 1. super_admins table =====

CREATE TABLE IF NOT EXISTS super_admins (
    user_id      UUID NOT NULL PRIMARY KEY,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_via  TEXT NOT NULL,

    -- v2 admits exactly one provenance value. Future slices that add
    -- maintainer-CLI grants ('cli_grant') extend this CHECK in their
    -- migration. The CHECK is the static truth-table; the bootstrap
    -- branch writes only 'bootstrap_first_install' at v2.
    CONSTRAINT super_admins_granted_via_chk
        CHECK (granted_via IN ('bootstrap_first_install'))
);

COMMENT ON TABLE super_admins IS
    'Slice 198: platform-global super_admin identity rows. NOT tenant-scoped (no RLS). The DBUserResolver (slice 192) consults this table when populating the `atlas:super_admin` JWT claim. Write surface: exactly one — the bootstrap branch in users.Store.BootstrapFirstInstallOrUpsert (slice 198), which writes via the BYPASSRLS atlas_migrate pool during first install.';

COMMENT ON COLUMN super_admins.granted_via IS
    'Slice 198: provenance of the grant. v2 admits only ''bootstrap_first_install''. Future slices extending the source of grants (e.g., maintainer-CLI ''cli_grant'') extend the CHECK constraint in their own migration.';

-- Lookup-by-user_id is the hot path — the DBUserResolver checks
-- membership per JWT mint. The PRIMARY KEY already covers it; no
-- additional index needed.

-- ===== 2. Role grants =====
--
-- atlas_app: SELECT only. The DBUserResolver consults this table via
-- the RLS-bound atlas_app pool (the table is NOT under RLS but the
-- pool still authenticates as atlas_app). SELECT-only is sufficient;
-- atlas_app is NEVER the writer for this table.
GRANT SELECT ON super_admins TO atlas_app;

-- atlas_migrate: SELECT + INSERT. The bootstrap branch writes via the
-- BYPASSRLS auth pool (atlas_migrate). No UPDATE / DELETE grants —
-- the table is functionally append-only.
GRANT SELECT, INSERT ON super_admins TO atlas_migrate;

-- atlas_service_account: SELECT only. Future read paths that need
-- platform-global identity lookups (e.g., the maintainer-CLI grant
-- listing) read here without an RLS context.
GRANT SELECT ON super_admins TO atlas_service_account;

-- ===== 3. me_audit_log.action CHECK extension =====
--
-- Slice 144 extended the original slice-181 11-value CHECK to 14
-- values (adding 'tenant_rename'). This slice adds a 15th:
-- 'bootstrap_first_install'. The bootstrap branch writes ONE row per
-- successful first install — anchored to the newly-created tenant.

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
        'bootstrap_first_install'
    ));
