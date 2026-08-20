-- OE-633 — Playwright e2e seed for the incident register web workspace.
--
-- Builds on fixtures/walkthroughs/00-seed.sql. Seeds two demo-tenant
-- incidents and one alt-tenant canary incident. The web spec authenticates
-- as the demo tenant and asserts the alt-tenant canary does not render.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO risks (
    id, tenant_id, title, description, category, treatment,
    treatment_owner, inherent_score, residual_score
)
VALUES (
    '77777777-7777-7777-7777-777777776633',
    '00000000-0000-0000-0000-00000000d3a0',
    'Incident response evidence gap',
    'Risk used by the incident register e2e fixture.',
    'confidentiality',
    'mitigate',
    'security-engineering',
    '{"likelihood": 3, "impact": 4}'::jsonb,
    '{"likelihood": 2, "impact": 3}'::jsonb
)
ON CONFLICT DO NOTHING;

INSERT INTO vendors (
    id, tenant_id, name, domain, criticality, review_cadence, owner_user
)
VALUES (
    '55555555-5555-5555-5555-555555556633',
    '00000000-0000-0000-0000-00000000d3a0',
    'Fixture Monitoring',
    'monitoring.example.invalid',
    'high',
    'quarterly',
    'security-engineering'
)
ON CONFLICT DO NOTHING;

INSERT INTO evidence_records (
    id, tenant_id, control_id, scope_id, observed_at, provenance, result,
    payload, hash, freshness_class, ingestion_path, source_attribution,
    control_ref, evidence_kind, schema_version
)
VALUES (
    '66666666-6666-6666-6666-666666666633',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '22222222-2222-2222-2222-222222220001',
    '2026-08-04T09:55:00Z',
    '{"source":"incident-e2e"}'::jsonb,
    'pass',
    '{"alert":"acknowledged"}'::jsonb,
    'sha256-incident-fixture-01',
    'monthly',
    'push',
    '{"actor":"demo-operator@example.invalid"}'::jsonb,
    'CRY-05',
    'incident.fixture.v1',
    '1.0.0'
)
ON CONFLICT DO NOTHING;

INSERT INTO incidents (
    id, tenant_id, title, description, status, operator_severity, severity,
    affected_system_tier, affected_systems, detected_by, detected_at,
    triaged_by, triaged_at, contained_by, contained_at, resolved_by, resolved_at
)
VALUES (
    '63363363-6336-6336-6336-633633633001',
    '00000000-0000-0000-0000-00000000d3a0',
    'Fixture resolved incident',
    'Resolved incident ready for postmortem closure.',
    'resolved',
    'p1',
    'p1',
    'critical',
    '[{"name":"api-prod","tier":"critical","environment":"prod"},{"name":"auth-prod","tier":"high","environment":"prod"}]'::jsonb,
    'demo-operator@example.invalid',
    '2026-08-04T10:00:00Z',
    'demo-operator@example.invalid',
    '2026-08-04T10:05:00Z',
    'demo-operator@example.invalid',
    '2026-08-04T10:15:00Z',
    'demo-operator@example.invalid',
    '2026-08-04T10:35:00Z'
),
(
    '63363363-6336-6336-6336-633633633002',
    '00000000-0000-0000-0000-00000000d3a0',
    'Fixture detected incident',
    'Detected incident waiting for triage.',
    'detected',
    'p2',
    'p2',
    'high',
    '[{"name":"worker-prod","tier":"high","environment":"prod"}]'::jsonb,
    'demo-operator@example.invalid',
    '2026-08-04T11:00:00Z',
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL
)
ON CONFLICT DO NOTHING;

INSERT INTO incident_controls (incident_id, control_id, tenant_id, linked_by)
VALUES
    ('63363363-6336-6336-6336-633633633001', '33333333-3333-3333-3333-333333330001', '00000000-0000-0000-0000-00000000d3a0', 'demo-operator@example.invalid'),
    ('63363363-6336-6336-6336-633633633002', '33333333-3333-3333-3333-333333330001', '00000000-0000-0000-0000-00000000d3a0', 'demo-operator@example.invalid')
ON CONFLICT DO NOTHING;

INSERT INTO incident_risks (incident_id, risk_id, tenant_id, linked_by)
VALUES ('63363363-6336-6336-6336-633633633001', '77777777-7777-7777-7777-777777776633', '00000000-0000-0000-0000-00000000d3a0', 'demo-operator@example.invalid')
ON CONFLICT DO NOTHING;

INSERT INTO incident_vendors (incident_id, vendor_id, tenant_id, linked_by)
VALUES ('63363363-6336-6336-6336-633633633001', '55555555-5555-5555-5555-555555556633', '00000000-0000-0000-0000-00000000d3a0', 'demo-operator@example.invalid')
ON CONFLICT DO NOTHING;

INSERT INTO incident_evidence_links (incident_id, evidence_id, tenant_id, linked_by)
VALUES ('63363363-6336-6336-6336-633633633001', '66666666-6666-6666-6666-666666666633', '00000000-0000-0000-0000-00000000d3a0', 'demo-operator@example.invalid')
ON CONFLICT DO NOTHING;

INSERT INTO incident_timeline (
    id, tenant_id, incident_id, action, actor, from_state, to_state, summary,
    occurred_at
)
VALUES
    ('63363363-6336-6336-6336-63363363a001', '00000000-0000-0000-0000-00000000d3a0', '63363363-6336-6336-6336-633633633001', 'created', 'demo-operator@example.invalid', NULL, 'detected', 'incident logged', '2026-08-04T10:00:00Z'),
    ('63363363-6336-6336-6336-63363363a002', '00000000-0000-0000-0000-00000000d3a0', '63363363-6336-6336-6336-633633633001', 'transitioned', 'demo-operator@example.invalid', 'detected', 'triaged', 'triage complete', '2026-08-04T10:05:00Z'),
    ('63363363-6336-6336-6336-63363363a003', '00000000-0000-0000-0000-00000000d3a0', '63363363-6336-6336-6336-633633633001', 'transitioned', 'demo-operator@example.invalid', 'triaged', 'contained', 'contained blast radius', '2026-08-04T10:15:00Z'),
    ('63363363-6336-6336-6336-63363363a004', '00000000-0000-0000-0000-00000000d3a0', '63363363-6336-6336-6336-633633633001', 'transitioned', 'demo-operator@example.invalid', 'contained', 'resolved', 'restored service', '2026-08-04T10:35:00Z'),
    ('63363363-6336-6336-6336-63363363a005', '00000000-0000-0000-0000-00000000d3a0', '63363363-6336-6336-6336-633633633002', 'created', 'demo-operator@example.invalid', NULL, 'detected', 'incident logged', '2026-08-04T11:00:00Z')
ON CONFLICT DO NOTHING;

COMMIT;

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000a17e';

INSERT INTO incidents (
    id, tenant_id, title, description, status, operator_severity, severity,
    affected_systems, detected_by, detected_at
)
VALUES (
    '63363363-6336-6336-6336-633633633999',
    '00000000-0000-0000-0000-00000000a17e',
    'Alt-tenant incident canary',
    'Must not appear in the demo tenant UI.',
    'detected',
    'p2',
    'p2',
    '[{"name":"alt-system"}]'::jsonb,
    'alt-operator@example.invalid',
    '2026-08-04T12:00:00Z'
)
ON CONFLICT DO NOTHING;

COMMIT;
