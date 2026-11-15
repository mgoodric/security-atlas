-- Framework scopes: an activated or superseded revision is frozen (#1965).
--
-- Activated and superseded revisions are immutable (D3 on #1943). The
-- decisions on such a revision are already frozen by
-- framework_scope_applicability_guard; this freezes the row itself.
-- GetFrameworkScopeAsOf returns the revision active at a point in time, so
-- an in-place edit would make an as_of read report today's predicate (or
-- effective_from, or approval) as the one in force then. To change a frozen
-- revision, create a new one (POST /v1/framework-scopes with from_scope_id)
-- and take it through review.
--
-- On an activated or superseded row the trigger is an allow-list. It
-- refuses a change to any column except:
--
--   * name, a label that plays no part in as_of reads;
--   * superseded_by, superseded_at and updated_at, which the supersede path
--     (SupersedePreviousActivated) writes;
--   * state, and only activated -> superseded. A step back to draft would
--     otherwise reopen the predicate and, through the applicability guard,
--     the decisions.
--
-- The compare is to_jsonb(NEW) against to_jsonb(OLD) with the allowed
-- columns removed, so a column added to framework_scopes later is frozen by
-- default. That covers the predicate, effective_from, framework_version_id,
-- tenant_id, the approval provenance (approver_user_id, approved_at,
-- predicate_hash_at_approval, approval_evidence_file_url/_hash) and #2101's
-- submitted_by and self_approved.
--
-- The bounce (framework_scopes_bounce_on_predicate_change) and reset
-- (framework_scope_applicability_reset_approval) triggers act only on
-- review and approved rows, so they never write to a frozen row.
--
-- DELETE is not guarded, like the decision guard (#1787 D4): no API deletes
-- a scope, and the FK cascades and the operator purge must still work.
--
-- internal/frameworkscope.Store.UpdatePredicate checks the predicate case
-- first to return ErrPredicateFrozen (HTTP 409); this trigger is the
-- guarantee no caller can bypass, including a write racing an activation.

CREATE OR REPLACE FUNCTION framework_scopes_predicate_frozen()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    allowed CONSTANT TEXT[] := ARRAY['name', 'superseded_by', 'superseded_at', 'updated_at', 'state'];
    new_row JSONB;
    old_row JSONB;
    changed TEXT;
BEGIN
    IF OLD.state NOT IN ('activated', 'superseded') THEN
        RETURN NEW;
    END IF;
    IF NEW.state IS DISTINCT FROM OLD.state
       AND NOT (OLD.state = 'activated' AND NEW.state = 'superseded') THEN
        RAISE EXCEPTION 'framework_scopes: a % framework scope cannot move to %; create a new revision', OLD.state, NEW.state
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'framework_scopes_predicate_frozen';
    END IF;
    new_row := to_jsonb(NEW) - allowed;
    old_row := to_jsonb(OLD) - allowed;
    IF new_row IS DISTINCT FROM old_row THEN
        SELECT string_agg(n.key, ', ' ORDER BY n.key)
          INTO changed
          FROM jsonb_each(new_row) n
         WHERE n.value IS DISTINCT FROM old_row -> n.key;
        RAISE EXCEPTION 'framework_scopes: a % framework scope is frozen (changed: %); create a new revision', OLD.state, changed
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'framework_scopes_predicate_frozen';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER framework_scopes_predicate_frozen_trg
    BEFORE UPDATE ON framework_scopes
    FOR EACH ROW
    EXECUTE FUNCTION framework_scopes_predicate_frozen();
