-- security-atlas — #1780: deterministic SCF anchor candidates for staged
-- control import rows (bring-your-own controls, #1734).
--
-- Each staged row gets ranked SCF anchor candidates when it is staged:
--
--   crosswalk  anchors whose framework mappings reach any of the row's
--              tested-under criteria (effective_fw_to_scf_edges), ranked by
--              criteria reached, then mapping strength;
--   keyword    catalog keyword matches (the question-mapping retrieval),
--              filling the list to a fixed size.
--
-- Candidates come from one SCF release, recorded on the batch. A row whose
-- tested criteria no candidate reaches is flagged (no_candidate_reaches).
-- No LLM is involved, so no candidate carries AI output and the
-- ai_assist_human_approver_guard does not apply here; an LLM rerank adopts
-- it when it lands.

ALTER TABLE control_import_batches
    ADD COLUMN IF NOT EXISTS scf_framework_version_id UUID NULL REFERENCES framework_versions(id),
    ADD COLUMN IF NOT EXISTS scf_release TEXT NULL,
    ADD COLUMN IF NOT EXISTS suggestions_status TEXT NOT NULL DEFAULT 'ready';

DO $$
BEGIN
    ALTER TABLE control_import_batches
        ADD CONSTRAINT control_import_batches_suggestions_status_chk
        CHECK (suggestions_status IN ('pending', 'ready', 'failed', 'unavailable'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE control_import_rows
    ADD COLUMN IF NOT EXISTS no_candidate_reaches BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS control_import_candidates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    batch_id        UUID NOT NULL,
    row_id          UUID NOT NULL REFERENCES control_import_rows(id) ON DELETE CASCADE,
    rank            INTEGER NOT NULL,
    scf_anchor_id   UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    scf_id          TEXT NOT NULL,
    title           TEXT NOT NULL,
    source          TEXT NOT NULL,
    -- [{"code": "CC6.1", "requirement_id": "<framework_requirements.id>"}]
    reaches         JSONB NOT NULL DEFAULT '[]'::JSONB,
    strength        DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT control_import_candidates_source_chk
        CHECK (source IN ('crosswalk', 'keyword')),
    CONSTRAINT control_import_candidates_rank_chk CHECK (rank >= 1),
    CONSTRAINT control_import_candidates_row_rank_unique UNIQUE (row_id, rank),
    CONSTRAINT control_import_candidates_row_anchor_unique UNIQUE (row_id, scf_anchor_id),
    CONSTRAINT control_import_candidates_batch_tenant_fk
        FOREIGN KEY (tenant_id, batch_id)
        REFERENCES control_import_batches (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_control_import_candidates_tenant_batch
    ON control_import_candidates (tenant_id, batch_id, row_id, rank);

-- RLS: same four-policy shape as the other control_import_* tables.
ALTER TABLE control_import_candidates ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_import_candidates FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    BEGIN
        CREATE POLICY tenant_read ON control_import_candidates FOR SELECT USING (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_write ON control_import_candidates FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_update ON control_import_candidates FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_delete ON control_import_candidates FOR DELETE USING (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON control_import_candidates TO atlas_app;
