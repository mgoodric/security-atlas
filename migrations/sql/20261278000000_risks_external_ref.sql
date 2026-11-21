-- #2838: a risk carries the organisation's own risk ID ("R-014"), so a
-- security leader finds risks by the IDs their team already uses, and the
-- risk register import (#2812) has a duplicate key and a parent-link key.
--
-- Optional. When set it is trimmed, non-empty, at most 128 characters and
-- unique within the tenant. Uniqueness ignores case: "R-014" and "r-014"
-- are the same ID, while the value is stored and shown as typed. The
-- partial unique index leaves any number of risks without one, and the
-- same ID in two tenants is two rows, not a conflict.

ALTER TABLE risks
    ADD COLUMN external_ref TEXT NULL
    CONSTRAINT risks_external_ref_shape CHECK (
        external_ref IS NULL
        OR (external_ref = btrim(external_ref)
            AND external_ref <> ''
            AND char_length(external_ref) <= 128)
    );

CREATE UNIQUE INDEX risks_tenant_external_ref_key
    ON risks (tenant_id, lower(external_ref))
    WHERE external_ref IS NOT NULL;
