-- security-atlas — sample-pull primitives (slice 026).
--
-- Three tables that compose the auditor's reproducible-sampling primitive:
--   populations          - what a sample is drawn FROM (control + scope_predicate
--                          + time_window). Frozen-at horizon is captured at
--                          create-time so slice 028 (AuditPeriod freezing) can
--                          stamp the column and have the SELECT path honor it
--                          without retro-fits.
--   samples              - what was DRAWN. (population_id, n, seed, created_by,
--                          created_at). The seed is the deterministic-reproduce
--                          handle: same seed + same population -> identical N
--                          evidence record ids in the same order (anti-criterion
--                          P0).
--   sample_annotations   - per-record auditor decisions (passed | failed |
--                          not-applicable + freeform notes). One annotation row
--                          per (sample, evidence_record). Tested samples carry
--                          these; untested samples have none.
--
-- Plus one audit-log table:
--   sample_audit_log     - one row per sample pull. Captures the seed -> sample
--                          mapping so the audit trail is replay-able even if
--                          the samples table is dropped. Parallel to slice 013's
--                          evidence_audit_log shape, but the action vocabulary
--                          is sampling-specific.
--
-- Constitutional invariants this migration honors:
--   #2  Ingestion and evaluation separated; sampling READS from the evidence
--       ledger and never writes to it. Three tables introduced here own their
--       own state; no UPDATE/DELETE policy on evidence_records is touched.
--   #6  Tenant isolation at the database layer. Every table gets the four-policy
--       split (tenant_read FOR SELECT, tenant_write FOR INSERT WITH CHECK,
--       tenant_update FOR UPDATE USING + WITH CHECK, tenant_delete FOR DELETE)
--       under FORCE ROW LEVEL SECURITY. Matches the slice 014/017/018/036
--       pattern exactly.
--   #10 Audit-period freezing forward-compat. populations.frozen_at is NULL by
--       default in this slice; slice 028 will write it when an AuditPeriod is
--       frozen. The store-side SELECT path uses
--       `observed_at <= COALESCE(populations.frozen_at, 'infinity')` so the
--       gate is a no-op until slice 028 lands.
--
-- Anti-criteria honored at the schema layer:
--   - No mutation of sampled evidence_records: the store never UPDATEs/DELETEs
--     evidence_records, and slice 013 already removed UPDATE/DELETE policies
--     on that table, so even a bug here cannot mutate the ledger.
--   - Non-deterministic sampling is impossible: samples.seed is NOT NULL, and
--     the integration test asserts deterministic reproducibility.
--   - Post-frozen evidence is excluded by the populations.frozen_at filter (no-op
--     this slice, real once slice 028 ships).

-- ===== 1. populations =====

CREATE TABLE populations (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    control_id          UUID NOT NULL,
    -- scope_predicate uses the same JSON-AST shape as slice 017's
    -- applicability_expr and slice 018's framework_scope predicate. Empty
    -- object ({}) and {"op":"true"} both mean "match every cell" -- the
    -- scope.Evaluate helper canonicalizes either form.
    scope_predicate     JSONB NOT NULL,
    time_window_start   TIMESTAMPTZ NOT NULL,
    time_window_end     TIMESTAMPTZ NOT NULL,
    -- AC-5: forward-compat hook for audit-period freezing. NULL means "live"
    -- (no horizon); slice 028 will set this when a period is frozen so the
    -- population draws from observed_at <= frozen_at only. The COALESCE in
    -- the query path makes NULL behave as +infinity.
    frozen_at           TIMESTAMPTZ NULL,
    row_count           BIGINT NOT NULL DEFAULT 0,
    created_by          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT populations_window_valid
        CHECK (time_window_start <= time_window_end),
    CONSTRAINT populations_created_by_nonempty
        CHECK (length(created_by) > 0),
    -- Composite FK to controls(tenant_id, id) blocks cross-tenant references
    -- (slice 002 D3 invariant). When tenant_id and control_id don't agree the
    -- INSERT fails with 23503 -- the application surfaces it as 400.
    FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls(tenant_id, id) ON DELETE RESTRICT
);

-- Composite uniqueness across (tenant_id, id) lets samples reference back
-- with a cross-tenant-safe FK.
ALTER TABLE populations
    ADD CONSTRAINT populations_tenant_id_unique UNIQUE (tenant_id, id);

CREATE INDEX idx_populations_tenant_created_at
    ON populations (tenant_id, created_at DESC);

CREATE INDEX idx_populations_tenant_control
    ON populations (tenant_id, control_id, created_at DESC);

-- ===== 2. samples =====

CREATE TABLE samples (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    population_id   UUID NOT NULL,
    n               INTEGER NOT NULL,
    -- seed is the user-supplied text; the application hashes it to a 32-byte
    -- ChaCha8 key. The text is stored verbatim so a future audit can
    -- recompute the SHA-256 -> ChaCha8 -> Fisher-Yates pipeline exactly.
    seed            TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT samples_n_positive CHECK (n > 0),
    CONSTRAINT samples_seed_nonempty CHECK (length(seed) > 0),
    CONSTRAINT samples_created_by_nonempty CHECK (length(created_by) > 0),

    -- Composite FK pins the sample to its tenant-owned population row.
    FOREIGN KEY (tenant_id, population_id)
        REFERENCES populations(tenant_id, id) ON DELETE RESTRICT
);

ALTER TABLE samples
    ADD CONSTRAINT samples_tenant_id_unique UNIQUE (tenant_id, id);

CREATE INDEX idx_samples_tenant_created_at
    ON samples (tenant_id, created_at DESC);

