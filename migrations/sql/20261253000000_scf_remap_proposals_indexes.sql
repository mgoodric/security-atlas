-- security-atlas — #2325: indexes for the SCF remap queue.
--
-- legacy_anchor_id and target_framework_version_id cascade from
-- scf_anchors and framework_versions; without an index, deleting an anchor
-- or a release scans scf_remap_proposals. The third index serves the keyset
-- order of GET /v1/controls/remap-proposals (created_at DESC, bundle_id,
-- id) within a tenant.

CREATE INDEX IF NOT EXISTS idx_scf_remap_proposals_legacy_anchor
    ON scf_remap_proposals (legacy_anchor_id);

CREATE INDEX IF NOT EXISTS idx_scf_remap_proposals_target_version
    ON scf_remap_proposals (target_framework_version_id);

CREATE INDEX IF NOT EXISTS idx_scf_remap_proposals_tenant_page
    ON scf_remap_proposals (tenant_id, created_at DESC, bundle_id, id);
