-- security-atlas — Walkthrough recording primitive (slice 027).
--
-- Canvas §8.3 walkthrough primitive: an auditor or control owner records a
-- narrative walkthrough of how a control operates, with optional annotated
-- screen captures and transcript. Each walkthrough is content-hashed
-- (sha256 over canonical JSON, per ADR 0003's content-only-inputs pattern)
-- and stored as an audit artifact with provenance. Walkthroughs become
-- part of the audit-export bundle (slice 030 OSCAL assessment-results).
--
-- Three tables introduced:
--   walkthroughs              - tenant-scoped narrative + content commitment.
--                               Lifecycle: draft -> finalized (terminal).
--                               Optional audit_period_id pin; when the
--                               period is frozen, the application layer
--                               rejects mutation (P0-3).
--   walkthrough_attachments   - tenant-scoped image / transcript binary
--                               metadata. The blob lives in the slice 036
--                               artifact store (S3) under the per-tenant
--                               storage_key. Annotation metadata (image
--                               regions + notes) is jsonb on this row, NOT
--                               on the artifact blob.
--   walkthrough_audit_log     - append-only lifecycle log (created /
--                               attachment_added / finalized / tamper_detected).
--                               Picked up by the slice 062 admin audit-log
--                               view through the to_regclass() probe.
--
-- Constitutional invariants honored:
--   #2  Ingestion / evaluation separation: walkthroughs are independent
--       audit artifacts. The hash is computed at write time over the
--       narrative + transcript + control_id + created_by + created_at +
--       sorted attachment content_hashes. The hash inputs are content-
--       derived; ADR 0003's content-only-inputs principle is applied
--       identically here.
--   #6  Tenant isolation at the DB layer. walkthroughs +
--       walkthrough_attachments use the four-policy split under FORCE
--       ROW LEVEL SECURITY (slice 014/017/018/021/026/028/035/036/059
--       pattern). walkthrough_audit_log uses SELECT + INSERT policies
--       only -- the absence of UPDATE/DELETE under FORCE makes the table
--       append-only (slice 011/013/019/035/059 pattern).
--   #10 Audit-period freezing: walkthroughs optionally pin to an
--       audit_period_id. The application layer rejects mutation when the
--       referenced period is frozen (Anti-criterion P0-3). The DB CHECK
--       enforces (status = 'finalized') is immutable once set; the
--       handler enforces the additional period-freeze gate.
--
-- Anti-criteria honored at the schema layer (P0):
--   - Skipping hash / tamper detection (P0-1): canonical_hash is NOT NULL
--     on every row; the application Get path re-computes and surfaces a
--     tamper_detected flag at retrieval (AC-6). The DB cannot enforce
--     content-vs-hash agreement (the hash inputs include sibling table
--     rows), so the application layer is authoritative; the schema makes
--     the hash mandatory.
--   - Auto-generated text without explicit user authorship (P0-2):
--     created_by is NOT NULL + CHECK length(created_by) > 0; every row
--     carries an authoring identity. The handler enforces that the
--     created_by matches the authenticated credential.
--   - Mutation after audit-period freeze (P0-3): the handler reads
--     audit_periods.status and rejects with 409 when the period is
--     frozen. The schema constraint (audit_period_fk) keeps the period
--     reference cross-tenant-safe via composite (tenant_id, audit_period_id).
--
-- Migration is reversible via 20260511000025_walkthroughs.down.sql which
-- drops all three tables in dependency order.

-- ===== 1. walkthroughs =====
--
-- One row per recorded walkthrough. Status lifecycle:
--   draft       - in-progress; narrative + attachments may be added.
--   finalized   - terminal; hash committed, no further mutation allowed.
--                 The application path that flips draft->finalized
--                 recomputes the canonical hash one final time so the
--                 stored hash matches the as-finalized content.

CREATE TABLE walkthroughs (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    audit_period_id     UUID NULL,
    control_id          UUID NOT NULL,
    narrative           TEXT NOT NULL,
    transcript          TEXT NULL,
    canonical_hash      BYTEA NOT NULL,
    status              TEXT NOT NULL DEFAULT 'draft',
    created_by          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT walkthroughs_status_chk
        CHECK (status IN ('draft', 'finalized')),
    CONSTRAINT walkthroughs_narrative_nonempty
        CHECK (length(narrative) > 0),
    CONSTRAINT walkthroughs_created_by_nonempty
        CHECK (length(created_by) > 0),
    CONSTRAINT walkthroughs_canonical_hash_len
        CHECK (octet_length(canonical_hash) = 32),

    -- Composite FK to controls(tenant_id, id) -- blocks cross-tenant
    -- linkage. controls has the (tenant_id, id) composite uniqueness
    -- from slice 002.
    FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls(tenant_id, id) ON DELETE RESTRICT,

    -- Composite FK to audit_periods(tenant_id, id) -- mirrors slice 028's
    -- populations.audit_period_id pattern. NULL means "live" walkthrough
    -- (not yet pinned to a period); when set, the application layer
    -- enforces the freeze-immutability invariant.
    FOREIGN KEY (tenant_id, audit_period_id)
        REFERENCES audit_periods(tenant_id, id) ON DELETE RESTRICT
);

-- (tenant_id, id) composite uniqueness for cross-tenant-safe FKs from
-- walkthrough_attachments + walkthrough_audit_log.
ALTER TABLE walkthroughs
    ADD CONSTRAINT walkthroughs_tenant_id_unique UNIQUE (tenant_id, id);

CREATE INDEX idx_walkthroughs_tenant_control
    ON walkthroughs (tenant_id, control_id, created_at DESC);

CREATE INDEX idx_walkthroughs_tenant_period
    ON walkthroughs (tenant_id, audit_period_id)
    WHERE audit_period_id IS NOT NULL;

CREATE INDEX idx_walkthroughs_tenant_created
    ON walkthroughs (tenant_id, created_at DESC);

-- ===== 2. walkthrough_attachments =====
--
-- One row per file attached to a walkthrough. The blob itself lives in
-- the slice 036 artifact store; the storage_key here is the same key
-- format (tenant-{uuid}/{uuid}) so a verifier can deref the blob without
-- a sibling-table join.
--
-- sha256_hash is the lowercase-hex sha256 of the bytes, computed
-- server-side at upload time. The walkthrough's canonical_hash is
-- computed over the SORTED set of attachment sha256_hash values, so any
-- attachment-set change (add/remove/byte-mutate) invalidates the
-- walkthrough hash (AC-6).
--
-- annotations is a free-form jsonb capturing image-region metadata (the
-- canvas leaves the exact shape to the implementer; v1 uses a simple
-- {regions: [{x,y,w,h,label}]} convention rendered by the frontend).

CREATE TABLE walkthrough_attachments (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    walkthrough_id  UUID NOT NULL,
    storage_key     TEXT NOT NULL,
    content_type    TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL,
    sha256_hash     TEXT NOT NULL,
    annotations     JSONB NOT NULL DEFAULT '{}'::jsonb,
    uploaded_by     TEXT NOT NULL,
    uploaded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT walkthrough_attachments_storage_key_nonempty
        CHECK (length(storage_key) > 0),
    CONSTRAINT walkthrough_attachments_content_type_nonempty
        CHECK (length(content_type) > 0),
    CONSTRAINT walkthrough_attachments_size_nonneg
        CHECK (size_bytes >= 0),
    -- 64 lowercase hex chars = 32 bytes. The handler always writes the
    -- lowercase form; the constraint just keeps the column shape stable.
    CONSTRAINT walkthrough_attachments_sha256_len
        CHECK (length(sha256_hash) = 64),
    CONSTRAINT walkthrough_attachments_uploaded_by_nonempty
        CHECK (length(uploaded_by) > 0),

    FOREIGN KEY (tenant_id, walkthrough_id)
        REFERENCES walkthroughs(tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX idx_walkthrough_attachments_tenant_walkthrough
    ON walkthrough_attachments (tenant_id, walkthrough_id, uploaded_at);

CREATE INDEX idx_walkthrough_attachments_tenant_uploaded
    ON walkthrough_attachments (tenant_id, uploaded_at DESC);

-- ===== 3. walkthrough_audit_log =====
--
-- Append-only lifecycle log. Picked up by the slice 062 admin audit-log
-- view via to_regclass('walkthrough_audit_log'). Action enumeration:
--   walkthrough_created      - on POST /v1/walkthroughs
--   attachment_added         - on POST /v1/walkthroughs/:id/attachments
--   walkthrough_finalized    - on POST /v1/walkthroughs/:id:finalize
--   tamper_detected          - on GET /v1/walkthroughs/:id when re-hash != stored
--   mutation_rejected_frozen - on POST /v1/walkthroughs/:id/attachments when period frozen

CREATE TABLE walkthrough_audit_log (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    walkthrough_id  UUID NOT NULL,
    action          TEXT NOT NULL,
    actor           TEXT NOT NULL,
    detail          JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT walkthrough_audit_log_action_chk
        CHECK (action IN (
            'walkthrough_created',
            'attachment_added',
            'walkthrough_finalized',
            'tamper_detected',
            'mutation_rejected_frozen'
        )),
    CONSTRAINT walkthrough_audit_log_actor_nonempty
        CHECK (length(actor) > 0)
);

CREATE INDEX idx_walkthrough_audit_log_tenant_walkthrough
    ON walkthrough_audit_log (tenant_id, walkthrough_id, occurred_at DESC);

CREATE INDEX idx_walkthrough_audit_log_tenant_occurred
    ON walkthrough_audit_log (tenant_id, occurred_at DESC);

-- ===== 4. Row-Level Security =====
--
-- Four-policy split on walkthroughs + walkthrough_attachments under
-- FORCE ROW LEVEL SECURITY (slice 014/017/018/021/026/028/035/036/059
-- pattern). SELECT + INSERT only on walkthrough_audit_log under FORCE
-- (slice 011/013/019/035/059 pattern -- absence of UPDATE/DELETE
-- policies makes the table append-only).

ALTER TABLE walkthroughs ENABLE ROW LEVEL SECURITY;
ALTER TABLE walkthroughs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON walkthroughs
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON walkthroughs
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON walkthroughs
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON walkthroughs
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE walkthrough_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE walkthrough_attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON walkthrough_attachments
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON walkthrough_attachments
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON walkthrough_attachments
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON walkthrough_attachments
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE walkthrough_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE walkthrough_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON walkthrough_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON walkthrough_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON walkthroughs TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON walkthrough_attachments TO atlas_app;
GRANT SELECT, INSERT ON walkthrough_audit_log TO atlas_app;
