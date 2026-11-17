-- security-atlas -- #2329: which credential made a me_audit_log change.
--
-- me_audit_log.user_id is a users(id) uuid. A change made with a key that is
-- not a user's (the bootstrap admin, an unbound API key, an OAuth client)
-- is written as uuid.Nil, and the row did not say which credential acted.
-- This adds `actor_principal`: the caller's principal
-- (credstore.PrincipalOf -- the bare users(id) uuid for a person, else the
-- key or client id), the same value control_evaluations.triggered_by holds
-- (#2479). It is an id, never a token or any secret material.
--
-- A column, not a field in the after value, because the after value's shape
-- is per action and an auditor's query for "what did this credential change"
-- should not have to know every shape.
--
-- Additive and nullable: rows written before this migration, and writers
-- that have no caller (the demo seeder, the first-install bootstrap), stay
-- NULL. The table stays append-only (no UPDATE/DELETE policy is added).

ALTER TABLE me_audit_log
    ADD COLUMN IF NOT EXISTS actor_principal TEXT NULL;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_actor_principal_nonempty
        CHECK (actor_principal IS NULL OR length(actor_principal) BETWEEN 1 AND 256);
