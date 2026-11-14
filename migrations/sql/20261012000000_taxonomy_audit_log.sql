-- security-atlas — #1810: an append-only audit trail for an organisation's
-- risk taxonomy (#1745).
--
-- Category management (add, rename, archive, un-archive, set default,
-- delete) writes one row here per change, in the same transaction as the
-- change, naming the actor and the category before and after. `subject_kind`
-- names what changed; the theme-management ticket of #1745 adds 'theme' to
-- its CHECK rather than a second table.
--
-- Shape follows risk_audit_log (#1749): RLS with SELECT + INSERT policies
-- only, plus a trigger that denies UPDATE and DELETE even for a role that
-- bypasses RLS. The rows appear in the unified admin audit log as kind
-- `taxonomy`.
--
-- No FK to org_risk_categories: the trail of a deleted category outlives it.

CREATE TABLE taxonomy_audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    subject_kind  TEXT NOT NULL
                  CONSTRAINT taxonomy_audit_log_subject_kind_check
                  CHECK (subject_kind IN ('category')),
    subject_id    UUID NOT NULL,
    -- The category's slug, so a deleted category's history still reads.
    subject_slug  TEXT NOT NULL,
    action        TEXT NOT NULL
                  CONSTRAINT taxonomy_audit_log_action_check
                  CHECK (action IN ('create', 'rename', 'archive', 'unarchive', 'set_default', 'delete')),
    -- before is NULL on create; after is NULL on delete.
    before        JSONB NULL,
    after         JSONB NULL,
    -- The credential that made the change; never anonymous.
    actor         TEXT NOT NULL,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Slice 180 convention for the audit-log family.
    subject_module TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT taxonomy_audit_log_actor_nonempty CHECK (length(actor) > 0),
    CONSTRAINT taxonomy_audit_log_state_present
        CHECK (before IS NOT NULL OR after IS NOT NULL)
);

CREATE INDEX idx_taxonomy_audit_log_tenant_occurred
    ON taxonomy_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX idx_taxonomy_audit_log_subject
    ON taxonomy_audit_log (tenant_id, subject_kind, subject_id, occurred_at);

ALTER TABLE taxonomy_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE taxonomy_audit_log FORCE ROW LEVEL SECURITY;

-- Append-only by RLS construction: SELECT + INSERT policies only.
CREATE POLICY tenant_read ON taxonomy_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON taxonomy_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON taxonomy_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION taxonomy_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'taxonomy_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER taxonomy_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON taxonomy_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION taxonomy_audit_log_append_only();

-- ===== seed only a tenant that has no categories =====
--
-- #1809's seed skipped slugs the tenant already had, so it restored any
-- seeded category the tenant had since deleted the next time a list, a risk
-- write or the sync trigger called it. Now that categories can be deleted
-- (#1810), the seed runs only for a tenant with no categories at all: the
-- seven arrive together on first use and never come back once removed.
CREATE OR REPLACE FUNCTION seed_risk_categories(p_tenant_id UUID)
RETURNS VOID
LANGUAGE sql
AS $$
    INSERT INTO org_risk_categories (tenant_id, slug, name, description, is_default)
    SELECT p_tenant_id, s.slug, s.name, s.description, s.slug = 'operational'
    FROM (VALUES
        ('confidentiality', 'Confidentiality',
            'Unauthorised disclosure of information.'),
        ('integrity', 'Integrity',
            'Unauthorised or accidental modification of information or systems.'),
        ('availability', 'Availability',
            'Loss of access to information or services.'),
        ('privacy', 'Privacy',
            'Handling of personal data contrary to obligations or expectations.'),
        ('regulatory', 'Regulatory',
            'Failure to meet a legal, regulatory or contractual requirement.'),
        ('operational', 'Operational',
            'Failure of people, process or technology in day-to-day operation.'),
        ('financial', 'Financial',
            'Direct monetary loss.')
    ) AS s(slug, name, description)
    WHERE NOT EXISTS (
        SELECT 1 FROM org_risk_categories c WHERE c.tenant_id = p_tenant_id
    )
    ON CONFLICT DO NOTHING;
$$;
