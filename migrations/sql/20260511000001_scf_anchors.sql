-- scf_anchors — the canonical Secure Controls Framework anchor catalog.
--
-- Globally bundled with the platform; not tenant-scoped. Slice 006 imports
-- the SCF JSON catalog into this table. Each row is one SCF control,
-- version-pinned to a framework_versions row. Slice 008 builds the UCF
-- graph by joining framework_requirements (forthcoming) onto these anchors
-- via STRM-typed edges.

CREATE TABLE scf_anchors (
    id                    UUID PRIMARY KEY,
    framework_version_id  UUID NOT NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    scf_id                TEXT NOT NULL,
    family                TEXT NOT NULL,
    title                 TEXT NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    subtopics             JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (framework_version_id, scf_id)
);

CREATE INDEX idx_scf_anchors_family_scf_id ON scf_anchors (family, scf_id);

-- The SCF catalog is platform-bundled. No tenant_id, no RLS — every
-- authenticated caller reads the same rows. atlas_app gets SELECT;
-- atlas_migrate (the DDL role) gets writes for the importer to use.
GRANT SELECT ON scf_anchors TO atlas_app;
