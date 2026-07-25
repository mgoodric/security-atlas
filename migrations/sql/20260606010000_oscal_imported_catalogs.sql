-- migrations/sql/20260606010000_oscal_imported_catalogs.sql
--
-- Slice 492 — OSCAL catalog import (ingest direction of invariant #8).
--
-- Three NEW tables persist an imported OSCAL catalog as a DISTINCT,
-- provenance-labeled, tenant-scoped set — never the bundled SCF spine
-- (`scf_anchors`, which stays untouched: P0-492-4). An imported control
-- maps requirement -> SCF anchor only (invariant #7 / P0-492-1); it never
-- carries a requirement -> requirement edge.
--
--   imported_catalogs            — one row per import run. Provenance:
--                                  source ('oscal-import'), imported_by,
--                                  source_sha256 (lowercase hex sha256 of
--                                  the exact inbound bytes — tamper-evident,
--                                  threat-model S/T), source_label
--                                  (operator-declared framework label),
--                                  oscal_version, control_count.
--   imported_catalog_controls    — one row per imported control. Carries
--                                  the OSCAL source_control_id, title,
--                                  flattened statement prose, group_path,
--                                  and a NULLABLE scf_anchor_id (the SCF
--                                  scf_id string the control maps TO; NULL =
--                                  "needs operator mapping", the slice-155
--                                  questionnaire pattern minus the AI).
--   imported_catalog_audit_log   — append-only (SELECT + INSERT RLS only)
--                                  import audit trail (AC-7 / threat-model R).
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the DB layer. imported_catalogs +
--       imported_catalog_controls use the four-policy split under FORCE
--       ROW LEVEL SECURITY (slice 014/017/018/021/026/028/035/036/059/155
--       precedent). imported_catalog_audit_log uses SELECT + INSERT
--       policies only — the absence of UPDATE/DELETE under FORCE makes it
--       append-only (slice 011/013/019/035 precedent).
--   #7  SCF is the canonical control catalog. scf_anchor_id maps TO an SCF
--       anchor's scf_id; there is no FK to another imported requirement.
--   #8  OSCAL is the wire format (ingest direction).
--
-- NOT-NULL discipline: all three tables are NEW (zero ALTER on a slice-002
-- table), so no internal/db/integration_test.go helper-fixture patches
-- are required.
--
-- Idempotency / reversibility: pure CREATE TABLE / CREATE INDEX / CREATE
-- POLICY. Reversible via the paired .down.sql (DROP in dependency order).

-- ===== imported_catalogs =====

CREATE TABLE imported_catalogs (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL,
    -- Constant 'oscal-import' for this slice; a column (not a literal) so a
    -- future ingest source (e.g. 'cprt-import') is additive.
    source         TEXT NOT NULL DEFAULT 'oscal-import',
    -- The operator/credential that performed the import (AC-4 provenance).
    imported_by    TEXT NOT NULL,
    -- lowercase hex sha256 of the exact inbound OSCAL document bytes.
    source_sha256  TEXT NOT NULL,
    -- Operator-declared framework label, e.g. "NIST SP 800-53 rev5".
    source_label   TEXT NOT NULL DEFAULT '',
    -- The catalog's declared OSCAL version, echoed from the validated doc.
    oscal_version  TEXT NOT NULL DEFAULT '',
    -- The catalog metadata title, echoed for display.
    catalog_title  TEXT NOT NULL DEFAULT '',
    -- Number of controls imported in this run.
    control_count  INTEGER NOT NULL DEFAULT 0,
    imported_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT imported_catalogs_source_chk
        CHECK (source IN ('oscal-import')),
    CONSTRAINT imported_catalogs_imported_by_nonempty
        CHECK (length(imported_by) > 0),
    CONSTRAINT imported_catalogs_sha256_format
        CHECK (source_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE INDEX idx_imported_catalogs_tenant_imported
    ON imported_catalogs (tenant_id, imported_at DESC);

ALTER TABLE imported_catalogs ENABLE ROW LEVEL SECURITY;
ALTER TABLE imported_catalogs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON imported_catalogs
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON imported_catalogs
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON imported_catalogs
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON imported_catalogs
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON imported_catalogs TO atlas_app;

-- ===== imported_catalog_controls =====
--
-- One row per control in an imported catalog. scf_anchor_id is NULLABLE:
-- NULL means "the importer found no deterministic SCF crosswalk for this
-- control; it needs operator mapping" (decision D1). The control is NEVER
-- dropped for being unmappable — import-unmapped-and-flag.
--
-- scf_anchor_id is a free-form scf_id string (e.g. "IAC-06"), matching the
-- slice-155 questionnaire_questions precedent — scf_anchors.scf_id is
-- unique only within framework_version_id, so a single-column FK isn't
-- expressible. A v2 follow-on resolves to scf_anchors.id (UUID) + the
-- framework_version_id for cross-version semantics.

CREATE TABLE imported_catalog_controls (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    imported_catalog_id UUID NOT NULL
        REFERENCES imported_catalogs (id) ON DELETE CASCADE,
    -- The control's OSCAL control-id, exactly as authored in the document.
    source_control_id   TEXT NOT NULL,
    title               TEXT NOT NULL DEFAULT '',
    -- The control's prose statement, flattened from the OSCAL part tree.
    statement           TEXT NOT NULL DEFAULT '',
    -- The '/'-joined OSCAL group-title chain the control sits under.
    group_path          TEXT NOT NULL DEFAULT '',
    -- Mapping TO the canonical SCF anchor (free-form scf_id). NULL = needs
    -- operator mapping. Requirement -> SCF anchor only (invariant #7).
    scf_anchor_id       TEXT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT imported_catalog_controls_source_id_nonempty
        CHECK (length(source_control_id) > 0),
    CONSTRAINT imported_catalog_controls_unique_per_catalog
        UNIQUE (imported_catalog_id, source_control_id)
);

CREATE INDEX idx_imported_catalog_controls_tenant_catalog
    ON imported_catalog_controls (tenant_id, imported_catalog_id);
CREATE INDEX idx_imported_catalog_controls_tenant_anchor
    ON imported_catalog_controls (tenant_id, scf_anchor_id)
    WHERE scf_anchor_id IS NOT NULL;

ALTER TABLE imported_catalog_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE imported_catalog_controls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON imported_catalog_controls
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON imported_catalog_controls
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON imported_catalog_controls
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON imported_catalog_controls
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON imported_catalog_controls TO atlas_app;

-- ===== imported_catalog_audit_log =====
--
-- Append-only import audit trail (AC-7 / threat-model R). SELECT + INSERT
-- RLS only under FORCE ROW LEVEL SECURITY makes the table immutable once
-- written (slice 011/013/019/035 append-only precedent). A rejected import
-- (validation failure) still records an 'import_rejected' row.

CREATE TABLE imported_catalog_audit_log (
    id             UUID PRIMARY KEY,
    tenant_id      UUID NOT NULL,
    -- NULL when the import was rejected before any imported_catalogs row
    -- was committed (the transaction rolled back). Populated on success.
    catalog_id     UUID NULL,
    action         TEXT NOT NULL,
    actor          TEXT NOT NULL,
    source_sha256  TEXT NOT NULL,
    source_label   TEXT NOT NULL DEFAULT '',
    control_count  INTEGER NOT NULL DEFAULT 0,
    detail         JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT imported_catalog_audit_log_action_chk
        CHECK (action IN ('catalog_imported', 'import_rejected')),
    CONSTRAINT imported_catalog_audit_log_actor_nonempty
        CHECK (length(actor) > 0)
);

CREATE INDEX idx_imported_catalog_audit_log_tenant_occurred
    ON imported_catalog_audit_log (tenant_id, occurred_at DESC);

ALTER TABLE imported_catalog_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE imported_catalog_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON imported_catalog_audit_log
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON imported_catalog_audit_log
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON imported_catalog_audit_log TO atlas_app;
