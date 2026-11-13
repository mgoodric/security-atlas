-- Reverse #1747: drop the policy import columns and the policy audit log.

DROP TRIGGER IF EXISTS policy_audit_log_no_update_trg ON policy_audit_log;
DROP FUNCTION IF EXISTS policy_audit_log_append_only();
DROP TABLE IF EXISTS policy_audit_log;

DROP INDEX IF EXISTS idx_policies_tenant_title_version;

ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_source_url_len;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_imported_fields_chk;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_source_attribution_chk;

-- Imported rows cannot satisfy the restored CHECK; relabel them first.
UPDATE policies SET source_attribution = 'tenant_authored'
WHERE source_attribution = 'imported';

ALTER TABLE policies
    ADD CONSTRAINT policies_source_attribution_chk
        CHECK (source_attribution IN ('community_draft', 'tenant_authored', 'vendor_provided'));

ALTER TABLE policies
    DROP COLUMN IF EXISTS imported_at,
    DROP COLUMN IF EXISTS imported_by,
    DROP COLUMN IF EXISTS source_url,
    DROP COLUMN IF EXISTS historical_approved_at,
    DROP COLUMN IF EXISTS historical_approver_name;
