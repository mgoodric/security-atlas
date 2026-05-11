-- security-atlas — evidence ledger write surface (slice 013).
--
-- Extends the slice-002 `evidence_records` table with the columns the push
-- endpoint needs (idempotency_key, evidence_kind, schema_version, the
-- source_attribution JSONB, credential_id, ingestion_path, control_ref),
-- hardens the RLS shape into a real append-only surface (no UPDATE / no
-- DELETE policies → bound by FORCE ROW LEVEL SECURITY), and adds a sibling
-- `evidence_audit_log` table that records every push attempt — accepted or
-- rejected — keyed by credential id (AC-7).
--
-- Constitutional invariants this migration honors:
--   #2  Ingestion and evaluation are separated stages; this table is the
--       append-only ledger between them. No UPDATE / DELETE policy is
--       installed for the application role, so evaluation code cannot
--       mutate source-of-truth evidence even if it tried.
--   #5  Tenant-scoped writes go through RLS WITH CHECK; the credential
--       check that the platform layer also performs is the second leg of
--       defense in depth.
--   #9  Manual evidence is first-class — `ingestion_path` enumerates
--       'push', 'pull', 'subscribe', 'manual_upload'; the column does NOT
--       discriminate value (pull and push live in the same ledger).
--
-- Drift notes (resolved by this slice; called out in PR body):
--   * Slice 002 made `control_id UUID NOT NULL` with a composite FK to
--     controls(tenant_id, id). The EvidenceSDK proto field is `string
--     control_id` — free-form, can be a UUID OR a string anchor like
--     `scf:VPM-04`. Slice 013 relaxes the column to NULL and adds a
--     sibling `control_ref TEXT NOT NULL` for the free-form value. The
--     composite FK is preserved on the optional UUID path — when the
--     caller pushes a UUID, the FK still rejects cross-tenant references
--     (slice 002 D3 invariant); when the caller pushes an SCF anchor,
--     `control_id` is NULL and `control_ref` holds the value.
--   * Slice 002 installed a `USING`-only RLS policy on evidence_records
--     (`current_tenant_matches(tenant_id)`). That blocks reads but does
--     NOT block a tenant from inserting rows that carry a different
--     tenant_id. Slice 013 replaces the single policy with the
--     tenant_or_catalog-style split used by slices 014 and 017:
--     tenant_read, tenant_insert (WITH CHECK), and explicit ABSENCE of
--     tenant_update / tenant_delete policies. With FORCE ROW LEVEL
--     SECURITY, the absence of an UPDATE policy on a permissive
--     command-restricted table means UPDATE is denied for atlas_app —
--     the table is append-only at the role level.
--   * Slice 036 (S3 object store) is `not-ready`. AC-6 (payload > 1MB →
--     redirect to S3) cannot be fully satisfied here; slice 013 enforces
--     a 1MB body-size limit at the HTTP/gRPC layer and rejects oversized
--     payloads with a clear error pointing the caller at `payload_uri`.
--     The `payload_uri` column already exists from slice 002.

-- ===== 1. Extend evidence_records with the slice-013 push columns =====

ALTER TABLE evidence_records
    ADD COLUMN idempotency_key   TEXT NULL,
    ADD COLUMN evidence_kind     TEXT NULL,
    ADD COLUMN schema_version    TEXT NULL,
    ADD COLUMN credential_id     TEXT NULL,
    ADD COLUMN ingestion_path    TEXT NOT NULL DEFAULT 'push',
    ADD COLUMN source_attribution JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN control_ref       TEXT NOT NULL DEFAULT '';

-- Constraints land after the columns so the DEFAULTs can backfill any
-- pre-existing rows from slice 002 fixtures (none in real deployments;
-- this is defense in depth).

