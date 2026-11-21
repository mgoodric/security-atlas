-- security-atlas — #2847: review flags on staged risk import rows (spec
-- #2812).
--
-- A staged row carries the reasons an approver should look at it before
-- deciding, from a fixed vocabulary:
--
--   owner_unlinked        the owner matched no user and was kept as the
--                         unlinked free-text owner (#2843);
--   treatment_downgraded  a mitigate or transfer row arrived as untreated
--                         (#2842);
--   parent_link_failed    the parent-risk-ID link broke a hierarchy rule
--                         (#2846).
--
-- The column lands empty: those tickets set it. The review page renders
-- and filters on it.

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS flags TEXT[] NOT NULL DEFAULT '{}'::TEXT[];

DO $$
BEGIN
    ALTER TABLE risk_import_rows
        ADD CONSTRAINT risk_import_rows_flags_chk
        CHECK (flags <@ ARRAY['owner_unlinked', 'treatment_downgraded', 'parent_link_failed']::TEXT[]);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
