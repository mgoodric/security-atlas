-- security-atlas -- operational access-review / recertification campaigns.
--
-- This adds a tenant-scoped workflow for periodic access certification:
-- define a scoped campaign, snapshot review items from SCIM or manual CSV,
-- assign reviewers, record keep/revoke attestations with reasons, export the
-- revoke list, emit completion evidence, and create due reminders.
--
-- No table here revokes access. Revoke attestations are decisions only; access
-- enforcement remains an operator or future connector action.

CREATE TABLE access_review_campaigns (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL,
    name               TEXT NOT NULL,
    source             TEXT NOT NULL CHECK (source IN ('scim', 'manual_csv')),
    scope_systems      TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    scope_entitlements TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    scope_user_ids     TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    status             TEXT NOT NULL DEFAULT 'draft'
                       CHECK (status IN ('draft', 'active', 'completed', 'cancelled')),
    due_at             TIMESTAMPTZ NOT NULL,
    created_by         TEXT NOT NULL,
    completed_at       TIMESTAMPTZ NULL,
    evidence_record_id UUID NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT access_review_campaigns_name_nonempty CHECK (length(name) > 0),
    CONSTRAINT access_review_campaigns_created_by_nonempty CHECK (length(created_by) > 0),
    CONSTRAINT access_review_campaigns_tenant_id_unique UNIQUE (tenant_id, id)
);

CREATE INDEX access_review_campaigns_tenant_due
    ON access_review_campaigns (tenant_id, due_at, status);

ALTER TABLE access_review_campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_review_campaigns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON access_review_campaigns
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON access_review_campaigns
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON access_review_campaigns
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON access_review_campaigns
    FOR DELETE USING (current_tenant_matches(tenant_id));

CREATE TABLE access_review_reviewer_assignments (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    campaign_id   UUID NOT NULL,
    reviewer_id   TEXT NOT NULL,
    assigned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT access_review_assignment_reviewer_nonempty CHECK (length(reviewer_id) > 0),
    CONSTRAINT access_review_assignment_unique UNIQUE (tenant_id, campaign_id, reviewer_id),
    CONSTRAINT access_review_assignment_campaign_fk
        FOREIGN KEY (tenant_id, campaign_id)
        REFERENCES access_review_campaigns (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX access_review_assignments_reviewer
    ON access_review_reviewer_assignments (tenant_id, reviewer_id, campaign_id);

ALTER TABLE access_review_reviewer_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_review_reviewer_assignments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON access_review_reviewer_assignments
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON access_review_reviewer_assignments
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON access_review_reviewer_assignments
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON access_review_reviewer_assignments
    FOR DELETE USING (current_tenant_matches(tenant_id));

CREATE TABLE access_review_items (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL,
    campaign_id          UUID NOT NULL,
    system               TEXT NOT NULL,
    entitlement          TEXT NOT NULL,
    principal_user_id    TEXT NOT NULL,
    principal_email      TEXT NOT NULL DEFAULT '',
    reviewer_id          TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'attested')),
    decision             TEXT NULL CHECK (decision IN ('keep', 'revoke')),
    reason               TEXT NOT NULL DEFAULT '',
    attested_by          TEXT NULL,
    attested_at          TIMESTAMPTZ NULL,
    source               TEXT NOT NULL CHECK (source IN ('scim', 'manual_csv')),
    source_ref           TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT access_review_items_system_nonempty CHECK (length(system) > 0),
    CONSTRAINT access_review_items_entitlement_nonempty CHECK (length(entitlement) > 0),
    CONSTRAINT access_review_items_principal_nonempty CHECK (length(principal_user_id) > 0),
    CONSTRAINT access_review_items_reviewer_nonempty CHECK (length(reviewer_id) > 0),
    CONSTRAINT access_review_items_campaign_fk
        FOREIGN KEY (tenant_id, campaign_id)
        REFERENCES access_review_campaigns (tenant_id, id)
        ON DELETE CASCADE,
    CONSTRAINT access_review_items_attestation_complete CHECK (
        (status = 'pending' AND decision IS NULL AND attested_by IS NULL AND attested_at IS NULL)
        OR
        (status = 'attested' AND decision IS NOT NULL AND length(reason) > 0
         AND attested_by IS NOT NULL AND attested_at IS NOT NULL)
    )
);

CREATE INDEX access_review_items_campaign
    ON access_review_items (tenant_id, campaign_id, status, reviewer_id);
CREATE INDEX access_review_items_revoke_export
    ON access_review_items (tenant_id, campaign_id, decision)
    WHERE decision = 'revoke';

ALTER TABLE access_review_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_review_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON access_review_items
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON access_review_items
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON access_review_items
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON access_review_items
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON access_review_campaigns TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON access_review_campaigns TO atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON access_review_reviewer_assignments TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON access_review_reviewer_assignments TO atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON access_review_items TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON access_review_items TO atlas_migrate;
