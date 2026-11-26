-- security-atlas -- #2479: who triggered a manual evaluation.
--
-- control_evaluations records trigger = 'manual' for POST
-- /v1/controls/{id}/evaluate (#2110) but not who made the call. This adds
-- `triggered_by`: the caller's principal (credstore.PrincipalOf -- the bare
-- users(id) uuid for a person, else the key or client id), stamped on every
-- row of a manual run.
--
-- It sits on the ledger row, not in a separate log, because the row is the
-- record of the run: history and as-of reads carry who ran it with no join.
--
-- The CHECK keeps the column to manual runs only. ingest, scheduled and
-- replay runs are started by the platform and stay NULL. A manual row may
-- still be NULL: the demo seeder (internal/demoseed) labels its rows manual
-- and has no caller, and every manual row written before this migration has
-- none either.
--
-- Additive and nullable: no backfill, no rewrite of existing rows, and the
-- table stays append-only (no UPDATE/DELETE policy is added).

ALTER TABLE control_evaluations
    ADD COLUMN IF NOT EXISTS triggered_by TEXT NULL;

ALTER TABLE control_evaluations
    ADD CONSTRAINT control_evaluations_triggered_by_manual_only
        CHECK (triggered_by IS NULL OR trigger = 'manual');

ALTER TABLE control_evaluations
    ADD CONSTRAINT control_evaluations_triggered_by_nonempty
        CHECK (triggered_by IS NULL OR length(triggered_by) BETWEEN 1 AND 256);
