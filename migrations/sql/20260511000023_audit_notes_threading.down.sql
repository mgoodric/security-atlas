-- security-atlas -- reverse slice 029 audit_notes threading + visibility
-- extension. Drops the parent_note_id column + self-FK, restores the
-- slice-025 CHECK constraints, and re-creates the tenant_update +
-- tenant_delete RLS policies + GRANTs.

-- Re-grant UPDATE,DELETE before re-creating the policies.
GRANT UPDATE, DELETE ON audit_notes TO atlas_app;

-- Re-create the slice-025 four-policy split.
CREATE POLICY tenant_update ON audit_notes
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON audit_notes
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- Restore slice-025 scope_type CHECK.
ALTER TABLE audit_notes
    DROP CONSTRAINT audit_notes_scope_type_chk;
ALTER TABLE audit_notes
    ADD CONSTRAINT audit_notes_scope_type_chk
    CHECK (scope_type IN ('control', 'finding', 'sample', 'period'));

-- Restore slice-025 visibility CHECK (pinned to auditor_only).
-- Any 'shared' rows must be deleted before running this down migration;
-- the cleanup is the operator's responsibility (this is a destructive
-- rollback by definition).
ALTER TABLE audit_notes
    DROP CONSTRAINT audit_notes_visibility_chk;
ALTER TABLE audit_notes
    ADD CONSTRAINT audit_notes_visibility_chk
    CHECK (visibility = 'auditor_only');

-- Drop slice-029 indexes + columns + FKs.
DROP INDEX IF EXISTS idx_audit_notes_tenant_scope_period;
DROP INDEX IF EXISTS idx_audit_notes_tenant_parent;
ALTER TABLE audit_notes
    DROP CONSTRAINT IF EXISTS audit_notes_parent_fk;
ALTER TABLE audit_notes
    DROP CONSTRAINT IF EXISTS audit_notes_tenant_id_unique;
ALTER TABLE audit_notes
    DROP COLUMN IF EXISTS parent_note_id;
