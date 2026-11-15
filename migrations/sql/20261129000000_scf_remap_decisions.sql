-- security-atlas — #1776: reviewers decide SCF remap proposals.
--
-- A reviewer resolves an open proposal (#1775) in one of three ways:
--
--   approved     the control is re-anchored on a successor in the target
--                release, as a superseding control revision; final
--   rejected     the control stays on its legacy anchor; the proposal can
--                be revisited (approved or kept legacy later)
--   kept_legacy  the control stays on its legacy anchor; final
--
-- The proposal's status carries the current outcome. Every decision also
-- appends one row to scf_remap_decisions, the anchor history: old release
-- and anchor, new release and anchor, the new control revision, who decided
-- and when. That table is append-only for the app role.
--
-- #2236's composite (tenant_id, control_id) FK and its tenant_insert /
-- tenant_update policies are kept as they are; only the status CHECK and
-- the target/status CHECK widen to admit the new statuses.

ALTER TABLE scf_remap_proposals
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_status_check;
ALTER TABLE scf_remap_proposals
    ADD CONSTRAINT scf_remap_proposals_status_check
    CHECK (status IN ('open', 'rebound', 'approved', 'rejected', 'kept_legacy'));

-- An approved proposal names the anchor the control was re-anchored to,
-- like a rebound one. NOT VALID as in #2236: a pre-#2236 rebound row whose
-- control lost its anchor could not be backfilled.
ALTER TABLE scf_remap_proposals
    DROP CONSTRAINT IF EXISTS scf_remap_proposals_target_matches_status;
ALTER TABLE scf_remap_proposals
    ADD CONSTRAINT scf_remap_proposals_target_matches_status
    CHECK ((status IN ('rebound', 'approved')) = (target_anchor_id IS NOT NULL)) NOT VALID;

-- The decision history references a proposal within its own tenant.
DO $$
BEGIN
    ALTER TABLE scf_remap_proposals
        ADD CONSTRAINT scf_remap_proposals_tenant_id_uniq UNIQUE (tenant_id, id);
EXCEPTION WHEN duplicate_object OR duplicate_table THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS scf_remap_decisions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL,
    proposal_id       UUID NOT NULL,
    decision          TEXT NOT NULL
                      CONSTRAINT scf_remap_decisions_decision_check
                      CHECK (decision IN ('approved', 'rejected', 'kept_legacy')),
    -- True when the decision came from bulk-approve of unchanged proposals.
    bulk              BOOLEAN NOT NULL DEFAULT false,
    -- The control version on the legacy anchor when the decision was made.
    legacy_control_id UUID NOT NULL,
    legacy_anchor_id  UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    legacy_scf_id     TEXT NOT NULL,
    legacy_release    TEXT NOT NULL,
    -- Set on an approval only: the superseding control revision and the
    -- anchor it is bound to.
    new_control_id    UUID,
    new_anchor_id     UUID REFERENCES scf_anchors(id) ON DELETE CASCADE,
    new_scf_id        TEXT,
    new_release       TEXT,
    reason            TEXT CHECK (reason IS NULL OR char_length(reason) <= 2000),
    -- PrincipalOf the deciding person (a users(id) uuid).
    decided_by        TEXT NOT NULL CHECK (decided_by <> ''),
    decided_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT scf_remap_decisions_proposal_fk
        FOREIGN KEY (tenant_id, proposal_id)
        REFERENCES scf_remap_proposals (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scf_remap_decisions_legacy_control_fk
        FOREIGN KEY (tenant_id, legacy_control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scf_remap_decisions_new_control_fk
        FOREIGN KEY (tenant_id, new_control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scf_remap_decisions_target_matches_decision CHECK (
        (decision = 'approved') = (new_control_id IS NOT NULL)
        AND (decision = 'approved') = (new_anchor_id IS NOT NULL)
        AND (decision = 'approved') = (new_scf_id IS NOT NULL)
        AND (decision = 'approved') = (new_release IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_scf_remap_decisions_tenant_proposal
    ON scf_remap_decisions (tenant_id, proposal_id, decided_at);

ALTER TABLE scf_remap_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE scf_remap_decisions FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON scf_remap_decisions
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_insert ON scf_remap_decisions
        FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Append-only: no UPDATE or DELETE for the app role.
GRANT SELECT, INSERT ON scf_remap_decisions TO atlas_app;
