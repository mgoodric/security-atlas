-- security-atlas — slice 052 seed: 10 default themes per Plans/canvas/06-risk.md §6.5.
--
-- Idempotent — ON CONFLICT DO NOTHING on the partial unique index
-- `org_themes_default_name_unique`. Safe to re-run; missing rows are inserted,
-- existing rows are untouched. Seed migration runs as `atlas_migrate`
-- (BYPASSRLS), which is the only role permitted to insert tenant_id = NULL
-- rows — the application-role policy on org_themes forbids NULL-tenant
-- writes.
--
-- These ten names ARE the canonical default vocabulary. Renaming or removing
-- any of them is a behavior-breaking change for every tenant that has tagged
-- risks with these themes; it should be done via a dedicated migration with
-- a backfill path, NOT by editing this file.
--
-- UUIDs are generated at INSERT time via gen_random_uuid() (pgcrypto built-in
-- to PG 13+); this is fine for defaults because the FK target of `themes` is
-- a text[] on risks, not the UUIDs. The UUIDs only matter as primary keys.

INSERT INTO org_themes (id, tenant_id, theme_name, description)
VALUES
    (gen_random_uuid(), NULL, 'ownership',
        'Asset, service, or resource without an identified owner.'),
    (gen_random_uuid(), NULL, 'tech-debt',
        'Known shortcuts, MVP scaffolding, or deferred-by-design work.'),
    (gen_random_uuid(), NULL, 'access-control',
        'Identity, authentication, authorization, and privilege management.'),
    (gen_random_uuid(), NULL, 'key-management',
        'Secrets, certificates, and rotation.'),
    (gen_random_uuid(), NULL, 'data-protection',
        'Encryption, classification, and residency.'),
    (gen_random_uuid(), NULL, 'availability',
        'Uptime, redundancy, and business continuity planning.'),
    (gen_random_uuid(), NULL, 'monitoring',
        'Detection, logging, and audit trail.'),
    (gen_random_uuid(), NULL, 'supply-chain',
        'Third-party dependencies, OSS, and build pipeline.'),
    (gen_random_uuid(), NULL, 'vendor-risk',
        'Direct vendor management.'),
    (gen_random_uuid(), NULL, 'human-process',
        'Training, awareness, and manual workflow.')
ON CONFLICT (theme_name) WHERE tenant_id IS NULL DO NOTHING;
