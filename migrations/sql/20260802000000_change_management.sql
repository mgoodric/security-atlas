-- security-atlas -- OE-629: lightweight change-management register.
--
-- Change records are operational changes for SOC 2 CC8 evidence, distinct
-- from action plans (remediation commitments) and decision logs
-- (architecture/product rationale). Lifecycle:
--
--     proposed -> approved -> implemented -> verified
--
-- The approval and verification transitions emit evidence_records in the
-- application store for every affected control.

CREATE TABLE changes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    title           TEXT NOT NULL CHECK (length(title) > 0 AND length(title) <= 200),
    description     TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
    source          TEXT NOT NULL DEFAULT 'manual'
                    CHECK (source IN ('manual', 'jira', 'csv')),
    source_ref      TEXT NOT NULL DEFAULT '' CHECK (length(source_ref) <= 200),
    source_url      TEXT NOT NULL DEFAULT '' CHECK (length(source_url) <= 1000),
    status          TEXT NOT NULL DEFAULT 'proposed'
                    CHECK (status IN ('proposed', 'approved', 'implemented', 'verified')),
    proposed_by     UUID NOT NULL,
    proposed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    approver_id     UUID,
    approved_at     TIMESTAMPTZ,
    implemented_by  UUID,
    implemented_at  TIMESTAMPTZ,
    verified_by     UUID,
    verified_at     TIMESTAMPTZ,
    risk_notes      TEXT NOT NULL DEFAULT '' CHECK (length(risk_notes) <= 4000),
    rollback_notes  TEXT NOT NULL DEFAULT '' CHECK (length(rollback_notes) <= 4000),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT changes_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT changes_proposer_fk FOREIGN KEY (tenant_id, proposed_by)
        REFERENCES users (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT changes_approver_fk FOREIGN KEY (tenant_id, approver_id)
        REFERENCES users (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT changes_implemented_by_fk FOREIGN KEY (tenant_id, implemented_by)
        REFERENCES users (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT changes_verified_by_fk FOREIGN KEY (tenant_id, verified_by)
        REFERENCES users (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT changes_approved_fields_chk
        CHECK ((status IN ('approved','implemented','verified')) = (approver_id IS NOT NULL AND approved_at IS NOT NULL)),
    CONSTRAINT changes_implemented_fields_chk
        CHECK ((status IN ('implemented','verified')) = (implemented_by IS NOT NULL AND implemented_at IS NOT NULL)),
    CONSTRAINT changes_verified_fields_chk
        CHECK ((status = 'verified') = (verified_by IS NOT NULL AND verified_at IS NOT NULL))
);

CREATE INDEX changes_tenant_status_created
    ON changes (tenant_id, status, created_at DESC);

CREATE UNIQUE INDEX changes_tenant_source_ref_uniq
    ON changes (tenant_id, source, source_ref)
    WHERE source_ref <> '';

ALTER TABLE changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE changes FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON changes
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON changes
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON changes
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE ON changes TO atlas_app;

CREATE OR REPLACE FUNCTION changes_status_transition_guard()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF (OLD.status = 'proposed'    AND NEW.status = 'approved')
    OR (OLD.status = 'approved'    AND NEW.status = 'implemented')
    OR (OLD.status = 'implemented' AND NEW.status = 'verified')
    THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'illegal change status transition: % -> %', OLD.status, NEW.status
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER changes_status_transition_trg
    BEFORE UPDATE ON changes
    FOR EACH ROW
    EXECUTE FUNCTION changes_status_transition_guard();

CREATE TABLE change_controls (
    change_id  UUID NOT NULL,
    control_id UUID NOT NULL,
    tenant_id  UUID NOT NULL,
    linked_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_by  UUID NOT NULL,

    PRIMARY KEY (change_id, control_id),
    CONSTRAINT change_controls_change_fk
        FOREIGN KEY (tenant_id, change_id)
        REFERENCES changes (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT change_controls_control_fk
        FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT change_controls_linked_by_fk
        FOREIGN KEY (tenant_id, linked_by)
        REFERENCES users (tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX change_controls_control
    ON change_controls (tenant_id, control_id);

ALTER TABLE change_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE change_controls FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON change_controls
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON change_controls
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON change_controls
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE ON change_controls TO atlas_app;

CREATE TABLE change_audit_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    change_id   UUID NOT NULL,
    actor_id    UUID NOT NULL,
    action_type TEXT NOT NULL
                CHECK (action_type IN (
                    'created',
                    'approved',
                    'implemented',
                    'verified',
                    'control_linked'
                )),
    before_state JSONB,
    after_state  JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT change_audit_log_change_fk
        FOREIGN KEY (tenant_id, change_id)
        REFERENCES changes (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT change_audit_log_actor_fk
        FOREIGN KEY (tenant_id, actor_id)
        REFERENCES users (tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX change_audit_log_tenant_change
    ON change_audit_log (tenant_id, change_id, created_at ASC);

ALTER TABLE change_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE change_audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON change_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON change_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON change_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION change_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'change_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER change_audit_log_no_update_trg
    BEFORE UPDATE OR DELETE ON change_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION change_audit_log_append_only();
