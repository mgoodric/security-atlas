-- Personnel security joiner/leaver workflows.
--
-- Tenant-scoped onboarding/offboarding checklists sourced from HRIS/SCIM/manual
-- events. These rows capture task completion and evidence only; they do not
-- provision or deprovision access.

CREATE TABLE personnel_security_checklists (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    workflow_kind       TEXT NOT NULL,
    source              TEXT NOT NULL,
    source_event_id     TEXT NULL,
    person_external_id  TEXT NOT NULL,
    person_work_email   TEXT NOT NULL DEFAULT '',
    person_display_name TEXT NOT NULL DEFAULT '',
    control_id          UUID NULL,
    due_at              TIMESTAMPTZ NOT NULL,
    status              TEXT NOT NULL DEFAULT 'open',
    created_by          TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ NULL,

    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, control_id) REFERENCES controls (tenant_id, id) ON DELETE RESTRICT,

    CONSTRAINT personnel_security_checklists_kind_chk
        CHECK (workflow_kind IN ('onboarding', 'offboarding')),
    CONSTRAINT personnel_security_checklists_source_chk
        CHECK (source IN ('manual', 'rippling', 'bamboohr', 'scim')),
    CONSTRAINT personnel_security_checklists_status_chk
        CHECK (status IN ('open', 'completed')),
    CONSTRAINT personnel_security_checklists_person_external_id_nonempty
        CHECK (length(person_external_id) > 0)
);

CREATE UNIQUE INDEX personnel_security_checklists_source_event_uniq
    ON personnel_security_checklists (tenant_id, source, source_event_id)
    WHERE source_event_id IS NOT NULL AND length(source_event_id) > 0;

CREATE INDEX idx_personnel_security_checklists_due
    ON personnel_security_checklists (tenant_id, workflow_kind, status, due_at);

ALTER TABLE personnel_security_checklists ENABLE ROW LEVEL SECURITY;
ALTER TABLE personnel_security_checklists FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON personnel_security_checklists
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON personnel_security_checklists
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON personnel_security_checklists
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON personnel_security_checklists
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON personnel_security_checklists TO atlas_app;

CREATE TABLE personnel_security_checklist_items (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL,
    checklist_id       UUID NOT NULL,
    slug               TEXT NOT NULL,
    title              TEXT NOT NULL,
    category           TEXT NOT NULL,
    sort_order         INTEGER NOT NULL DEFAULT 0,
    completed_at       TIMESTAMPTZ NULL,
    completed_by       TEXT NULL,
    evidence_record_id UUID NULL,
    evidence_uri       TEXT NOT NULL DEFAULT '',
    notes              TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, checklist_id, slug),
    FOREIGN KEY (tenant_id, checklist_id)
        REFERENCES personnel_security_checklists (tenant_id, id) ON DELETE CASCADE,

    CONSTRAINT personnel_security_items_slug_nonempty
        CHECK (length(slug) > 0),
    CONSTRAINT personnel_security_items_completion_actor
        CHECK (completed_at IS NULL OR length(COALESCE(completed_by, '')) > 0)
);

CREATE INDEX idx_personnel_security_items_checklist
    ON personnel_security_checklist_items (tenant_id, checklist_id, sort_order);
CREATE INDEX idx_personnel_security_items_open
    ON personnel_security_checklist_items (tenant_id, checklist_id)
    WHERE completed_at IS NULL;

ALTER TABLE personnel_security_checklist_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE personnel_security_checklist_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON personnel_security_checklist_items
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON personnel_security_checklist_items
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON personnel_security_checklist_items
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON personnel_security_checklist_items
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON personnel_security_checklist_items TO atlas_app;
