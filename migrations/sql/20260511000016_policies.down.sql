-- Reverse of 20260511000016_policies.sql.
--
-- Restores the slice-002 shape (column-wise). The four-policy RLS split is
-- collapsed back to the single tenant_isolation policy; the version-chain
-- columns are dropped; status reverts to the policy_status enum; version
-- reverts to INTEGER (data preservation: only rows whose version parses as
-- an integer can survive -- in practice the table is empty in v1 so this
-- is a no-op).

-- 1. Drop the four-policy RLS split + restore tenant_isolation.
DROP POLICY IF EXISTS tenant_read   ON policies;
DROP POLICY IF EXISTS tenant_write  ON policies;
DROP POLICY IF EXISTS tenant_update ON policies;
DROP POLICY IF EXISTS tenant_delete ON policies;
CREATE POLICY tenant_isolation ON policies
    USING (current_tenant_matches(tenant_id));

-- 2. Drop the new indexes.
DROP INDEX IF EXISTS idx_policies_tenant_predecessor;
DROP INDEX IF EXISTS idx_policies_tenant_status_created;
DROP INDEX IF EXISTS policies_predecessor_unique_when_set;

-- 3. Drop the new constraints in reverse-dependency order.
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_predecessor_fk;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_tenant_id_unique;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_effective_date_set_when_published;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_source_attribution_chk;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_created_by_nonempty;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_approver_role_nonempty;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_owner_role_nonempty;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_body_md_nonempty;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_version_nonempty;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_title_nonempty;
ALTER TABLE policies DROP CONSTRAINT IF EXISTS policies_status_chk;

-- 4. Rename plural array column back.
ALTER TABLE policies
    RENAME COLUMN acknowledgment_required_roles TO acknowledgment_required_role;

-- 5. Restore legacy owner/approver columns (default-empty; the up
-- migration's DROP wiped any data they held).
ALTER TABLE policies
    ADD COLUMN owner    TEXT NOT NULL DEFAULT '',
    ADD COLUMN approver TEXT NOT NULL DEFAULT '';

-- 6. Revert status: TEXT -> policy_status enum.
ALTER TABLE policies
    ALTER COLUMN status DROP DEFAULT,
    ALTER COLUMN status TYPE policy_status USING status::policy_status,
    ALTER COLUMN status SET DEFAULT 'draft'::policy_status;

-- 7. Revert body_md DEFAULT.
ALTER TABLE policies
    ALTER COLUMN body_md SET DEFAULT '';

-- 8. Revert version: TEXT -> INTEGER. NB: any non-integer values WILL
-- fail this cast -- intentional. Up migrations should never leave the
-- table in a state where a non-empty deployment cannot down-migrate.
ALTER TABLE policies
    ALTER COLUMN version DROP DEFAULT,
    ALTER COLUMN version TYPE INTEGER USING version::INTEGER;

ALTER TABLE policies
    ALTER COLUMN version SET DEFAULT 1;

-- 9. Drop the new columns.
ALTER TABLE policies
    DROP COLUMN IF EXISTS superseded_at,
    DROP COLUMN IF EXISTS published_by,
    DROP COLUMN IF EXISTS published_at,
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS submitted_by,
    DROP COLUMN IF EXISTS submitted_at,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS source_attribution,
    DROP COLUMN IF EXISTS linked_control_ids,
    DROP COLUMN IF EXISTS approver_role,
    DROP COLUMN IF EXISTS owner_role,
    DROP COLUMN IF EXISTS predecessor_id;
