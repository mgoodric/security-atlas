-- Framework scopes: the predicate of an activated or superseded revision is
-- frozen (#1965).
--
-- Activated and superseded revisions are immutable (D3 on #1943). The
-- decisions on such a revision are already frozen by
-- framework_scope_applicability_guard; this freezes the predicate the same
-- way. GetFrameworkScopeAsOf returns the revision active at a point in time,
-- so an in-place edit would make an as_of read report today's predicate as
-- the one in force then. To change it, create a new revision
-- (POST /v1/framework-scopes with from_scope_id) and take it through review.
--
-- Only predicate and predicate_hash are frozen. The activated -> superseded
-- transition (SupersedePreviousActivated) still updates state,
-- superseded_by, superseded_at and updated_at on the frozen row.
--
-- internal/frameworkscope.Store.UpdatePredicate checks the same condition
-- first to return ErrPredicateFrozen (HTTP 409); this trigger is the
-- guarantee no caller can bypass, including a write racing an activation.

CREATE OR REPLACE FUNCTION framework_scopes_predicate_frozen()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.state IN ('activated', 'superseded')
       AND (NEW.predicate IS DISTINCT FROM OLD.predicate
            OR NEW.predicate_hash IS DISTINCT FROM OLD.predicate_hash) THEN
        RAISE EXCEPTION 'framework_scopes: the predicate of a % framework scope is frozen; create a new revision', OLD.state
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
