-- #2171: every optional module can be switched off (internal/featureflag
-- Modules). Three changes to stored switches; no module data changes.
--
-- 1. Personnel security gets its switch, `personnel.enabled` (default on),
--    in a new category 'personnel'. Widen the CHECK.
--
-- 2. `oscal.export` gated vendor claims under a misleading name. It becomes
--    the Vendor claims feature of Third-party risk, `vendor.claims`. Each
--    tenant's stored value moves to the new key unchanged, so a tenant that
--    had vendor claims on keeps them on. The audit log is append-only and
--    keeps the old key on its history rows.
--
-- 3. Before this ticket nothing read `risk.enabled`, `vendor.enabled`,
--    `policy.enabled` or `exceptions.enabled`: a tenant that stored one as
--    off still saw the module. From now on the switch hides it. So that no
--    tenant loses a module on deploy, a stored `false` on those four keys
--    is set back to `true`, with an audit-log row saying why.
--
-- atlas_migrate has BYPASSRLS, so the UPDATEs reach every tenant's rows.

ALTER TABLE feature_flags
    DROP CONSTRAINT IF EXISTS feature_flags_category_chk;

ALTER TABLE feature_flags
    ADD CONSTRAINT feature_flags_category_chk
    CHECK (category IN (
        'core', 'risk', 'vendor', 'policy', 'controls',
        'audit', 'evidence', 'board', 'integrations',
        'questionnaire', 'personnel'
    ));

UPDATE feature_flags
   SET flag_key = 'vendor.claims',
       category = 'vendor'
 WHERE flag_key = 'oscal.export';

INSERT INTO feature_flag_audit_log (id, tenant_id, flag_key, from_enabled, to_enabled, actor, reason)
SELECT gen_random_uuid(), tenant_id, flag_key, false, true,
       'migration:20261130000000',
       'Module switches are enforced from #2171. This switch was stored off but hid nothing, so it is set on to keep the module visible as before.'
  FROM feature_flags
 WHERE flag_key IN ('risk.enabled', 'vendor.enabled', 'policy.enabled', 'exceptions.enabled')
   AND enabled = false;

UPDATE feature_flags
   SET enabled = true,
       last_changed_by = 'migration:20261130000000',
       last_changed_at = now()
 WHERE flag_key IN ('risk.enabled', 'vendor.enabled', 'policy.enabled', 'exceptions.enabled')
   AND enabled = false;
