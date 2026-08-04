-- security-atlas -- OE-631: tenant-scoped incident register backend.
--
-- Incidents are mutable state projections with append-only timeline history.
-- Lifecycle:
--
--     detected -> triaged -> contained -> resolved -> closed
--
-- The timeline and link tables expose SELECT + INSERT only to atlas_app under
-- FORCE RLS. Lifecycle ordering is gated in application code so every
-- projection update and timeline insert can occur in one tenant transaction.

ALTER TABLE evidence_records
    ADD CONSTRAINT evidence_records_tenant_id_unique UNIQUE (tenant_id, id);

CREATE TABLE incidents (
    id                    UUID PRIMARY KEY,
    tenant_id             UUID NOT NULL,
    title                 TEXT NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'detected',
    operator_severity     TEXT NOT NULL,
    severity              TEXT NOT NULL,
    affected_system_tier  TEXT NULL,
    affected_systems      JSONB NOT NULL DEFAULT '[]'::jsonb,
    detected_by           TEXT NOT NULL,
    detected_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    triaged_by            TEXT NULL,
    triaged_at            TIMESTAMPTZ NULL,
    contained_by          TEXT NULL,
    contained_at          TIMESTAMPTZ NULL,
    resolved_by           TEXT NULL,
    resolved_at           TIMESTAMPTZ NULL,
    closed_by             TEXT NULL,
    closed_at             TIMESTAMPTZ NULL,
    postmortem_summary    TEXT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT incidents_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT incidents_title_nonempty CHECK (length(btrim(title)) > 0),
    CONSTRAINT incidents_detected_by_nonempty CHECK (length(btrim(detected_by)) > 0),
    CONSTRAINT incidents_status_chk
        CHECK (status IN ('detected', 'triaged', 'contained', 'resolved', 'closed')),
    CONSTRAINT incidents_operator_severity_chk
        CHECK (operator_severity IN ('p3', 'p2', 'p1', 'p0')),
    CONSTRAINT incidents_severity_chk
        CHECK (severity IN ('p3', 'p2', 'p1', 'p0')),
    CONSTRAINT incidents_affected_system_tier_chk
        CHECK (affected_system_tier IS NULL OR affected_system_tier IN ('low', 'medium', 'high', 'critical')),
    CONSTRAINT incidents_affected_systems_array_chk
        CHECK (jsonb_typeof(affected_systems) = 'array'),
    CONSTRAINT incidents_closed_postmortem_chk
        CHECK (status <> 'closed' OR (closed_by IS NOT NULL AND closed_at IS NOT NULL AND length(btrim(postmortem_summary)) > 0))
);

CREATE INDEX incidents_tenant_status_created
    ON incidents (tenant_id, status, created_at DESC);

CREATE INDEX incidents_tenant_severity_created
    ON incidents (tenant_id, severity, created_at DESC);

ALTER TABLE incidents ENABLE ROW LEVEL SECURITY;
ALTER TABLE incidents FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON incidents
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON incidents
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON incidents
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE ON incidents TO atlas_app;

