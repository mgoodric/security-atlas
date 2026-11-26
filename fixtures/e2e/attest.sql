-- #2089 — Playwright e2e seed for `web/e2e/attest.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The base control is `automated`, and the attest form route
-- (`GET /v1/controls/{id}/attest-form`) rejects anything that is not
-- manual, so this file seeds one `manual_attested` control in the demo
-- tenant. The e2e JWT carries the admin role, which satisfies any
-- owner_role, so `caller_can_attest` is true.
--
-- manual_evidence_schema is left NULL: the form then renders only the
-- platform-required statement field, which is all the spec fills.

BEGIN;

INSERT INTO controls (
    id, tenant_id, scf_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '33333333-3333-3333-3333-33333333a001',
    '00000000-0000-0000-0000-00000000d3a0',
    'IAC-17',
    'Quarterly access review — production consoles',
    'The security lead reviews production console access each quarter and attests to the result.',
    'Identity & Access Management',
    'manual_attested',
    'security-engineering',
    'active',
    '{"op":"true"}',
    'demo-access-review-attest',
    'quarterly'
)
ON CONFLICT DO NOTHING;

COMMIT;
