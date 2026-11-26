-- security-atlas — policy library import with historical lifecycle (#1747).
--
-- An adopter with an existing policy library imports it as published
-- policies that keep their real history: who approved each one and when,
-- when it took effect, when it is next due for review, and where the
-- source document lives. Imported rows are marked and every import is
-- audit-logged.
--
--   * source_attribution gains 'imported'.
--   * New columns on policies:
--       historical_approver_name  free-text name of whoever approved the
--                                 policy before it reached the tool. Kept
--                                 apart from approved_by, which holds a
--                                 credential id of an in-tool approver.
--       historical_approved_at    date of that approval.
--       source_url                link to the source document.
--       imported_by / imported_at the credential that ran the import.
--     A CHECK requires the historical approver, date, importer and import
--     time on every imported row, and forbids them on any other row.
--   * policy_audit_log: append-only, tenant-scoped, RLS-forced. One row per
--     imported policy, naming the importer, source file, historical
--     approver and date, and source URL. No UPDATE or DELETE grant, and a
--     trigger refuses both, so a tenant purge classifies it as a ledger.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the database layer: policy_audit_log gets
--       ENABLE + FORCE ROW LEVEL SECURITY with tenant_read / tenant_write.
--
-- Reversible via 20260819000000_policy_import.down.sql.

ALTER TABLE policies
    ADD COLUMN historical_approver_name TEXT        NULL,
    ADD COLUMN historical_approved_at   DATE        NULL,
    ADD COLUMN source_url               TEXT        NULL,
    ADD COLUMN imported_by              TEXT        NULL,
    ADD COLUMN imported_at              TIMESTAMPTZ NULL;

ALTER TABLE policies
    DROP CONSTRAINT policies_source_attribution_chk;

ALTER TABLE policies
    ADD CONSTRAINT policies_source_attribution_chk
        CHECK (source_attribution IN ('community_draft', 'tenant_authored', 'vendor_provided', 'imported')),
    ADD CONSTRAINT policies_imported_fields_chk
        CHECK (
            (source_attribution = 'imported') = (
                historical_approver_name IS NOT NULL
                AND length(historical_approver_name) > 0
                AND historical_approved_at IS NOT NULL
                AND imported_by IS NOT NULL
                AND imported_at IS NOT NULL
            )
        ),
    ADD CONSTRAINT policies_source_url_len
        CHECK (source_url IS NULL OR length(source_url) <= 2000),
    ADD CONSTRAINT policies_historical_approver_name_len
        CHECK (historical_approver_name IS NULL OR length(historical_approver_name) <= 500);

-- Duplicate detection on import looks up (tenant, title, version).
CREATE INDEX idx_policies_tenant_title_version
    ON policies (tenant_id, title, version);

CREATE TABLE policy_audit_log (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                UUID NOT NULL,
    policy_id                UUID NOT NULL,
    action                   TEXT NOT NULL CHECK (action IN ('imported')),
    actor                    TEXT NOT NULL CHECK (length(actor) > 0),
    source_file              TEXT NOT NULL DEFAULT '' CHECK (length(source_file) <= 500),
    historical_approver_name TEXT NULL CHECK (historical_approver_name IS NULL OR length(historical_approver_name) <= 500),
    historical_approved_at   DATE NULL,
    source_url               TEXT NULL,
    subject_module           TEXT NOT NULL DEFAULT 'core',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_policy_audit_log_tenant_policy
    ON policy_audit_log (tenant_id, policy_id, created_at ASC);

ALTER TABLE policy_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE policy_audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON policy_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON policy_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON policy_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION policy_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'policy_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER policy_audit_log_no_update_trg
    BEFORE UPDATE OR DELETE ON policy_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION policy_audit_log_append_only();
