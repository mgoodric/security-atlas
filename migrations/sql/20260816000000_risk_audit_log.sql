-- security-atlas -- #1749: an append-only audit trail for moving a risk
-- between org units and hierarchy levels.
--
-- `risks.level` and `risks.org_unit_id` drive every hierarchy view (the
-- org tree, the theme x org_unit heatmap, the aggregation engine's team
-- count). PATCH /v1/risks/{id} changes them, and each change writes one
-- row here in the same transaction, naming the actor and the before/after
-- placement. Shape follows action_plan_audit_log: RLS with SELECT + INSERT
-- policies only, plus a trigger that denies UPDATE and DELETE even for a
-- role that bypasses RLS.
--
-- No FK to risks: the trail of where a risk sat outlives the risk itself.

CREATE TABLE risk_audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    risk_id      UUID NOT NULL,
    -- The credential that made the change; never anonymous.
    actor        TEXT NOT NULL,
    action_type  TEXT NOT NULL
                 CHECK (action_type IN ('placement_changed')),
    before_state JSONB NOT NULL,
    after_state  JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Slice 180 convention for the audit-log family.
    subject_module TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT risk_audit_log_actor_nonempty CHECK (length(actor) > 0)
);

CREATE INDEX risk_audit_log_tenant_risk
    ON risk_audit_log (tenant_id, risk_id, created_at DESC);

ALTER TABLE risk_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_audit_log FORCE ROW LEVEL SECURITY;

-- Append-only by RLS construction: SELECT + INSERT policies only.
CREATE POLICY tenant_read ON risk_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON risk_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON risk_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION risk_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'risk_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER risk_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON risk_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION risk_audit_log_append_only();
