-- Reverses 20261008000000_taxonomy_audit_log.sql.
DROP TABLE IF EXISTS taxonomy_audit_log;
DROP FUNCTION IF EXISTS taxonomy_audit_log_append_only();

-- Restore #1809's per-slug seed.
CREATE OR REPLACE FUNCTION seed_risk_categories(p_tenant_id UUID)
RETURNS VOID
LANGUAGE sql
AS $$
    INSERT INTO org_risk_categories (tenant_id, slug, name, description, is_default)
    SELECT
        p_tenant_id,
        s.slug,
        s.name,
        s.description,
        s.slug = 'operational' AND NOT EXISTS (
            SELECT 1 FROM org_risk_categories d
            WHERE d.tenant_id = p_tenant_id AND d.is_default
        )
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
    ON CONFLICT DO NOTHING;
$$;
