DROP TABLE IF EXISTS control_import_candidates;
ALTER TABLE control_import_rows DROP COLUMN IF EXISTS no_candidate_reaches;
ALTER TABLE control_import_batches
    DROP CONSTRAINT IF EXISTS control_import_batches_suggestions_status_chk,
    DROP COLUMN IF EXISTS suggestions_status,
    DROP COLUMN IF EXISTS scf_release,
    DROP COLUMN IF EXISTS scf_framework_version_id;
