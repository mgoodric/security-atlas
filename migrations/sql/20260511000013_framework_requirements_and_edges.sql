-- framework_requirements + fw_to_scf_edges — UCF graph tables for slice 007.
--
-- These are the two adjacency tables that materialize the canvas §3 graph:
-- each row in framework_requirements is one clause inside a FrameworkVersion
-- (e.g., "SOC2:2017:CC6.6"); each row in fw_to_scf_edges is one STRM-typed
-- edge from a requirement to a single SCF anchor. There is NO requirement-
-- to-requirement table: invariant 1 (canvas §3.1, "framework-to-framework
-- relationships are derived through SCF anchors, never directly") is
-- enforced at DDL level. Adding a fw_to_fw_edges table later would be a
-- constitutional violation, not a feature.
--
-- Catalog data, not tenant data: no tenant_id, no RLS. Same shape as
-- scf_anchors (slice 006). atlas_app gets SELECT; atlas_migrate (DDL role)
-- gets writes for the importer.

-- Both enums are wrapped in a DO/EXCEPTION block for re-run idempotency
-- (slice 065 bug #3): Postgres has no `CREATE TYPE IF NOT EXISTS`, and the
-- self-host bootstrap re-applies every migration on each `docker compose up`.
DO $$ BEGIN
    CREATE TYPE strm_relationship_type AS ENUM (
        'equal',
        'subset_of',
        'superset_of',
        'intersects_with',
        'no_relationship'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE crosswalk_source_attribution AS ENUM (
        'scf_official',
        'community_draft',
        'org_internal'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE framework_requirements (
    id                    UUID PRIMARY KEY,
    framework_version_id  UUID NOT NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    code                  TEXT NOT NULL,
    title                 TEXT NOT NULL,
    body                  TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (framework_version_id, code)
);

CREATE INDEX idx_framework_requirements_code
    ON framework_requirements (code);

CREATE TABLE fw_to_scf_edges (
    id                          UUID PRIMARY KEY,
    framework_requirement_id    UUID NOT NULL REFERENCES framework_requirements(id) ON DELETE CASCADE,
    -- ON DELETE CASCADE on scf_anchor_id: when an SCF release is replaced,
    -- the old anchors are deleted and their inbound edges go with them.
    -- An edge without an anchor is meaningless. Slice 006's importer-test
    -- truncation flow also depends on this; without CASCADE, every test
    -- that wipes scf_anchors would need to know about fw_to_scf_edges
    -- first — a maintenance burden that gets bigger with every new
    -- crosswalk slice.
    scf_anchor_id               UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    relationship_type           strm_relationship_type NOT NULL,
    strength                    DOUBLE PRECISION NOT NULL,
    source_attribution          crosswalk_source_attribution NOT NULL,
    rationale                   TEXT NOT NULL DEFAULT '',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fw_to_scf_edges_strength_range
        CHECK (strength >= 0.0 AND strength <= 1.0),
    -- One edge per (requirement, anchor) pair. STRM permits exactly one
    -- relationship_type between any source-target pair (NIST IR 8477 §4):
    -- you can't simultaneously be `equal` and `intersects_with`.
    UNIQUE (framework_requirement_id, scf_anchor_id)
);

CREATE INDEX idx_fw_to_scf_edges_requirement
    ON fw_to_scf_edges (framework_requirement_id);

CREATE INDEX idx_fw_to_scf_edges_anchor
    ON fw_to_scf_edges (scf_anchor_id);

CREATE INDEX idx_fw_to_scf_edges_source_attribution
    ON fw_to_scf_edges (source_attribution);

-- Catalog tables — bundled with the platform, not tenant-scoped. atlas_app
-- reads; atlas_migrate (the DDL/import role) writes. No RLS; if a tenant
-- needs to override a mapping locally, slice 011+ introduces a tenant-
-- scoped override table that joins on top.
GRANT SELECT ON framework_requirements TO atlas_app;
GRANT SELECT ON fw_to_scf_edges TO atlas_app;
