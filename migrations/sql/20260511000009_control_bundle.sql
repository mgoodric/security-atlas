-- security-atlas — Control bundle format + version-stamped controls (slice 009).
--
-- Implements docs/issues/009-control-bundle-format.md AC-1..AC-6.
--
-- ----------------------------------------------------------------------------
-- Design choice: version-stamp `controls` in place (the "simpler design"
-- suggested in the slice spec) rather than introduce a side `control_versions`
-- table. Rationale:
--
--   1. Slice 002 already gave us `version INT`. We add `bundle_id TEXT`
--      (the natural key declared in the bundle's YAML manifest) and
--      `superseded_by UUID NULL` for the supersession chain.
--   2. A partial unique index keyed on `(tenant_id, bundle_id)
--      WHERE superseded_by IS NULL` enforces "one active version per
--      bundle_id per tenant" without touching foreign keys (the slice-002
--      composite FK on evidence_records still works — both active and
--      superseded rows live in the same table).
--   3. Re-uploading the same `bundle_id` flips the prior row's
--      `superseded_by` to the new row's id in the same transaction
--      (application-layer atomicity). The partial unique index
--      enforces the invariant at the DB level.
--   4. The full bundle manifest (post-canonicalization) is stored verbatim
--      on each version as `bundle_manifest_yaml` + `bundle_manifest_hash`
--      (sha256 of bytes). Auditors get byte-exact reproducibility from the
--      DB without round-tripping to object storage.
--
-- This is *additive*: every existing slice-002 `controls` row is backfilled
-- with `bundle_id = 'legacy_' || id::text`, `version = 1`, `superseded_by =
-- NULL`, and an empty `bundle_manifest_yaml`. No DML on existing rows is
-- destructive.
-- ----------------------------------------------------------------------------

-- ===== 1. Augment the controls table =====

ALTER TABLE controls
    ADD COLUMN bundle_id              TEXT        NULL,
    ADD COLUMN superseded_by          UUID        NULL,
    ADD COLUMN bundle_manifest_yaml   TEXT        NOT NULL DEFAULT '',
    ADD COLUMN bundle_manifest_hash   TEXT        NOT NULL DEFAULT '',
    ADD COLUMN scf_anchor_id          UUID        NULL,
    ADD COLUMN evidence_queries       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN manual_evidence_schema JSONB       NULL,
    ADD COLUMN linked_policy_ids      TEXT[]      NOT NULL DEFAULT ARRAY[]::TEXT[],
    ADD COLUMN freshness_class        TEXT        NULL,
    ADD COLUMN bundle_uploaded_at     TIMESTAMPTZ NULL,
    ADD COLUMN bundle_uploaded_by     TEXT        NULL;

-- ===== 2. Backfill existing slice-002 rows =====
--
-- Production has no slice-002-only controls rows yet (no prior slice writes),
-- but defensive backfill keeps tests + future deployments honest. Each existing
-- row gets a synthetic bundle_id derived from its uuid so the partial unique
-- index can be created without conflict.

UPDATE controls
SET bundle_id = 'legacy_' || id::text
WHERE bundle_id IS NULL;

-- Now make bundle_id mandatory for every row.
ALTER TABLE controls
    ALTER COLUMN bundle_id SET NOT NULL;

-- ===== 3. Self-FK + SCF anchor FK =====
--
-- superseded_by points at another controls row in the same tenant. ON DELETE
-- SET NULL so removing a successor doesn't cascade-delete its predecessor.
ALTER TABLE controls
    ADD CONSTRAINT controls_superseded_by_fk
        FOREIGN KEY (superseded_by) REFERENCES controls (id) ON DELETE SET NULL;

-- scf_anchor_id references the global scf_anchors catalog (no tenant_id —
-- platform-bundled). ON DELETE RESTRICT so an anchor row in active use can't
-- silently disappear from under a control.
ALTER TABLE controls
    ADD CONSTRAINT controls_scf_anchor_fk
        FOREIGN KEY (scf_anchor_id) REFERENCES scf_anchors (id) ON DELETE RESTRICT;

-- ===== 4. Partial unique index — one active version per bundle_id per tenant =====
--
-- AC-6: Re-uploading the same bundle id creates a new control row and
-- supersedes the prior. The DB invariant: at most one (tenant_id, bundle_id)
-- row may have `superseded_by IS NULL` at any time. The application's
-- supersession transaction (set predecessor.superseded_by THEN insert
-- successor) must be atomic; the unique index enforces it regardless.

CREATE UNIQUE INDEX controls_one_active_version_per_bundle
    ON controls (tenant_id, bundle_id)
    WHERE superseded_by IS NULL;

-- Index for the common reverse lookup: "show me every version of this bundle".
CREATE INDEX idx_controls_tenant_bundle_id
    ON controls (tenant_id, bundle_id, version DESC);

-- Index on scf_anchor_id for SCF -> control lookups (UCF graph traversal in
-- slice 008). Partial: anchored controls only — until a control is anchored
-- it's not part of the canonical graph.
CREATE INDEX idx_controls_scf_anchor
    ON controls (scf_anchor_id)
    WHERE scf_anchor_id IS NOT NULL;

-- ===== 5. Tighten RLS — four-policy split =====
--
-- Slice 002 left `tenant_isolation USING (current_tenant_matches(...))`.
-- Slice 009 ships writes; we tighten to the slice-014/017/018/036 pattern with
-- explicit WITH CHECK on INSERT and USING + WITH CHECK on UPDATE. Slice-002's
-- `controls` rows have no production traffic yet, so the policy switch is safe.

DROP POLICY IF EXISTS tenant_isolation ON controls;

CREATE POLICY tenant_read ON controls
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON controls
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON controls
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON controls
    FOR DELETE
    USING (current_tenant_matches(tenant_id));
