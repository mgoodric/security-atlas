-- Down migration for 20260516000002_policies_next_review_at.sql.
--
-- Drops the partial index first, then the column. The slice-022 policy
-- write paths continue to work after the drop because they never set the
-- column (it was added with no default and was always nullable).

DROP INDEX IF EXISTS idx_policies_tenant_next_review;

ALTER TABLE policies
    DROP COLUMN IF EXISTS next_review_at;
