-- security-atlas — #2236: a durable record of each SCF re-bind and flag.
--
-- scf_remap_proposals (#1775) already holds one row per control per target
-- release, written in the re-bind's transaction, with the legacy anchor and
-- both releases. Two things were missing to answer "why did this control's
-- anchor change, and when" later:
--
--   target_anchor_id  the anchor the control moved to. Set on a 'rebound'
--                     row, NULL on an 'open' (flagged) row, which moved
--                     nothing.
--   source            which path wrote the row: 'boot' (RebindControls,
--                     during `atlas-cli catalog import-scf`) or 'upload'
--                     (control.Store.Upload re-binding an unchanged
--                     manifest in place).
--
-- The upload path runs as the app role, so atlas_app may now insert its own
-- tenant's rows, and update them for the open-to-rebound upsert.

ALTER TABLE scf_remap_proposals
    ADD COLUMN IF NOT EXISTS target_anchor_id UUID REFERENCES scf_anchors(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'boot';

-- Every row before this migration came from the boot path, so the default
-- backfills them; later writers name their path.
ALTER TABLE scf_remap_proposals ALTER COLUMN source DROP DEFAULT;

-- A rebound control sits on the anchor it was moved to unless someone has
-- re-anchored it since; that is the best record left of a pre-#2236 move.
UPDATE scf_remap_proposals p
SET target_anchor_id = c.scf_anchor_id
FROM controls c
WHERE c.id = p.control_id
  AND p.status = 'rebound'
  AND p.target_anchor_id IS NULL;

DO $$
BEGIN
    ALTER TABLE scf_remap_proposals
        ADD CONSTRAINT scf_remap_proposals_source_check
        CHECK (source IN ('boot', 'upload'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- NOT VALID: a pre-#2236 rebound row whose control has since lost its anchor
-- cannot be backfilled; every new row must hold.
DO $$
BEGIN
    ALTER TABLE scf_remap_proposals
        ADD CONSTRAINT scf_remap_proposals_target_matches_status
        CHECK ((status = 'rebound') = (target_anchor_id IS NOT NULL)) NOT VALID;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_insert ON scf_remap_proposals
        FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_update ON scf_remap_proposals
        FOR UPDATE USING (current_tenant_matches(tenant_id))
        WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT INSERT, UPDATE ON scf_remap_proposals TO atlas_app;