CREATE TABLE incident_controls (
    incident_id UUID NOT NULL,
    control_id  UUID NOT NULL,
    tenant_id   UUID NOT NULL,
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_by   TEXT NOT NULL,

    PRIMARY KEY (tenant_id, incident_id, control_id),
    CONSTRAINT incident_controls_incident_fk
        FOREIGN KEY (tenant_id, incident_id)
        REFERENCES incidents (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT incident_controls_control_fk
        FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT incident_controls_linked_by_nonempty CHECK (length(btrim(linked_by)) > 0)
);

CREATE TABLE incident_risks (
    incident_id UUID NOT NULL,
    risk_id     UUID NOT NULL,
    tenant_id   UUID NOT NULL,
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_by   TEXT NOT NULL,

    PRIMARY KEY (tenant_id, incident_id, risk_id),
    CONSTRAINT incident_risks_incident_fk
        FOREIGN KEY (tenant_id, incident_id)
        REFERENCES incidents (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT incident_risks_risk_fk
        FOREIGN KEY (tenant_id, risk_id)
        REFERENCES risks (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT incident_risks_linked_by_nonempty CHECK (length(btrim(linked_by)) > 0)
);

CREATE TABLE incident_vendors (
    incident_id UUID NOT NULL,
    vendor_id   UUID NOT NULL,
    tenant_id   UUID NOT NULL,
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_by   TEXT NOT NULL,

    PRIMARY KEY (tenant_id, incident_id, vendor_id),
    CONSTRAINT incident_vendors_incident_fk
        FOREIGN KEY (tenant_id, incident_id)
        REFERENCES incidents (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT incident_vendors_vendor_fk
        FOREIGN KEY (tenant_id, vendor_id)
        REFERENCES vendors (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT incident_vendors_linked_by_nonempty CHECK (length(btrim(linked_by)) > 0)
);

CREATE TABLE incident_evidence_links (
    incident_id UUID NOT NULL,
    evidence_id UUID NOT NULL,
    tenant_id   UUID NOT NULL,
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_by   TEXT NOT NULL,

    PRIMARY KEY (tenant_id, incident_id, evidence_id),
    CONSTRAINT incident_evidence_links_incident_fk
        FOREIGN KEY (tenant_id, incident_id)
        REFERENCES incidents (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT incident_evidence_links_evidence_fk
        FOREIGN KEY (tenant_id, evidence_id)
        REFERENCES evidence_records (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT incident_evidence_links_linked_by_nonempty CHECK (length(btrim(linked_by)) > 0)
);

CREATE INDEX incident_controls_control
    ON incident_controls (tenant_id, control_id);
CREATE INDEX incident_risks_risk
    ON incident_risks (tenant_id, risk_id);
CREATE INDEX incident_vendors_vendor
    ON incident_vendors (tenant_id, vendor_id);
CREATE INDEX incident_evidence_links_evidence
    ON incident_evidence_links (tenant_id, evidence_id);

ALTER TABLE incident_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE incident_controls FORCE ROW LEVEL SECURITY;
ALTER TABLE incident_risks ENABLE ROW LEVEL SECURITY;
ALTER TABLE incident_risks FORCE ROW LEVEL SECURITY;
ALTER TABLE incident_vendors ENABLE ROW LEVEL SECURITY;
ALTER TABLE incident_vendors FORCE ROW LEVEL SECURITY;
ALTER TABLE incident_evidence_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE incident_evidence_links FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON incident_controls
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON incident_controls
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_read ON incident_risks
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON incident_risks
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_read ON incident_vendors
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON incident_vendors
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_read ON incident_evidence_links
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON incident_evidence_links
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON incident_controls TO atlas_app;
GRANT SELECT, INSERT ON incident_risks TO atlas_app;
GRANT SELECT, INSERT ON incident_vendors TO atlas_app;
GRANT SELECT, INSERT ON incident_evidence_links TO atlas_app;

CREATE TABLE incident_timeline (
    id          UUID PRIMARY KEY,
    tenant_id   UUID NOT NULL,
    incident_id UUID NOT NULL,
    action      TEXT NOT NULL,
    actor       TEXT NOT NULL,
    from_state  TEXT NULL,
    to_state    TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    subject_module TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT incident_timeline_incident_fk
        FOREIGN KEY (tenant_id, incident_id)
        REFERENCES incidents (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT incident_timeline_action_chk
        CHECK (action IN ('created', 'transitioned', 'closed', 'control_linked', 'risk_linked', 'vendor_linked', 'evidence_linked')),
    CONSTRAINT incident_timeline_actor_nonempty CHECK (length(btrim(actor)) > 0),
    CONSTRAINT incident_timeline_to_state_chk
        CHECK (to_state IN ('detected', 'triaged', 'contained', 'resolved', 'closed')),
    CONSTRAINT incident_timeline_from_state_chk
        CHECK (from_state IS NULL OR from_state IN ('detected', 'triaged', 'contained', 'resolved', 'closed'))
);

CREATE INDEX incident_timeline_tenant_occurred
    ON incident_timeline (tenant_id, occurred_at DESC);
CREATE INDEX incident_timeline_tenant_incident
    ON incident_timeline (tenant_id, incident_id, occurred_at ASC, id ASC);

ALTER TABLE incident_timeline ENABLE ROW LEVEL SECURITY;
ALTER TABLE incident_timeline FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON incident_timeline
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON incident_timeline
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON incident_timeline TO atlas_app;
