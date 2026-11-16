-- #2129: risk and vendor owners are users, migrated from free text.
--
-- 1. risks.owner_user_id and vendors.owner_user_id name the owner as a user.
--    The free-text columns they replace (risks.treatment_owner,
--    vendors.owner_user) are kept, not dropped: while owner_user_id is NULL
--    their text is the display-only "Owner (unlinked)". The API and edit
--    paths validate an owner as an active user in the record's own tenant;
--    the FK only proves the user exists.
--
-- 2. Existing free text is matched to a user of the record's own tenant by
--    exact name or email, case-insensitive, ignoring surrounding spaces.
--    The match is linked only when exactly one active user matches. Two or
--    more matching users (a shared name, or one user's name and another's
--    email) is ambiguous and stays unlinked, as does text no user matches.
--    No user of another tenant is ever considered: every join names the
--    record's tenant_id. The free text is not changed, so nothing is lost
--    and the down migration restores the records exactly.
--
--    scripts/owner-link-dry-run.sql reports the per-tenant counts this
--    matching gives (matched, ambiguous, unmatched) without writing.
--
-- 3. me_audit_log accepts 'vendor_owner_update', the row the vendor write
--    paths add when a vendor's owner changes. Risk owner changes are
--    risk_audit_log fields_changed rows, which need no new action. The CHECK
--    is rewritten in full from the latest definition
--    (20261120000000_llm_settings_audit.sql): every existing value is kept
--    and the new one is appended.
--
-- Reversible via 20261218000000_risk_vendor_owner_users.down.sql.

ALTER TABLE risks
    ADD COLUMN IF NOT EXISTS owner_user_id UUID NULL REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE vendors
    ADD COLUMN IF NOT EXISTS owner_user_id UUID NULL REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS risks_owner_user ON risks (tenant_id, owner_user_id)
    WHERE owner_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS vendors_owner_user ON vendors (tenant_id, owner_user_id)
    WHERE owner_user_id IS NOT NULL;

-- Link each risk whose text names exactly one active user of its tenant.
WITH matches AS (
    SELECT r.id AS risk_id, u.id AS user_id
      FROM risks r
      JOIN users u
        ON u.tenant_id = r.tenant_id
       AND u.status = 'active'
       AND (lower(btrim(u.display_name)) = lower(btrim(r.treatment_owner))
            OR lower(btrim(u.email)) = lower(btrim(r.treatment_owner)))
     WHERE r.owner_user_id IS NULL
       AND btrim(r.treatment_owner) <> ''
), unique_matches AS (
    SELECT risk_id, min(user_id::text)::uuid AS user_id
      FROM matches
     GROUP BY risk_id
    HAVING count(DISTINCT user_id) = 1
)
UPDATE risks r
   SET owner_user_id = m.user_id
  FROM unique_matches m
 WHERE r.id = m.risk_id;

-- The same for vendors.
WITH matches AS (
    SELECT v.id AS vendor_id, u.id AS user_id
      FROM vendors v
      JOIN users u
        ON u.tenant_id = v.tenant_id
       AND u.status = 'active'
       AND (lower(btrim(u.display_name)) = lower(btrim(v.owner_user))
            OR lower(btrim(u.email)) = lower(btrim(v.owner_user)))
     WHERE v.owner_user_id IS NULL
       AND btrim(v.owner_user) <> ''
), unique_matches AS (
    SELECT vendor_id, min(user_id::text)::uuid AS user_id
      FROM matches
     GROUP BY vendor_id
    HAVING count(DISTINCT user_id) = 1
)
UPDATE vendors v
   SET owner_user_id = m.user_id
  FROM unique_matches m
 WHERE v.id = m.vendor_id;

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
        'tenant_self_approval_update',
        'tenant_vendor_initial_review_update',
        'tenant_llm_routing_update',
        'tenant_llm_routing_clear',
        'control_import_settings_update',
        'vendor_owner_update'
    ));
