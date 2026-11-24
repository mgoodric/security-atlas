-- security-atlas -- #1742: import historical evidence with its real date.
--
-- An admin imports prior-period evidence (last year's audit results,
-- history from a spreadsheet or another GRC tool) through
-- POST /v1/evidence:import. Those records keep their real observed_at,
-- past the 24h skew window live ingest enforces, and are written now:
-- ingested_at is the real write time, never backdated (#1699).
--
--   evidence_records.ingestion_path   gains 'import'.
--   evidence_audit_log.import_batch_id
--                                     the batch an import attempt belongs
--                                     to, on every accepted and rejected
--                                     row it writes. NULL on every other
--                                     path.
--   audit_period_freeze_revisions     an import that names a frozen period
--                                     (--amend-frozen) re-freezes it as a
--                                     new revision. Revision 1 is the
--                                     original freeze, kept on the
--                                     audit_periods row and never rewritten;
--                                     this table holds revisions 2 and up.
--                                     Each lists the evidence records it
--                                     adds to the frozen population, so the
--                                     population at revision N is the
--                                     original horizon plus the records of
--                                     revisions 2..N, and any revision can
--                                     be replayed. justification is the
--                                     admin's required free-text reason for
--                                     amending (Matt, 2026-11-24).
--                                     Only records observed at or before the
--                                     original frozen_at may be listed
--                                     (invariant #10); the importer enforces
--                                     it and the frozen reads re-check it.
--   samples.freeze_revision           the freeze revision of the period a
--                                     sample's population drew from; NULL for
--                                     an open period or no period.
--   audit_period_audit_log action     gains period_amended_by_import.
--
-- Constitutional invariants:
--   #2  evidence_records stays append-only; nothing here updates a row.
--   #6  the new table is tenant-scoped under FORCE ROW LEVEL SECURITY,
--       SELECT + INSERT only, so a revision is append-only.
--   #10 a frozen population changes only by a new revision row; the
--       original frozen_at and frozen_hash on audit_periods are unchanged.
--
-- Reversible via 20261306000000_evidence_historical_import.down.sql.

-- ===== 1. ingestion_path 'import' =====

ALTER TABLE evidence_records
    DROP CONSTRAINT evidence_records_ingestion_path_chk;

-- NOT VALID, then VALIDATE: the ADD takes ACCESS EXCLUSIVE only briefly,
-- and the full-ledger scan runs under SHARE UPDATE EXCLUSIVE, which does
-- not block ingest.
ALTER TABLE evidence_records
    ADD CONSTRAINT evidence_records_ingestion_path_chk
        CHECK (ingestion_path IN ('push', 'pull', 'subscribe', 'manual_upload', 'webhook', 'import'))
        NOT VALID;

ALTER TABLE evidence_records
    VALIDATE CONSTRAINT evidence_records_ingestion_path_chk;

-- ===== 2. evidence_audit_log.import_batch_id =====

ALTER TABLE evidence_audit_log
    ADD COLUMN import_batch_id UUID NULL;

CREATE INDEX idx_evidence_audit_log_tenant_import_batch
    ON evidence_audit_log (tenant_id, import_batch_id)
    WHERE import_batch_id IS NOT NULL;

-- ===== 3. audit_period_freeze_revisions =====

CREATE TABLE audit_period_freeze_revisions (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    audit_period_id     UUID NOT NULL,
    revision            INTEGER NOT NULL,
    frozen_hash         BYTEA NOT NULL,
    previous_hash       BYTEA NOT NULL,
    frozen_at           TIMESTAMPTZ NOT NULL,
    reason              TEXT NOT NULL,
    justification       TEXT NOT NULL,
    actor               TEXT NOT NULL,
    import_batch_id     UUID NOT NULL,
    evidence_record_ids UUID[] NOT NULL,

    CONSTRAINT audit_period_freeze_revisions_revision_chk
        CHECK (revision >= 2),
    CONSTRAINT audit_period_freeze_revisions_reason_chk
        CHECK (reason IN ('amended_by_import')),
    CONSTRAINT audit_period_freeze_revisions_justification_len
        CHECK (char_length(btrim(justification)) BETWEEN 1 AND 2000),
    CONSTRAINT audit_period_freeze_revisions_actor_nonempty
        CHECK (length(actor) > 0),
    CONSTRAINT audit_period_freeze_revisions_records_nonempty
        CHECK (cardinality(evidence_record_ids) > 0),
    CONSTRAINT audit_period_freeze_revisions_period_rev_uniq
        UNIQUE (tenant_id, audit_period_id, revision),
    FOREIGN KEY (tenant_id, audit_period_id)
        REFERENCES audit_periods(tenant_id, id) ON DELETE RESTRICT
);

ALTER TABLE audit_period_freeze_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_period_freeze_revisions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON audit_period_freeze_revisions
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON audit_period_freeze_revisions
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON audit_period_freeze_revisions TO atlas_app;

-- ===== 4. samples.freeze_revision =====

ALTER TABLE samples
    ADD COLUMN freeze_revision INTEGER NULL;

ALTER TABLE samples
    ADD CONSTRAINT samples_freeze_revision_chk
        CHECK (freeze_revision IS NULL OR freeze_revision >= 1);

-- The sample_drawn log row carries it too, so the draw is reproducible from
-- the log alone: same seed, same population, same revision.
ALTER TABLE sample_audit_log
    ADD COLUMN freeze_revision INTEGER NULL;

-- ===== 5. audit_period_audit_log action =====

ALTER TABLE audit_period_audit_log
    DROP CONSTRAINT audit_period_audit_log_action_chk;

ALTER TABLE audit_period_audit_log
    ADD CONSTRAINT audit_period_audit_log_action_chk
    CHECK (action IN (
        'period_created',
        'period_frozen',
        'freeze_rejected_already_frozen',
        'population_attached',
        'auditor_assigned',
        'auditor_removed',
        'period_amended_by_import'
    ));
