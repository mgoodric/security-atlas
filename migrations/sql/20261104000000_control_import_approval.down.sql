-- Reverses 20261104000000_control_import_approval.sql.
DROP TABLE IF EXISTS control_import_audit_log;
DROP FUNCTION IF EXISTS control_import_audit_log_append_only();

ALTER TABLE control_import_rows
    DROP CONSTRAINT IF EXISTS control_import_rows_rejected_reason_chk,
    DROP CONSTRAINT IF EXISTS control_import_rows_approved_chk,
    DROP CONSTRAINT IF EXISTS control_import_rows_ai_assist_invariant,
    DROP COLUMN IF EXISTS human_approver,
    DROP COLUMN IF EXISTS human_approved,
    DROP COLUMN IF EXISTS ai_assisted,
    DROP COLUMN IF EXISTS scf_id,
    DROP COLUMN IF EXISTS scf_anchor_id,
    DROP COLUMN IF EXISTS bundle_id,
    DROP COLUMN IF EXISTS control_id,
    DROP COLUMN IF EXISTS decided_at,
    DROP COLUMN IF EXISTS decided_by;
