-- security-atlas — #1775: the SCF remap queue.
--
-- When `atlas-cli catalog import-scf` makes a new SCF release current, every
-- active, non-pinned control still bound to an anchor of an older release
-- gets one proposal here. The proposal reads the new release's legacy IDs
-- (#1769) to name the successor anchors and is classified:
--
--   unchanged     one successor, same title
--   renamed       one successor, different title
--   split         several successors
--   reused_id     the old code exists in the new release with a different
--                 lineage (its legacy ID names another code)
--   no_successor  no successor, or the new release carries no legacy IDs
--
-- The proposal moves nothing. status says what the #1971 D3 upgrade did
-- with the control: 'rebound' when it moved it in place (same code, same
-- title), 'open' when the control still waits for a person.
--
-- Rows are written by the migrate role during the import and read by the
-- app role through GET /v1/controls/remap-proposals; RLS keeps each tenant
-- to its own rows.

CREATE TABLE IF NOT EXISTS scf_remap_proposals (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                   UUID NOT NULL,
    control_id                  UUID NOT NULL REFERENCES controls(id) ON DELETE CASCADE,
    bundle_id                   TEXT NOT NULL,
    legacy_anchor_id            UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    legacy_scf_id               TEXT NOT NULL,
    legacy_release              TEXT NOT NULL,
    target_framework_version_id UUID NOT NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    target_release              TEXT NOT NULL,
    class                       TEXT NOT NULL
                                CONSTRAINT scf_remap_proposals_class_check
                                CHECK (class IN ('unchanged', 'renamed', 'split', 'reused_id', 'no_successor')),
    status                      TEXT NOT NULL
                                CONSTRAINT scf_remap_proposals_status_check
                                CHECK (status IN ('open', 'rebound')),
    -- The proposed successor anchors in the target release, by SCF code.
    successor_scf_ids           TEXT[] NOT NULL DEFAULT '{}',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One proposal per control per target release; a re-run of the same
    -- import leaves the existing row alone.
    CONSTRAINT scf_remap_proposals_control_target_uniq
        UNIQUE (control_id, target_framework_version_id),
    CONSTRAINT scf_remap_proposals_successors_match_class CHECK (
        CASE class
            WHEN 'no_successor' THEN cardinality(successor_scf_ids) = 0
            WHEN 'split'        THEN cardinality(successor_scf_ids) > 1
            WHEN 'reused_id'    THEN true
            ELSE cardinality(successor_scf_ids) = 1
        END)
);

CREATE INDEX IF NOT EXISTS idx_scf_remap_proposals_tenant_class_status
    ON scf_remap_proposals (tenant_id, class, status);

ALTER TABLE scf_remap_proposals ENABLE ROW LEVEL SECURITY;
ALTER TABLE scf_remap_proposals FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON scf_remap_proposals
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT ON scf_remap_proposals TO atlas_app;
