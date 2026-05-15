-- Slice 068 — make controls_superseded_by_fk DEFERRABLE INITIALLY DEFERRED.
--
-- Why
-- ---
-- Slice 009 (`20260511000009_control_bundle.sql`) shipped two things that,
-- together, made control-bundle RE-upload impossible:
--
--   1. a partial unique index `controls_one_active_version_per_bundle`
--      ON controls (tenant_id, bundle_id) WHERE superseded_by IS NULL —
--      "at most one active version per bundle per tenant"; and
--   2. a self-FK `controls_superseded_by_fk` (superseded_by -> controls.id)
--      created NON-deferrable (the default).
--
-- The slice-009 SQL even documents the only ordering that satisfies the
-- unique index (`internal/db/queries/controls.sql`: "Re-upload supersedes
-- by FIRST UPDATE-ing the prior row's superseded_by, THEN INSERTing the
-- new row"): you must flip the predecessor to superseded BEFORE the new
-- active row exists, otherwise there are momentarily TWO rows with
-- superseded_by IS NULL for the same (tenant_id, bundle_id) and the
-- INSERT trips the unique index with SQLSTATE 23505.
--
-- But that prescribed order is impossible while the self-FK is
-- non-deferrable: the UPDATE sets `prior.superseded_by = <new row id>`,
-- and the new row does not exist yet — a non-deferrable FK rejects the
-- UPDATE immediately. So slice 009's store code did insert-then-update
-- instead, which is the order the unique index rejects. Net effect:
-- the FIRST control-bundle upload works, every RE-upload 500s. The
-- self-host bundle's idempotency check (re-running atlas-bootstrap, which
-- re-uploads all 50 SOC 2 bundles) is what surfaced it.
--
-- The fix is the standard one for an intra-transaction
-- update-then-insert-the-target cycle: make the FK DEFERRABLE INITIALLY
-- DEFERRED so it is validated at COMMIT (by which point the new row
-- exists) rather than per-statement. This is exactly the pattern slice
-- 002 already uses for `frameworks_latest_version_fk`
-- (`20260511000000_init.sql`) — another "row points at a sibling created
-- in the same transaction" relationship. The companion store-code change
-- reorders Upload() to the slice-009-documented order (mark predecessor
-- superseded, then insert the successor).
--
-- ON DELETE SET NULL is preserved unchanged — only the deferrability of
-- the constraint check changes.
--
-- Round-trip safe: the down migration restores the original
-- non-deferrable constraint. No enum types or sibling objects.

ALTER TABLE controls
    DROP CONSTRAINT controls_superseded_by_fk;

ALTER TABLE controls
    ADD CONSTRAINT controls_superseded_by_fk
        FOREIGN KEY (superseded_by) REFERENCES controls (id) ON DELETE SET NULL
        DEFERRABLE INITIALLY DEFERRED;
