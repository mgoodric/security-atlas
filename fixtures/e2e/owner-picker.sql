-- #2531 — the owner the e2e owner pickers choose.
--
-- risks-create.spec.ts and create-toasts.spec.ts pick the first user the
-- real `/api/people?module=risk` lists. That is the shared e2e user, the
-- `sub` of the token web/e2e/global-setup.ts mints. Since #2501 a user
-- with no live role lacks every optional module and the picker leaves
-- them out, so the user needs its live roles too. Seeding both here lets
-- each spec pass run alone and in any order, instead of relying on an
-- earlier fixture having created them.
--
-- The users row matches audits-header.sql's byte for byte: DO NOTHING
-- keeps whichever fixture ran first, and audits-header.spec.ts pins the
-- display name while settings.spec.ts pins the time zone.
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject,
    time_zone
)
VALUES (
    '44444444-4444-4444-4444-444444440001',
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-operator@example.invalid',
    'Sam Operator',
    'active',
    'urn:atlas:test',
    'demo-operator-subject',
    'America/New_York'
)
ON CONFLICT DO NOTHING;

-- The live roles the e2e token claims (settings.sql seeds the same rows).
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-00000000d3a0', '44444444-4444-4444-4444-444444440001', 'admin', 'e2e:2531'),
       ('00000000-0000-0000-0000-00000000d3a0', '44444444-4444-4444-4444-444444440001', 'grc_engineer', 'e2e:2531')
ON CONFLICT DO NOTHING;
