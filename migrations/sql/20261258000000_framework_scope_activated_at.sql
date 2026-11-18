-- Framework scopes: record when a revision was activated (#2078).
--
-- effective_from is chosen by the operator and may be earlier than the
-- activation. The frozen-period SSP read
-- (ListExcludedRequirementsAsOfForExport) used to select by effective_from
-- alone, so a revision activated after an audit period froze, with
-- effective_from before the freeze, changed what a re-export of that
-- frozen period reported. It now also requires activated_at <= frozen_at:
-- a frozen period sees only revisions that existed at its freeze.
--
-- Activate also refuses an effective_from at or before the latest frozen
-- audit period's frozen_at for the framework version (HTTP 409,
-- frameworkscope.ErrEffectiveBeforeFreeze), under an advisory lock that
-- Freeze also takes. activated_at covers a freeze whose frozen_at was taken
-- before an activation that committed first. General as-of reads
-- (GetFrameworkScopeAsOf) keep selecting by effective_from, so backdating
-- still works for planning where no freeze is crossed.
--
-- activated_at is set by framework_scopes_stamp_activated_at, to
-- clock_timestamp() (the write, after Activate's lock, not the transaction
-- start), on the move into `activated`, and on any direct INSERT of an
-- activated or superseded row, as seeds and tests do. The CHECK makes it NOT NULL for every
-- revision that has been activated; draft, review and approved rows hold
-- NULL.
--
-- Backfill of existing activated and superseded rows, in order of
-- preference (D-log docs/audit-log/2078-framework-scope-activated-at-decisions.md):
--   1. the latest `activate` entry in framework_scope_workflow_log, the
--      recorded activation time (#1786 onwards);
--   2. LEAST(effective_from, updated_at) for rows older than the log. Never
--      later than the real activation (updated_at is at or after it) and
--      never later than effective_from, so the as-of result of every such
--      row is exactly what it was before this migration.
-- The UPDATE only touches rows whose activated_at is NULL, so re-running it
-- is a no-op. framework_scopes_predicate_frozen refuses any column change on
-- an activated or superseded row, so it is disabled for the backfill only.

ALTER TABLE framework_scopes ADD COLUMN IF NOT EXISTS activated_at TIMESTAMPTZ NULL;

ALTER TABLE framework_scopes DISABLE TRIGGER framework_scopes_predicate_frozen_trg;

UPDATE framework_scopes fs
SET activated_at = COALESCE(
        (SELECT max(l.occurred_at)
           FROM framework_scope_workflow_log l
          WHERE l.tenant_id = fs.tenant_id
            AND l.framework_scope_id = fs.id
            AND l.action = 'activate'),
        LEAST(fs.effective_from, fs.updated_at))
WHERE fs.state IN ('activated', 'superseded')
  AND fs.activated_at IS NULL;

ALTER TABLE framework_scopes ENABLE TRIGGER framework_scopes_predicate_frozen_trg;

CREATE OR REPLACE FUNCTION framework_scopes_stamp_activated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.state IN ('activated', 'superseded') AND NEW.activated_at IS NULL THEN
        NEW.activated_at := clock_timestamp();
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS framework_scopes_stamp_activated_at_trg ON framework_scopes;
CREATE TRIGGER framework_scopes_stamp_activated_at_trg
    BEFORE INSERT OR UPDATE OF state ON framework_scopes
    FOR EACH ROW
    EXECUTE FUNCTION framework_scopes_stamp_activated_at();

ALTER TABLE framework_scopes DROP CONSTRAINT IF EXISTS framework_scopes_activated_at_set;
ALTER TABLE framework_scopes ADD CONSTRAINT framework_scopes_activated_at_set
    CHECK (state NOT IN ('activated', 'superseded') OR activated_at IS NOT NULL);
