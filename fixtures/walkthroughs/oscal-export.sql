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

COMMIT;