CREATE INDEX idx_samples_population
    ON samples (tenant_id, population_id, created_at DESC);

-- sample_evidence is the realized N-record selection per sample. Stored
-- explicitly so a re-audit returns the same records regardless of any
-- subsequent population mutations (none allowed today, but defensive against
-- slice 028's frozen_at writes mutating the universe under a sample's feet).
-- `ordinal` is the 0-based position in the deterministic shuffle; auditor UIs
-- render in this order.
CREATE TABLE sample_evidence (
    sample_id           UUID NOT NULL,
    tenant_id           UUID NOT NULL,
    evidence_record_id  UUID NOT NULL,
    ordinal             INTEGER NOT NULL,
    PRIMARY KEY (sample_id, evidence_record_id),
    CONSTRAINT sample_evidence_ordinal_nonneg CHECK (ordinal >= 0),
    FOREIGN KEY (tenant_id, sample_id)
        REFERENCES samples(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_sample_evidence_tenant_sample_ordinal
    ON sample_evidence (tenant_id, sample_id, ordinal);

-- Note: evidence_record_id has a single-column FK (no composite tenant-FK)
-- because evidence_records does not expose a UNIQUE on (tenant_id, id).
-- Cross-tenant linkage is still blocked: RLS on sample_evidence requires
-- the tenant_id match, and the store-side INSERT path resolves the
-- evidence_record_id within the active tenant scope before writing.
ALTER TABLE sample_evidence
    ADD CONSTRAINT sample_evidence_evidence_fk
    FOREIGN KEY (evidence_record_id) REFERENCES evidence_records(id) ON DELETE RESTRICT;

-- ===== 3. sample_annotations =====

CREATE TABLE sample_annotations (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    sample_id           UUID NOT NULL,
    evidence_record_id  UUID NOT NULL,
    result              TEXT NOT NULL,
    annotated_by        TEXT NOT NULL,
    annotated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    notes               TEXT NOT NULL DEFAULT '',

    CONSTRAINT sample_annotations_result_chk
        CHECK (result IN ('passed', 'failed', 'not-applicable')),
    CONSTRAINT sample_annotations_annotated_by_nonempty
        CHECK (length(annotated_by) > 0),

    FOREIGN KEY (tenant_id, sample_id)
        REFERENCES samples(tenant_id, id) ON DELETE CASCADE
);

ALTER TABLE sample_annotations
    ADD CONSTRAINT sample_annotations_evidence_fk
    FOREIGN KEY (evidence_record_id) REFERENCES evidence_records(id) ON DELETE RESTRICT;

-- One annotation per (sample, evidence_record). Re-annotating UPSERTs by
-- the application layer; the unique index gives the application a stable
-- ON CONFLICT target.
CREATE UNIQUE INDEX sample_annotations_sample_evidence_uniq
    ON sample_annotations (tenant_id, sample_id, evidence_record_id);

CREATE INDEX idx_sample_annotations_tenant_sample
    ON sample_annotations (tenant_id, sample_id, annotated_at DESC);

-- ===== 4. sample_audit_log =====
--
-- AC-6: every sample pull writes a row capturing the seed -> sample mapping.
-- This is the re-audit log: if a future auditor needs to verify that a stated
-- seed produced a stated sample, they read this table. Append-only by RLS
-- shape (SELECT + INSERT policies only).

CREATE TABLE sample_audit_log (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    action          TEXT NOT NULL,
    actor           TEXT NOT NULL,
    population_id   UUID NULL,
    sample_id       UUID NULL,
    seed            TEXT NULL,
    n_requested     INTEGER NULL,
    n_returned      INTEGER NULL,
    reason_code     TEXT NOT NULL DEFAULT '',
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT sample_audit_log_action_chk
        CHECK (action IN (
            'population_created',
            'sample_drawn',
            'sample_annotated',
            'sample_rejected'
        )),
    CONSTRAINT sample_audit_log_actor_nonempty
        CHECK (length(actor) > 0)
);

CREATE INDEX idx_sample_audit_log_tenant_occurred
    ON sample_audit_log (tenant_id, occurred_at DESC);

CREATE INDEX idx_sample_audit_log_tenant_sample
    ON sample_audit_log (tenant_id, sample_id, occurred_at DESC)
    WHERE sample_id IS NOT NULL;

-- ===== Row-Level Security =====
--
-- Four-policy split everywhere, matching slices 014/017/018/036.
-- FORCE ROW LEVEL SECURITY binds the table owner too; atlas_migrate is
-- BYPASSRLS so DDL still works.

ALTER TABLE populations ENABLE ROW LEVEL SECURITY;
ALTER TABLE populations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON populations
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON populations
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON populations
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON populations
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE samples ENABLE ROW LEVEL SECURITY;
ALTER TABLE samples FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON samples
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON samples
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON samples
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON samples
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE sample_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE sample_evidence FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON sample_evidence
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON sample_evidence
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON sample_evidence
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON sample_evidence
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE sample_annotations ENABLE ROW LEVEL SECURITY;
ALTER TABLE sample_annotations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON sample_annotations
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON sample_annotations
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON sample_annotations
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON sample_annotations
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- sample_audit_log is append-only: SELECT + INSERT policies only. No
-- UPDATE/DELETE policies under FORCE ROW LEVEL SECURITY means atlas_app
-- cannot mutate audit rows. Matches slice 013's evidence_audit_log and
-- slice 036's artifact_access_log shape.
ALTER TABLE sample_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE sample_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON sample_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON sample_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON
    populations, samples, sample_evidence, sample_annotations, sample_audit_log
TO atlas_app;
