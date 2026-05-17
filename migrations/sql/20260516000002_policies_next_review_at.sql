-- security-atlas — slice 094: add policies.next_review_at column.
--
-- The compliance calendar (slice 094) surfaces upcoming policy review
-- deadlines as one of four event types. The slice-022 policies table did
-- not carry a "next review date" column; this migration adds one so the
-- calendar can read it.
--
-- The column is nullable with no default — existing policies start with
-- NULL ("no review scheduled"), which the calendar handler treats as
-- "omit this policy from the feed." Operators populate the value via a
-- future policy-admin PATCH (out of scope for slice 094 per its
-- anti-criterion P0-A4: no changes to existing write paths in this
-- slice). Until that PATCH lands, the column can be populated directly
-- by self-host operators via SQL — the calendar handler will pick up the
-- value on its next read.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the database layer. The existing four-policy
--       RLS split (tenant_read / tenant_write / tenant_update /
--       tenant_delete) on the policies table inherits the new column
--       automatically — no policy change required.
--
-- Anti-criteria honored (slice 094 P0-A4):
--   - No change to the existing write path on policies. The new column
--     is nullable and the slice-022 INSERT statements continue to work
--     unchanged (the column gets NULL by default).
--   - No backfill. Existing rows get NULL. Operators set the value
--     deliberately.
--
-- See docs/audit-log/094-compliance-calendar-decisions.md decision D4
-- for the full rationale and the migration's relationship to the
-- slice-022 policy lifecycle.
--
-- Reversible via 20260516000002_policies_next_review_at.down.sql which
-- drops the column.

ALTER TABLE policies
    ADD COLUMN IF NOT EXISTS next_review_at TIMESTAMPTZ NULL;

-- Index supports the calendar handler's `WHERE next_review_at IS NOT NULL
-- AND next_review_at BETWEEN $from AND $to` filter. Partial index on the
-- non-null subset keeps the index small for tenants who have not yet
-- populated review dates on most policies.
CREATE INDEX IF NOT EXISTS idx_policies_tenant_next_review
    ON policies (tenant_id, next_review_at)
    WHERE next_review_at IS NOT NULL;
