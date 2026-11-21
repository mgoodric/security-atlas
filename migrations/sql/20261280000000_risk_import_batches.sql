-- security-atlas — #2839: risk register import, the tracer (spec #2812).
--
-- A security leader uploads a CSV/XLSX risk register. Every non-blank data
-- row lands in exactly one of two tables:
--
--   risk_import_rows           staged rows (organisation risk ID, title,
--                              description, category, review due, and the
--                              values of the columns no risk field took),
--                              awaiting a person's decision;
--   risk_import_rejected_rows  rows that could not be staged, with the
--                              reason.
--
-- risk_import_batches carries the filename, uploader, status, counts and
-- the unmapped column headers. A person with approver rights approves the
-- pending rows: each becomes a risk created through the risk store in the
-- same transaction as the row decision and its risk_import_audit_log row.
-- Every table is tenant-scoped and RLS-enforced (invariant #6), with the
-- four-policy shape of the controls import (20260907000000), and the audit
-- log is append-only like control_import_audit_log (20261107000000).

CREATE TABLE IF NOT EXISTS risk_import_batches (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL,
    source_filename   TEXT NOT NULL,
    source_format     TEXT NOT NULL,
    uploaded_by       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'staged',
    total_rows        INTEGER NOT NULL DEFAULT 0,
    staged_count      INTEGER NOT NULL DEFAULT 0,
    rejected_count    INTEGER NOT NULL DEFAULT 0,
    unmapped_columns  JSONB NOT NULL DEFAULT '[]'::JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT risk_import_batches_tenant_id_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT risk_import_batches_format_chk
        CHECK (source_format IN ('csv', 'xlsx')),
    CONSTRAINT risk_import_batches_status_chk
        CHECK (status IN ('staged', 'completed')),
    CONSTRAINT risk_import_batches_counts_chk
        CHECK (staged_count >= 0 AND rejected_count >= 0
               AND total_rows = staged_count + rejected_count)
);

CREATE INDEX IF NOT EXISTS idx_risk_import_batches_tenant_created
    ON risk_import_batches (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS risk_import_rows (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL,
    batch_id          UUID NOT NULL,
    row_number        INTEGER NOT NULL,
    -- The organisation risk ID ("R-014"); optional, as on risks.
    external_ref      TEXT NULL,
    title             TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    -- The slug of the tenant category the row's value matched at upload.
    category_slug     TEXT NOT NULL,
    review_due        DATE NULL,
    -- {"<header>": "<value>"} for every column no risk field took. Never
    -- written into the risk's description.
    unmapped          JSONB NOT NULL DEFAULT '{}'::JSONB,
    status            TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT NULL,
    decided_by        TEXT NULL,
    decided_at        TIMESTAMPTZ NULL,
    -- The risk approval created, or the existing risk a duplicate matched.
    risk_id           UUID NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT risk_import_rows_status_chk
        CHECK (status IN ('pending', 'approved', 'rejected', 'duplicate')),
    CONSTRAINT risk_import_rows_decided_chk
        CHECK (status = 'pending' OR length(coalesce(decided_by, '')) > 0),
    CONSTRAINT risk_import_rows_risk_chk
        CHECK (status NOT IN ('approved', 'duplicate') OR risk_id IS NOT NULL),
    CONSTRAINT risk_import_rows_rejected_reason_chk
        CHECK (status <> 'rejected' OR length(coalesce(rejection_reason, '')) > 0),
    CONSTRAINT risk_import_rows_batch_tenant_fk
        FOREIGN KEY (tenant_id, batch_id)
        REFERENCES risk_import_batches (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_risk_import_rows_tenant_batch
    ON risk_import_rows (tenant_id, batch_id, row_number);

-- A file never stages one organisation risk ID twice; parsing rejects the
-- repeat, and this holds it. Case is ignored, as on risks.
CREATE UNIQUE INDEX IF NOT EXISTS risk_import_rows_batch_external_ref_key
    ON risk_import_rows (batch_id, lower(external_ref))
    WHERE external_ref IS NOT NULL;

CREATE TABLE IF NOT EXISTS risk_import_rejected_rows (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    batch_id      UUID NOT NULL,
    row_number    INTEGER NOT NULL,
    external_ref  TEXT NOT NULL DEFAULT '',
    reason        TEXT NOT NULL,
    detail        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT risk_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'duplicate_external_ref',
                          'invalid_external_ref', 'unknown_category',
                          'invalid_review_due')),
    CONSTRAINT risk_import_rejected_rows_batch_tenant_fk
        FOREIGN KEY (tenant_id, batch_id)
        REFERENCES risk_import_batches (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_risk_import_rejected_rows_tenant_batch
    ON risk_import_rejected_rows (tenant_id, batch_id, row_number);

ALTER TABLE risk_import_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_import_batches FORCE ROW LEVEL SECURITY;
ALTER TABLE risk_import_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_import_rows FORCE ROW LEVEL SECURITY;
ALTER TABLE risk_import_rejected_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_import_rejected_rows FORCE ROW LEVEL SECURITY;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['risk_import_batches', 'risk_import_rows', 'risk_import_rejected_rows']
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

GRANT SELECT, INSERT, UPDATE, DELETE ON risk_import_batches TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON risk_import_rows TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON risk_import_rejected_rows TO atlas_app;

-- ===== risk_import_audit_log =====
--
-- One append-only row per decision: approve (with the risk it created),
-- reject (with the reason) or duplicate (with the existing risk). RLS with
-- SELECT + INSERT policies only, and a trigger that denies UPDATE and
-- DELETE even for a role that bypasses RLS. No FK to the batch or row: the
-- trail outlives a deleted batch.
CREATE TABLE IF NOT EXISTS risk_import_audit_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    batch_id        UUID NOT NULL,
    row_id          UUID NOT NULL,
    row_number      INTEGER NOT NULL,
    external_ref    TEXT NULL,
    action          TEXT NOT NULL
                    CONSTRAINT risk_import_audit_log_action_check
                    CHECK (action IN ('approve', 'reject', 'duplicate')),
    -- The users(id) who decided; never a machine credential.
    actor           TEXT NOT NULL,
    risk_id         UUID NULL,
    reason          TEXT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Slice 180 convention for the audit-log family.
    subject_module  TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT risk_import_audit_log_actor_nonempty CHECK (length(actor) > 0),
    CONSTRAINT risk_import_audit_log_risk
        CHECK (action = 'reject' OR risk_id IS NOT NULL),
    CONSTRAINT risk_import_audit_log_reject_reason
        CHECK (action <> 'reject' OR length(coalesce(reason, '')) > 0)
);

CREATE INDEX IF NOT EXISTS idx_risk_import_audit_log_tenant_occurred
    ON risk_import_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_risk_import_audit_log_batch
    ON risk_import_audit_log (tenant_id, batch_id, occurred_at);

ALTER TABLE risk_import_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_import_audit_log FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON risk_import_audit_log
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$
BEGIN
    CREATE POLICY tenant_write ON risk_import_audit_log
        FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT ON risk_import_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION risk_import_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'risk_import_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS risk_import_audit_log_append_only_trg ON risk_import_audit_log;
CREATE TRIGGER risk_import_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON risk_import_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION risk_import_audit_log_append_only();
