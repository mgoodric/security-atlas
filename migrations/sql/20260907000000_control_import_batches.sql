-- security-atlas — #1779: control import batches (bring-your-own controls).
--
-- An operator uploads a CSV/XLSX of their existing controls. Every non-blank
-- data row lands in exactly one of two tables:
--
--   control_import_rows           staged rows (org control ID, title,
--                                 description, resolved tested-under
--                                 criteria, optional implementation type and
--                                 applicability), awaiting review;
--   control_import_rejected_rows  rows that could not be staged, with the
--                                 reason (missing field, unknown tested-under
--                                 code, duplicate ID, bad implementation type).
--
-- control_import_batches carries the upload's framework, filename, uploader,
-- status and counts. All three tables are tenant-scoped and RLS-enforced
-- (invariant #6). Nothing here creates a control: approval is a later step.

CREATE TABLE IF NOT EXISTS control_import_batches (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL,
    framework_slug        TEXT NOT NULL,
    framework_version_id  UUID NOT NULL REFERENCES framework_versions(id),
    framework_version     TEXT NOT NULL,
    source_filename       TEXT NOT NULL,
    source_format         TEXT NOT NULL,
    uploaded_by           TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'staged',
    total_rows            INTEGER NOT NULL DEFAULT 0,
    staged_count          INTEGER NOT NULL DEFAULT 0,
    rejected_count        INTEGER NOT NULL DEFAULT 0,
    unmapped_columns      JSONB NOT NULL DEFAULT '[]'::JSONB,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT control_import_batches_tenant_id_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT control_import_batches_format_chk
        CHECK (source_format IN ('csv', 'xlsx')),
    CONSTRAINT control_import_batches_status_chk
        CHECK (status IN ('staged', 'completed')),
    CONSTRAINT control_import_batches_counts_chk
        CHECK (staged_count >= 0 AND rejected_count >= 0
               AND total_rows = staged_count + rejected_count)
);

CREATE INDEX IF NOT EXISTS idx_control_import_batches_tenant_created
    ON control_import_batches (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS control_import_rows (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL,
    batch_id             UUID NOT NULL,
    row_number           INTEGER NOT NULL,
    org_control_id       TEXT NOT NULL,
    title                TEXT NOT NULL,
    description          TEXT NOT NULL,
    -- [{"code": "CC6.1", "requirement_id": "<framework_requirements.id>"}]
    tested_under         JSONB NOT NULL DEFAULT '[]'::JSONB,
    implementation_type  TEXT NULL,
    applicability        TEXT NULL,
    status               TEXT NOT NULL DEFAULT 'pending',
    rejection_reason     TEXT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT control_import_rows_status_chk
        CHECK (status IN ('pending', 'approved', 'rejected', 'duplicate')),
    CONSTRAINT control_import_rows_impl_type_chk
        CHECK (implementation_type IS NULL OR implementation_type IN
               ('automated', 'semi_automated', 'manual_attested', 'manual_periodic')),
    CONSTRAINT control_import_rows_batch_tenant_fk
        FOREIGN KEY (tenant_id, batch_id)
        REFERENCES control_import_batches (tenant_id, id)
        ON DELETE CASCADE,
    CONSTRAINT control_import_rows_batch_org_id_unique
        UNIQUE (batch_id, org_control_id)
);

CREATE INDEX IF NOT EXISTS idx_control_import_rows_tenant_batch
    ON control_import_rows (tenant_id, batch_id, row_number);

CREATE TABLE IF NOT EXISTS control_import_rejected_rows (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    batch_id         UUID NOT NULL,
    row_number       INTEGER NOT NULL,
    org_control_id   TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL,
    detail           TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT control_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'unknown_tested_under',
                          'duplicate_id', 'invalid_implementation_type')),
    CONSTRAINT control_import_rejected_rows_batch_tenant_fk
        FOREIGN KEY (tenant_id, batch_id)
        REFERENCES control_import_batches (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_control_import_rejected_rows_tenant_batch
    ON control_import_rejected_rows (tenant_id, batch_id, row_number);

-- RLS: four-policy shape, matching questionnaire_mapping_proposals.
ALTER TABLE control_import_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_import_batches FORCE ROW LEVEL SECURITY;
ALTER TABLE control_import_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_import_rows FORCE ROW LEVEL SECURITY;
ALTER TABLE control_import_rejected_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_import_rejected_rows FORCE ROW LEVEL SECURITY;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['control_import_batches', 'control_import_rows', 'control_import_rejected_rows']
    LOOP
        BEGIN
            EXECUTE format('CREATE POLICY tenant_read ON %I FOR SELECT USING (current_tenant_matches(tenant_id))', t);
        EXCEPTION WHEN duplicate_object THEN NULL; END;
        BEGIN
            EXECUTE format('CREATE POLICY tenant_write ON %I FOR INSERT WITH CHECK (current_tenant_matches(tenant_id))', t);
        EXCEPTION WHEN duplicate_object THEN NULL; END;
        BEGIN
            EXECUTE format('CREATE POLICY tenant_update ON %I FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id))', t);
        EXCEPTION WHEN duplicate_object THEN NULL; END;
        BEGIN
            EXECUTE format('CREATE POLICY tenant_delete ON %I FOR DELETE USING (current_tenant_matches(tenant_id))', t);
        EXCEPTION WHEN duplicate_object THEN NULL; END;
    END LOOP;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON control_import_batches TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON control_import_rows TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON control_import_rejected_rows TO atlas_app;
