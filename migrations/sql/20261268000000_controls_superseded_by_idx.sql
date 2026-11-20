-- security-atlas -- #2599: index the controls lineage walk.
--
-- ApprovedRemapLineageRevision (#1776, #2358) walks a control's revisions
-- back through superseded_by on every boot-seed upload of a stock manifest:
-- each step finds the revision whose superseded_by is the current one,
-- within the tenant. No index covered (tenant_id, superseded_by), so each
-- step scanned the tenant's controls.

CREATE INDEX IF NOT EXISTS idx_controls_tenant_superseded_by
    ON controls (tenant_id, superseded_by);