ALTER TABLE evidence_records
    ALTER COLUMN control_id DROP NOT NULL,
    ADD CONSTRAINT evidence_records_control_ref_nonempty
        CHECK (length(control_ref) > 0),
    ADD CONSTRAINT evidence_records_ingestion_path_chk
        CHECK (ingestion_path IN ('push', 'pull', 'subscribe', 'manual_upload', 'webhook'));

-- Idempotency dedup. Per EVIDENCE_SDK §4.4 the key must be unique within a
-- 24-hour window per tenant. Postgres can't express "trailing 24h" as a
-- UNIQUE constraint, so the constraint covers ALL active keys and the
-- platform layer enforces the 24h skew on observed_at. Partial index on
-- (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL keeps the
-- backfill of pre-slice-013 rows (where the column may be NULL) from
-- conflicting with each other (NULLs-distinct on UNIQUE, per
-- feedback_postgres_constraints.md).
CREATE UNIQUE INDEX evidence_records_tenant_idem_uniq
    ON evidence_records (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Hot-path lookups for the push handler's idempotency check.
CREATE INDEX idx_evidence_records_tenant_kind_observed
    ON evidence_records (tenant_id, evidence_kind, observed_at DESC)
    WHERE evidence_kind IS NOT NULL;

CREATE INDEX idx_evidence_records_credential
    ON evidence_records (tenant_id, credential_id, ingested_at DESC)
    WHERE credential_id IS NOT NULL;

-- ===== 2. Harden RLS into a real append-only surface =====

-- The slice-002 USING-only policy is replaced with a split that mirrors
-- slices 014 and 017: explicit read, explicit insert WITH CHECK, no
-- update or delete policy at all. FORCE ROW LEVEL SECURITY + no policy
-- for a command means atlas_app cannot execute that command on the
-- table; UPDATE and DELETE are therefore role-denied (the GRANT remains
-- so a future BYPASSRLS migration can still run DDL).
DROP POLICY IF EXISTS tenant_isolation ON evidence_records;

CREATE POLICY tenant_read ON evidence_records
    FOR SELECT
    USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_insert ON evidence_records
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

-- Intentionally NO POLICY for UPDATE or DELETE. RLS-without-a-matching-policy
-- under FORCE ROW LEVEL SECURITY blocks the row from being touched. The
-- combined effect: atlas_app can SELECT (its own rows) and INSERT (its own
-- rows) but cannot UPDATE or DELETE anything. Append-only by construction.

-- ===== 3. Audit log =====
--
-- One row per push attempt (accepted or rejected). Keyed by credential id
-- (slice 003's credstore mints `key_<32hex>` identifiers). Captures the
-- decision so the audit trail survives even when the attempt was rejected
-- and no evidence row was written.
--
-- Tenant-scoped, append-only (same RLS shape as evidence_records).

CREATE TABLE evidence_audit_log (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    credential_id   TEXT NOT NULL,
    decision        TEXT NOT NULL,
    reason_code     TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NULL,
    evidence_kind   TEXT NULL,
    record_id       UUID NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT evidence_audit_log_decision_chk
        CHECK (decision IN (
            'accepted',
            'deduplicated',
            'rejected_validation',
            'rejected_unknown_kind',
            'rejected_idempotency_mismatch',
            'rejected_scope_violation',
            'rejected_observed_at_skew',
            'rejected_oversized',
            'rejected_rate_limit',
            'rejected_unauthenticated',
            'rejected_internal_error'
        ))
);

CREATE INDEX idx_evidence_audit_log_tenant_received
    ON evidence_audit_log (tenant_id, received_at DESC);

CREATE INDEX idx_evidence_audit_log_credential
    ON evidence_audit_log (tenant_id, credential_id, received_at DESC);

ALTER TABLE evidence_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE evidence_audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON evidence_audit_log
    FOR SELECT
    USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_insert ON evidence_audit_log
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

-- No update / delete policies — append-only.

GRANT SELECT, INSERT, UPDATE, DELETE ON evidence_audit_log TO atlas_app;
