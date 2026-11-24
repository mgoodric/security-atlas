-- security-atlas -- #3050: the original freeze is immutable in the database.
--
-- A frozen audit period's original freeze (revision 1) is the tuple
-- (frozen_at, frozen_hash, frozen_by) on its audit_periods row. Since #1742
-- (PR #3044) a frozen population changes only through an appended row in
-- audit_period_freeze_revisions, and every revision must replay against
-- that original. Until now the tuple was immutable only in application
-- code: the Freeze query's `WHERE status = 'open'` guard. atlas_app holds
-- UPDATE on audit_periods, so any other UPDATE could rewrite it.
--
--   audit_periods_freeze_immutable   BEFORE UPDATE OF frozen_at,
--                                    frozen_hash, frozen_by. Once a column
--                                    is set it never changes again: no
--                                    rewrite and no reset to NULL. The one
--                                    allowed transition is NULL to a value,
--                                    which is what Freeze does. Since
--                                    audit_periods_frozen_coherent ties
--                                    status = 'open' to a NULL tuple, a
--                                    frozen period can no longer be
--                                    reopened either. INSERT is not guarded:
--                                    the demo seed and fixtures insert
--                                    already-frozen rows directly.
--   audit_period_freeze_revisions_chain
--                                    BEFORE INSERT. Revision N (N >= 2) is
--                                    accepted only on a frozen period whose
--                                    latest revision is N - 1 (revision 1 is
--                                    the audit_periods row), and only when
--                                    its previous_hash equals that
--                                    revision's frozen_hash. The UNIQUE
--                                    (tenant_id, audit_period_id, revision)
--                                    constraint already refuses a second
--                                    revision N from a concurrent writer;
--                                    this closes gaps and broken links.
--
-- A privilege split (column-level UPDATE grants) was the alternative. It
-- cannot express "NULL to value only": Freeze, which runs as atlas_app,
-- has to write the same columns. The trigger applies to every role,
-- including the owner; a deliberate recovery has to disable it in its own
-- transaction, which the owner alone can do.
--
-- Neither function is SECURITY DEFINER. The chain check reads the period
-- and revision rows as the inserting role, under its RLS policies, which is
-- the tenant the inserted row must belong to anyway.
--
-- Constitutional invariants:
--   #2  nothing here writes a row; both triggers only refuse.
--   #10 the original freeze replays: its horizon and commitment cannot be
--       rewritten, and every later revision links back to it.
--
-- Reversible via 20261307000000_audit_period_freeze_immutable.down.sql.

-- ===== 1. audit_periods: the original freeze tuple is write-once =====

CREATE OR REPLACE FUNCTION audit_periods_freeze_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (OLD.frozen_at IS NOT NULL AND NEW.frozen_at IS DISTINCT FROM OLD.frozen_at)
       OR (OLD.frozen_hash IS NOT NULL AND NEW.frozen_hash IS DISTINCT FROM OLD.frozen_hash)
       OR (OLD.frozen_by IS NOT NULL AND NEW.frozen_by IS DISTINCT FROM OLD.frozen_by) THEN
        RAISE EXCEPTION 'audit period % is frozen: frozen_at, frozen_hash and frozen_by cannot change once set', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS audit_periods_freeze_immutable_trg ON audit_periods;
CREATE TRIGGER audit_periods_freeze_immutable_trg
    BEFORE UPDATE OF frozen_at, frozen_hash, frozen_by ON audit_periods
    FOR EACH ROW
    EXECUTE FUNCTION audit_periods_freeze_immutable();

-- ===== 2. audit_period_freeze_revisions: an unbroken hash chain =====

CREATE OR REPLACE FUNCTION audit_period_freeze_revisions_chain()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    latest_revision INTEGER;
    latest_hash     BYTEA;
BEGIN
    SELECT r.revision, r.frozen_hash
      INTO latest_revision, latest_hash
      FROM audit_period_freeze_revisions r
     WHERE r.tenant_id = NEW.tenant_id
       AND r.audit_period_id = NEW.audit_period_id
     ORDER BY r.revision DESC
     LIMIT 1;

    IF NOT FOUND THEN
        SELECT 1, ap.frozen_hash
          INTO latest_revision, latest_hash
          FROM audit_periods ap
         WHERE ap.tenant_id = NEW.tenant_id
           AND ap.id = NEW.audit_period_id
           AND ap.status = 'frozen';
        IF NOT FOUND THEN
            RAISE EXCEPTION 'audit period % is not frozen: it has no freeze to revise', NEW.audit_period_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.revision <> latest_revision + 1 THEN
        RAISE EXCEPTION 'freeze revision % of audit period % does not follow its latest revision %',
            NEW.revision, NEW.audit_period_id, latest_revision
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.previous_hash IS DISTINCT FROM latest_hash THEN
        RAISE EXCEPTION 'freeze revision % of audit period %: previous_hash does not match revision %',
            NEW.revision, NEW.audit_period_id, latest_revision
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS audit_period_freeze_revisions_chain_trg ON audit_period_freeze_revisions;
CREATE TRIGGER audit_period_freeze_revisions_chain_trg
    BEFORE INSERT ON audit_period_freeze_revisions
    FOR EACH ROW
    EXECUTE FUNCTION audit_period_freeze_revisions_chain();
