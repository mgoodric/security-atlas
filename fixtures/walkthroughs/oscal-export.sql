-- Slice 070 — fixture for `oscal-ssp-export.md`.
--
-- The OSCAL export walkthrough reuses the audit-period fixture. This
-- file freezes the period at the canonical walkthrough cutoff
-- (2026-04-15T12:00Z) so the rest of the walkthrough can demonstrate
-- the post-freeze export path. This is the only fixture that mutates
-- state set by an earlier fixture.

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

UPDATE audit_periods
SET
    status = 'frozen',
    frozen_at = '2026-04-15T12:00:00Z',
    frozen_by = 'demo-operator@example.invalid',
    frozen_hash = decode('a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0', 'hex')
WHERE id = '55555555-5555-5555-5555-555555550001'
  AND status = 'open';

-- #3066: a frozen period carries its recorded control set; without one,
-- replay and import amendment fail closed (period.ErrNoControlSnapshot).
INSERT INTO audit_period_frozen_controls (tenant_id, audit_period_id, control_ids, source)
SELECT ap.tenant_id, ap.id,
       ARRAY(SELECT c.id FROM controls c WHERE c.tenant_id = ap.tenant_id ORDER BY c.id),
       'seed'
FROM audit_periods ap
WHERE ap.tenant_id = '00000000-0000-0000-0000-00000000d3a0'
  AND ap.id = '55555555-5555-5555-5555-555555550001'
  AND ap.status = 'frozen'
ON CONFLICT DO NOTHING;

COMMIT;
