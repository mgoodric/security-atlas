-- security-atlas — S3 artifact store (slice 036).
--
-- Closes the AC-6 partial from slice 013: payloads >1 MiB are rejected at
-- the push endpoint today; this slice provides the storage destination
-- the caller can redirect to. The blob itself lives in S3 (or any S3-
-- compatible backend; MinIO is the local-dev default). This table holds
-- the metadata Postgres needs to gate access: who owns it, what's its
-- content hash, how big is it, when was it uploaded.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the database layer — four-policy RLS
--       (tenant_read/write/update/delete) under FORCE ROW LEVEL SECURITY,
--       matching slices 014, 017, 024.
--   #2  Append-only audit log — artifact_access_log captures every
--       upload + download; explicit absence of UPDATE/DELETE policies
--       (only SELECT + INSERT) keeps the audit record from being mutated.
--
-- Wire shape notes:
--   * storage_key is the RELATIVE S3 object key. Format is
--     'tenant-{tenant_uuid}/{artifact_uuid}'. Two flat segments, both
--     server-generated UUIDs — no user-controlled segment, no path
--     traversal class. The full `payload_uri` ('s3://{bucket}/{key}')
--     is constructed at response time from a runtime config; the bucket
--     is not stored in the row so a deployment can repoint without a
--     data migration.
--   * content_hash is the LOWERCASE HEX sha256 of the uploaded bytes.
--     Server-computed during the upload handler; clients cannot supply
--     a trusted hash (anti-criterion P0).
--   * size_bytes is in bytes; BIGINT for headroom (long tail of large
--     evidence bundles — annual SOC 2 export packs).
--   * uploaded_by is the credential id from credstore (slice 003,
--     'key_<32hex>'); makes the audit log self-explanatory without
--     joining back to a user table.

-- ===== artifacts =====

CREATE TABLE artifacts (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    storage_key     TEXT NOT NULL,
    content_hash    TEXT NULL,
    size_bytes      BIGINT NOT NULL,
    content_type    TEXT NOT NULL,
    uploaded_by     TEXT NOT NULL,
    uploaded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- size_bytes must be positive; zero-byte artifacts are rejected at
    -- the handler layer too, but DB defence-in-depth is cheap.
    CONSTRAINT artifacts_size_positive CHECK (size_bytes > 0),

    -- storage_key must be non-empty; the handler always generates a
    -- value, but a buggy direct INSERT shouldn't land an empty key.
    CONSTRAINT artifacts_storage_key_nonempty CHECK (length(storage_key) > 0),

    -- content_type must be non-empty for the same reason.
    CONSTRAINT artifacts_content_type_nonempty CHECK (length(content_type) > 0),

    -- uploaded_by must be non-empty (audit trail integrity).
    CONSTRAINT artifacts_uploaded_by_nonempty CHECK (length(uploaded_by) > 0)
);

-- Composite uniqueness across (tenant_id, id) lets future slices link
-- back to artifacts with a cross-tenant-safe FK (e.g., a 'evidence ->
-- artifact' relation would use ON (tenant_id, artifact_id)). Mirrors
-- the slice-002/024 pattern.
ALTER TABLE artifacts
    ADD CONSTRAINT artifacts_tenant_id_unique UNIQUE (tenant_id, id);

-- One storage_key per tenant — keys are derived from
-- 'tenant-{tenant_uuid}/{artifact_uuid}', so collisions can only happen
-- if a caller bypasses the application; the constraint catches that.
CREATE UNIQUE INDEX artifacts_tenant_storage_key_uniq
    ON artifacts (tenant_id, storage_key);

-- Partial UNIQUE on (tenant_id, content_hash) for upload-time dedup. NULL
-- is allowed (future paths may defer hash computation), and the WHERE
-- clause sidesteps the NULLs-distinct-on-UNIQUE Postgres default (see
-- feedback_postgres_constraints.md). When present, the hash is unique
-- per tenant; re-uploads with the same content can be resolved to the
-- existing artifact row.
CREATE UNIQUE INDEX artifacts_tenant_content_hash_uniq
    ON artifacts (tenant_id, content_hash)
    WHERE content_hash IS NOT NULL;

-- Hot path: list-by-tenant ordered by upload time (admin "recent
-- uploads" panel and audit replay).
CREATE INDEX idx_artifacts_tenant_uploaded_at
    ON artifacts (tenant_id, uploaded_at DESC);

-- ===== Row-Level Security =====

ALTER TABLE artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE artifacts FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON artifacts
    FOR SELECT
    USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_write ON artifacts
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

CREATE POLICY tenant_update ON artifacts
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));

CREATE POLICY tenant_delete ON artifacts
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- ===== artifact_access_log =====
--
-- One row per upload + download. Mirrors evidence_audit_log shape from
-- slice 013, but with an `action` vocabulary instead of `decision`
-- (this is access logging, not an ingestion accept/reject decision).
-- Append-only: SELECT + INSERT policies, no UPDATE/DELETE policy under
-- FORCE ROW LEVEL SECURITY means atlas_app cannot mutate audit rows.

CREATE TABLE artifact_access_log (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    artifact_id     UUID NOT NULL,
    action          TEXT NOT NULL,
    actor           TEXT NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT artifact_access_log_action_chk
        CHECK (action IN ('upload', 'download')),

    CONSTRAINT artifact_access_log_actor_nonempty
        CHECK (length(actor) > 0),

    -- Composite FK enforces tenant boundary in the FK target — an
    -- audit row cannot point at an artifact belonging to a different
    -- tenant. CASCADE so audit rows go away with the artifact (the
    -- artifact's own deletion is itself an audit-worthy event; the
    -- caller is expected to log a final 'delete' action before the
    -- DELETE, but the FK keeps orphan rows from accumulating).
    FOREIGN KEY (tenant_id, artifact_id)
        REFERENCES artifacts(tenant_id, id) ON DELETE CASCADE
);

-- Hot path: per-artifact recent history.
CREATE INDEX idx_artifact_access_log_tenant_artifact_occurred
    ON artifact_access_log (tenant_id, artifact_id, occurred_at DESC);

-- Tenant-wide access feed (admin view, audit export).
CREATE INDEX idx_artifact_access_log_tenant_occurred
    ON artifact_access_log (tenant_id, occurred_at DESC);

ALTER TABLE artifact_access_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE artifact_access_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON artifact_access_log
    FOR SELECT
    USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_write ON artifact_access_log
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

-- No UPDATE or DELETE policy by design — audit rows are append-only.
-- atlas_migrate has BYPASSRLS so the down-migration can still drop the
-- table under FORCE.

GRANT SELECT, INSERT, UPDATE, DELETE ON
    artifacts, artifact_access_log
TO atlas_app;
