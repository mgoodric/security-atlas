-- security-atlas — #1782: approving a staged control import row creates a
-- real control (bring-your-own controls, #1734).
--
-- A reviewer approves a staged row with one of its candidates or any anchor
-- of the batch's SCF release, or rejects it with a reason. Approval uploads a
-- control bundle through the control store in the same transaction as the
-- row update and the audit row below.
--
--   control_import_rows   gains the decision: who decided and when, the
--                         control approval created, the anchor it bound, and
--                         the AI-assist boundary columns guarded by the
--                         shared ai_assist_human_approver_guard (slice 498).
--                         ai_assisted is true when the approved candidate
--                         was placed by the LLM rerank (#1781).
--   control_import_audit_log
--                         one append-only row per approval, rejection or
--                         duplicate, with the batch, row, actor and anchor.
--                         It appears in the unified audit log as kind
--                         `control_import`.

ALTER TABLE control_import_rows
    ADD COLUMN IF NOT EXISTS decided_by TEXT NULL,
    ADD COLUMN IF NOT EXISTS decided_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS control_id UUID NULL,
    ADD COLUMN IF NOT EXISTS bundle_id TEXT NULL,
    ADD COLUMN IF NOT EXISTS scf_anchor_id UUID NULL,
    ADD COLUMN IF NOT EXISTS scf_id TEXT NULL,
    ADD COLUMN IF NOT EXISTS ai_assisted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS human_approved BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS human_approver TEXT NULL;

DO $$
BEGIN
    ALTER TABLE control_import_rows
        ADD CONSTRAINT control_import_rows_ai_assist_invariant
        CHECK (ai_assist_human_approver_guard(ai_assisted, human_approved, human_approver));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- An approved row names its approver and the control it created; every
-- approval needs a person, AI-assisted or not.
DO $$
BEGIN
    ALTER TABLE control_import_rows
        ADD CONSTRAINT control_import_rows_approved_chk
        CHECK (status <> 'approved' OR (
            human_approved
            AND human_approver IS NOT NULL AND length(human_approver) > 0
            AND control_id IS NOT NULL AND scf_anchor_id IS NOT NULL));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- A rejected row keeps its reason.
DO $$
BEGIN
    ALTER TABLE control_import_rows
        ADD CONSTRAINT control_import_rows_rejected_reason_chk
        CHECK (status <> 'rejected' OR length(coalesce(rejection_reason, '')) > 0);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ===== control_import_audit_log =====
--
-- Shape follows taxonomy_audit_log (#1810): RLS with SELECT + INSERT
-- policies only, and a trigger that denies UPDATE and DELETE even for a
-- role that bypasses RLS. No FK to the batch or row: the trail outlives a
-- deleted batch.
CREATE TABLE IF NOT EXISTS control_import_audit_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    batch_id        UUID NOT NULL,
    row_id          UUID NOT NULL,
    row_number      INTEGER NOT NULL,
    org_control_id  TEXT NOT NULL,
    action          TEXT NOT NULL
                    CONSTRAINT control_import_audit_log_action_check
                    CHECK (action IN ('approve', 'reject', 'duplicate')),
    -- The users(id) who decided; never a machine credential.
    actor           TEXT NOT NULL,
    -- The chosen anchor and its SCF release; set on approve.
    scf_anchor_id   UUID NULL,
    scf_id          TEXT NULL,
    scf_release     TEXT NULL,
    -- The control approval created, or the existing control a duplicate
    -- matched.
    control_id      UUID NULL,
    bundle_id       TEXT NULL,
    reason          TEXT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Slice 180 convention for the audit-log family.
    subject_module  TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT control_import_audit_log_actor_nonempty CHECK (length(actor) > 0),
    CONSTRAINT control_import_audit_log_approve_anchor
        CHECK (action <> 'approve' OR (scf_anchor_id IS NOT NULL AND control_id IS NOT NULL)),
    CONSTRAINT control_import_audit_log_reject_reason
        CHECK (action <> 'reject' OR length(coalesce(reason, '')) > 0)
);

CREATE INDEX IF NOT EXISTS idx_control_import_audit_log_tenant_occurred
    ON control_import_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_control_import_audit_log_batch
    ON control_import_audit_log (tenant_id, batch_id, occurred_at);

ALTER TABLE control_import_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_import_audit_log FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON control_import_audit_log
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$
BEGIN
    CREATE POLICY tenant_write ON control_import_audit_log
        FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT ON control_import_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION control_import_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'control_import_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS control_import_audit_log_append_only_trg ON control_import_audit_log;
CREATE TRIGGER control_import_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON control_import_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION control_import_audit_log_append_only();
