-- #2111 — Playwright e2e seed for `web/e2e/attest-result.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Seeds one manual_attested control of its own, so the real
-- attestations this spec records never land on the control attest.spec.ts
-- uses. The e2e JWT carries the admin role, which satisfies any
-- owner_role.
--
-- manual_evidence_schema is left NULL: the form then renders only the
-- platform fields (result, fail reason, statement).

BEGIN;

INSERT INTO controls (
    id, tenant_id, scf_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '33333333-3333-3333-3333-33333333a111',
    '00000000-0000-0000-0000-00000000d3a0',
    'IAC-17',
    'Quarterly privileged access attestation',
    'The security lead attests each quarter that privileged access matches the approved list.',
    'Identity & Access Management',
    'manual_attested',
    'security-engineering',
    'active',
    'env == "prod"',
    'demo-access-attest-result',
    'quarterly'
)
ON CONFLICT DO NOTHING;

COMMIT;
