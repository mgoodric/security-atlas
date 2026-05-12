-- security-atlas — Down migration for slice 009 (control bundle format).
--
-- Restores the slice-002 / slice-008 column shape on `controls`. Data on the
-- dropped columns is lost — those columns held bundle versioning state that
-- has no slice-002 equivalent. The application code stops writing to those
-- columns when running against a downgraded schema, so the loss is contained
-- to the supersession chain (re-upload restores the active row).

-- 1. Restore the loose tenant_isolation policy.
DROP POLICY IF EXISTS tenant_read ON controls;
DROP POLICY IF EXISTS tenant_write ON controls;
DROP POLICY IF EXISTS tenant_update ON controls;
DROP POLICY IF EXISTS tenant_delete ON controls;
CREATE POLICY tenant_isolation ON controls
    USING (current_tenant_matches(tenant_id));

-- 2. Drop indexes.
DROP INDEX IF EXISTS controls_one_active_version_per_bundle;
DROP INDEX IF EXISTS idx_controls_tenant_bundle_id;
DROP INDEX IF EXISTS idx_controls_scf_anchor;

-- 3. Drop FKs.
ALTER TABLE controls
    DROP CONSTRAINT IF EXISTS controls_superseded_by_fk,
    DROP CONSTRAINT IF EXISTS controls_scf_anchor_fk;

-- 4. Drop slice-009 columns.
ALTER TABLE controls
    DROP COLUMN IF EXISTS bundle_id,
    DROP COLUMN IF EXISTS superseded_by,
    DROP COLUMN IF EXISTS bundle_manifest_yaml,
    DROP COLUMN IF EXISTS bundle_manifest_hash,
    DROP COLUMN IF EXISTS scf_anchor_id,
    DROP COLUMN IF EXISTS evidence_queries,
    DROP COLUMN IF EXISTS manual_evidence_schema,
    DROP COLUMN IF EXISTS linked_policy_ids,
    DROP COLUMN IF EXISTS freshness_class,
    DROP COLUMN IF EXISTS bundle_uploaded_at,
    DROP COLUMN IF EXISTS bundle_uploaded_by;
