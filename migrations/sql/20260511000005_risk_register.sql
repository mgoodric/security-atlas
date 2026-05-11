-- security-atlas — risk register CRUD + treatment-status validation (slice 019).
--
-- Augments the slice-002 `risks` skeleton with the per-treatment fields canvas
-- §6.1 calls out, a many-to-many join table to controls, defense-in-depth
-- CHECK constraints, and the four-policy RLS split (read/write/update/delete)
-- that slice 014 established as the canonical pattern.
--
-- Open question #04 (risk-methodology default) is RESOLVED by this slice: the
-- default is `nist_800_30` (already encoded in the slice-002 column default).
-- The methodology enum stays pluggable — per-risk, not global.
--
-- See ARCHITECTURE_CANVAS.md §2.2 (Risk entity) and §6.1 (treatment statuses)
-- plus docs/issues/019-risk-register-crud.md.

-- ===== ALTER risks: per-treatment fields =====
--
-- `accepted_until` is the date on which a `treatment='accept'` decision lapses;
-- exec must re-attest after that. `accepter` is the user who signed off.
-- `instrument_reference` carries the insurance/policy/SOW identifier when
-- `treatment='transfer'`. All three default to NULL/empty so existing rows
-- (treatment='accept' is the slice-002 default) without these fields are
-- ALSO accommodated by the CHECK constraints below, which only fire when the
-- row genuinely declares the relevant treatment.

ALTER TABLE risks ADD COLUMN accepted_until DATE NULL;
ALTER TABLE risks ADD COLUMN accepter TEXT NOT NULL DEFAULT '';
ALTER TABLE risks ADD COLUMN instrument_reference TEXT NOT NULL DEFAULT '';

-- ===== risk_control_links =====
--
-- Many-to-many between risks and controls (canvas §2.2 erDiagram). The
-- application enforces "treatment='mitigate' => >=1 linked control" at write
-- time because Postgres CHECK constraints cannot reference a sibling table.
-- Composite FK on (tenant_id, control_id) and (tenant_id, risk_id) prevents
-- cross-tenant link leakage (same D3-style pattern as evidence_records).

CREATE TABLE risk_control_links (
    risk_id     UUID NOT NULL,
    control_id  UUID NOT NULL,
    tenant_id   UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, risk_id, control_id),
    FOREIGN KEY (tenant_id, control_id) REFERENCES controls(tenant_id, id) ON DELETE CASCADE
);

-- Add composite UNIQUE on risks so the link table can FK to it.
ALTER TABLE risks ADD CONSTRAINT risks_tenant_id_unique UNIQUE (tenant_id, id);
ALTER TABLE risk_control_links
    ADD CONSTRAINT risk_control_links_risk_fk
    FOREIGN KEY (tenant_id, risk_id) REFERENCES risks(tenant_id, id) ON DELETE CASCADE;

CREATE INDEX idx_risk_control_links_risk ON risk_control_links (tenant_id, risk_id);
CREATE INDEX idx_risk_control_links_control ON risk_control_links (tenant_id, control_id);

-- ===== CHECK constraints — DB-level treatment invariants =====
--
-- The constraint phrasing is "treatment is X => required fields present", which
-- in SQL becomes "treatment <> X OR required fields present". When treatment
-- has any other value the CHECK is trivially true; this avoids the false
-- positives we'd get with simple non-null asserts on rows where treatment is
-- mitigate or avoid.
--
-- Application layer also validates these on write paths so the API returns a
-- 400 rather than a raw 23514 (check_violation). The DB constraint is a
-- defense-in-depth guard — corrupting evidence through SQL alone is rejected.

ALTER TABLE risks ADD CONSTRAINT risks_accept_fields_required
    CHECK (
        treatment <> 'accept'
        OR (accepted_until IS NOT NULL AND length(accepter) > 0)
    );

ALTER TABLE risks ADD CONSTRAINT risks_transfer_fields_required
    CHECK (
        treatment <> 'transfer'
        OR length(instrument_reference) > 0
    );

-- ===== Indexes for filter-path queries =====

CREATE INDEX idx_risks_tenant_treatment ON risks (tenant_id, treatment);
CREATE INDEX idx_risks_tenant_category ON risks (tenant_id, category);
CREATE INDEX idx_risks_tenant_methodology ON risks (tenant_id, methodology);

-- ===== RLS policy split: read / write / update / delete =====
--
-- Slice 002 only declared `tenant_isolation USING (current_tenant_matches(...))`
-- which works for SELECT but is loose for INSERT/UPDATE because Postgres
-- defaults to using the USING expression as WITH CHECK when none is declared.
-- That worked in slice 002 because nothing wrote to risks. Slice 019 introduces
-- writes; we tighten with explicit policies (matches slice 014's pattern).

DROP POLICY tenant_isolation ON risks;

CREATE POLICY tenant_read ON risks
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON risks
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON risks
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON risks
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- Same policy split for the join table.
ALTER TABLE risk_control_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_control_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON risk_control_links
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON risk_control_links
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON risk_control_links
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON risk_control_links
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON risk_control_links TO atlas_app;
