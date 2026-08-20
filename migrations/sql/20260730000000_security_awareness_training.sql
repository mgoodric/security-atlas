-- Security awareness training program primitive (OPENENGINE-626).
--
-- Tracks tenant-owned people, training courses/campaigns, per-person
-- assignments/completions, and phishing-simulation outcomes. This is a tracking
-- surface only; it does not deliver courses or host an LMS.

CREATE TABLE security_training_people (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL,
    source             TEXT NOT NULL DEFAULT 'manual',
    source_person_id   TEXT NULL,
    display_name       TEXT NOT NULL,
    work_email         TEXT NULL,
    recipient_user_id  TEXT NULL,
    active             BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT security_training_people_source_chk
        CHECK (source IN ('manual', 'hris', 'scim')),
    CONSTRAINT security_training_people_display_name_nonempty
        CHECK (length(btrim(display_name)) > 0),
    CONSTRAINT security_training_people_source_ref_chk
        CHECK (source = 'manual' OR source_person_id IS NOT NULL)
);

CREATE UNIQUE INDEX security_training_people_source_unique
    ON security_training_people (tenant_id, source, source_person_id)
    WHERE source_person_id IS NOT NULL;

ALTER TABLE security_training_people
    ADD CONSTRAINT security_training_people_tenant_id_unique UNIQUE (tenant_id, id);

CREATE UNIQUE INDEX security_training_people_email_unique
    ON security_training_people (tenant_id, lower(work_email))
    WHERE work_email IS NOT NULL;

CREATE TABLE security_training_courses (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    code        TEXT NOT NULL,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    required    BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT security_training_courses_code_nonempty
        CHECK (length(btrim(code)) > 0),
    CONSTRAINT security_training_courses_title_nonempty
        CHECK (length(btrim(title)) > 0)
);

CREATE UNIQUE INDEX security_training_courses_code_unique
    ON security_training_courses (tenant_id, lower(code));

ALTER TABLE security_training_courses
    ADD CONSTRAINT security_training_courses_tenant_id_unique UNIQUE (tenant_id, id);

CREATE TABLE security_training_campaigns (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    course_id   UUID NOT NULL,
    name        TEXT NOT NULL,
    starts_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    due_at      TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT security_training_campaigns_name_nonempty
        CHECK (length(btrim(name)) > 0),
    CONSTRAINT security_training_campaigns_due_after_start
        CHECK (due_at >= starts_at),
    CONSTRAINT security_training_campaigns_course_fk
        FOREIGN KEY (tenant_id, course_id)
        REFERENCES security_training_courses (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX security_training_campaigns_unique
    ON security_training_campaigns (tenant_id, course_id, lower(name));

ALTER TABLE security_training_campaigns
    ADD CONSTRAINT security_training_campaigns_tenant_id_unique UNIQUE (tenant_id, id);

CREATE TABLE security_training_assignments (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL,
    campaign_id        UUID NOT NULL,
    person_id          UUID NOT NULL,
    due_at             TIMESTAMPTZ NOT NULL,
    assigned_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at       TIMESTAMPTZ NULL,
    completion_source  TEXT NULL,
    evidence_record_id UUID NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT security_training_assignments_completion_source_chk
        CHECK (completion_source IS NULL OR completion_source IN ('manual', 'csv', 'lms_connector')),
    CONSTRAINT security_training_assignments_completion_source_required
        CHECK ((completed_at IS NULL AND completion_source IS NULL) OR (completed_at IS NOT NULL AND completion_source IS NOT NULL)),
    CONSTRAINT security_training_assignments_campaign_fk
        FOREIGN KEY (tenant_id, campaign_id)
        REFERENCES security_training_campaigns (tenant_id, id)
        ON DELETE CASCADE,
    CONSTRAINT security_training_assignments_person_fk
        FOREIGN KEY (tenant_id, person_id)
        REFERENCES security_training_people (tenant_id, id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX security_training_assignments_unique
    ON security_training_assignments (tenant_id, campaign_id, person_id);

ALTER TABLE security_training_assignments
    ADD CONSTRAINT security_training_assignments_tenant_id_unique UNIQUE (tenant_id, id);

CREATE INDEX security_training_assignments_due_idx
    ON security_training_assignments (tenant_id, due_at)
    WHERE completed_at IS NULL;

CREATE TABLE security_training_phishing_results (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL,
    assignment_id  UUID NOT NULL,
    simulation_id  TEXT NOT NULL,
    sent_at        TIMESTAMPTZ NOT NULL,
    outcome        TEXT NOT NULL,
    clicked_at     TIMESTAMPTZ NULL,
    reported_at    TIMESTAMPTZ NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT security_training_phishing_sim_nonempty
        CHECK (length(btrim(simulation_id)) > 0),
    CONSTRAINT security_training_phishing_outcome_chk
        CHECK (outcome IN ('no_click', 'clicked', 'reported', 'credential_submitted')),
    CONSTRAINT security_training_phishing_assignment_fk
        FOREIGN KEY (tenant_id, assignment_id)
        REFERENCES security_training_assignments (tenant_id, id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX security_training_phishing_unique
    ON security_training_phishing_results (tenant_id, assignment_id, simulation_id);

ALTER TABLE security_training_people ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_training_people FORCE ROW LEVEL SECURITY;
ALTER TABLE security_training_courses ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_training_courses FORCE ROW LEVEL SECURITY;
ALTER TABLE security_training_campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_training_campaigns FORCE ROW LEVEL SECURITY;
ALTER TABLE security_training_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_training_assignments FORCE ROW LEVEL SECURITY;
ALTER TABLE security_training_phishing_results ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_training_phishing_results FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON security_training_people
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON security_training_people
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON security_training_people
    FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON security_training_people
    FOR DELETE USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_read ON security_training_courses
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON security_training_courses
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON security_training_courses
    FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON security_training_courses
    FOR DELETE USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_read ON security_training_campaigns
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON security_training_campaigns
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON security_training_campaigns
    FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON security_training_campaigns
    FOR DELETE USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_read ON security_training_assignments
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON security_training_assignments
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON security_training_assignments
    FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON security_training_assignments
    FOR DELETE USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_read ON security_training_phishing_results
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON security_training_phishing_results
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON security_training_phishing_results
    FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON security_training_phishing_results
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON security_training_people TO atlas_app, atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON security_training_courses TO atlas_app, atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON security_training_campaigns TO atlas_app, atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON security_training_assignments TO atlas_app, atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON security_training_phishing_results TO atlas_app, atlas_migrate;
