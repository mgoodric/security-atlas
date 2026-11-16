-- security-atlas — #2136: an organisation's posture target per framework
-- version.
--
-- A dashboard posture tile shows a framework version's coverage. Without a
-- target the colour means nothing (#2095), so the tile is neutral. A target
-- is the coverage the organisation aims for, with warning and critical
-- thresholds and the direction that counts as better, the same four fields
-- metric_targets carries. The tile then colours by the shared threshold
-- rule (web/lib/api/metrics.ts thresholdBadgeColor).
--
-- One row per (tenant, framework version). Values are coverage percentages,
-- 0..100. The ordering CHECK mirrors the handler's validation, so a row the
-- handler would refuse cannot be written by any other path either:
--
--   higher_is_better   critical <= warning <= target
--   lower_is_better    target <= warning <= critical
--   target_is_better   |warning - target| <= |critical - target|
--
-- PUT /v1/framework-scopes/posture-targets/{framework_version_id} upserts
-- the row and writes one framework_posture_target_audit_log row (before ->
-- after) in the same transaction. Only admin and grc_engineer may set it;
-- every signed-in role may read it.
--
-- Constitutional invariant #6: tenant isolation by RLS. FORCE RLS, with
-- SELECT / INSERT / UPDATE policies and no DELETE (there is no delete
-- route). framework_version_id references a catalog or tenant version;
-- the handler checks the caller can see it and that the tenant has an
-- activated scope for it.

CREATE TABLE framework_posture_targets (
    tenant_id             UUID          NOT NULL,
    framework_version_id  UUID          NOT NULL
        REFERENCES framework_versions (id) ON DELETE CASCADE,
    target_value          NUMERIC(5,2)  NOT NULL,
    warning_threshold     NUMERIC(5,2)  NOT NULL,
    critical_threshold    NUMERIC(5,2)  NOT NULL,
    direction             TEXT          NOT NULL
        CONSTRAINT framework_posture_targets_direction_check
        CHECK (direction IN ('higher_is_better', 'lower_is_better', 'target_is_better')),
    updated_by            TEXT          NOT NULL,
    created_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, framework_version_id),

    CONSTRAINT framework_posture_targets_range_check CHECK (
        target_value BETWEEN 0 AND 100
        AND warning_threshold BETWEEN 0 AND 100
        AND critical_threshold BETWEEN 0 AND 100
    ),
    CONSTRAINT framework_posture_targets_order_check CHECK (
        (direction = 'higher_is_better'
            AND critical_threshold <= warning_threshold
            AND warning_threshold <= target_value)
        OR (direction = 'lower_is_better'
            AND target_value <= warning_threshold
            AND warning_threshold <= critical_threshold)
        OR (direction = 'target_is_better'
            AND abs(warning_threshold - target_value) <= abs(critical_threshold - target_value))
    ),
    CONSTRAINT framework_posture_targets_updated_by_nonempty CHECK (length(updated_by) > 0)
);

ALTER TABLE framework_posture_targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE framework_posture_targets FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON framework_posture_targets
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON framework_posture_targets
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON framework_posture_targets
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE ON framework_posture_targets TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON framework_posture_targets TO atlas_migrate;

-- ===== the audit trail =====
--
-- Shape follows taxonomy_audit_log (#1810): RLS with SELECT + INSERT
-- policies only, plus a trigger that denies UPDATE and DELETE even for a
-- role that bypasses RLS. The rows appear in the unified audit log as kind
-- `posture_target`. No FK to the target: the trail outlives it.

CREATE TABLE framework_posture_target_audit_log (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID        NOT NULL,
    framework_version_id  UUID        NOT NULL,
    action                TEXT        NOT NULL
        CONSTRAINT framework_posture_target_audit_log_action_check
        CHECK (action IN ('set')),
    -- before is NULL the first time a target is set.
    before                JSONB       NULL,
    after                 JSONB       NOT NULL,
    actor                 TEXT        NOT NULL,
    occurred_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    subject_module        TEXT        NOT NULL DEFAULT 'core',

    CONSTRAINT framework_posture_target_audit_log_actor_nonempty CHECK (length(actor) > 0)
);

CREATE INDEX idx_framework_posture_target_audit_log_tenant_occurred
    ON framework_posture_target_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX idx_framework_posture_target_audit_log_version
    ON framework_posture_target_audit_log (tenant_id, framework_version_id, occurred_at);

ALTER TABLE framework_posture_target_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE framework_posture_target_audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON framework_posture_target_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON framework_posture_target_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON framework_posture_target_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION framework_posture_target_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'framework_posture_target_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER framework_posture_target_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON framework_posture_target_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION framework_posture_target_audit_log_append_only();
