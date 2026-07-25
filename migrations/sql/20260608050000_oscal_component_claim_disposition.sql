-- migrations/sql/20260608050000_oscal_component_claim_disposition.sql
--
-- Slice 589 — operator accept/reject/needs-info disposition over the
-- slice-512 vendor component-claims (imported_component_claims).
--
-- Slice 512 lands a vendor's component-definition as VENDOR-ATTRIBUTED
-- CLAIMS (imported_component_claims, is_vendor_claim=TRUE,
-- claim_status='asserted'). The import deliberately stops at 'asserted': it
-- never auto-accepts a claim (P0-512-1 — no fabricated coverage). This slice
-- adds the *operator* disposition: a human reviews a claim and records
-- accept / reject / needs-info. The disposition is METADATA ON THE CLAIM —
-- it credits (or declines to credit) the vendor's assertion. It NEVER writes
-- to control_evaluations and NEVER marks a control satisfied: a vendor claim
-- is an assertion, not platform-verified evidence (canvas invariant #2,
-- P0-512-1, P0-589 anti-criteria). The is_vendor_claim=TRUE CHECK from
-- slice 512 is left UNTOUCHED — a claim is always a claim.
--
-- Two changes:
--
--   1. Extend imported_component_claims:
--        * widen the claim_status CHECK to add 'needs_info' (the third
--          disposition target; 'accepted'/'rejected' already permitted by
--          slice 512). The import still only ever writes 'asserted'; the
--          three non-'asserted' values are operator-only.
--        * add nullable disposition metadata columns
--          (dispositioned_by, dispositioned_at, disposition_note). They are
--          NULL on an un-dispositioned ('asserted') claim and populated by
--          the operator action. is_vendor_claim is NOT touched.
--
--      NOT-NULL discipline: the new columns are NULLABLE (an 'asserted' claim
--      has no disposition yet), so this ALTER adds no NOT-NULL column to a
--      populated table — no integration-test fixture helper needs patching.
--
--   2. New append-only audit table imported_component_claim_dispositions:
--        one row per disposition event (the slice-021 exception_audit_log
--        precedent: append-only, never UPDATEd/DELETEd). It records the
--        from_status -> to_status transition, the actor, an optional note,
--        and the time. This is the forensic trail for "who credited this
--        vendor claim, when, and why" — the diligence-the-diligence-tool
--        story for inbound vendor claims.
--
-- Constitutional invariants honored:
--   #2  Ingestion / evaluation separation. The disposition writes ONLY the
--       claim's disposition metadata + the append-only event log; it never
--       touches control_evaluations or evidence_records. Accepting a claim
--       does not manufacture a passing evaluation.
--   #6  Tenant isolation at the DB layer. The new table uses the four-policy
--       split under FORCE ROW LEVEL SECURITY (the slice-512 precedent); the
--       claim UPDATE rides the existing slice-512 tenant_update policy.
--
-- Reversibility: paired .down.sql drops the audit table + the new columns +
-- restores the slice-512 three-value claim_status CHECK.

-- ---- 1. extend imported_component_claims ----

ALTER TABLE imported_component_claims
    DROP CONSTRAINT imported_component_claims_status_chk;
ALTER TABLE imported_component_claims
    ADD CONSTRAINT imported_component_claims_status_chk
        CHECK (claim_status IN ('asserted', 'accepted', 'rejected', 'needs_info'));

ALTER TABLE imported_component_claims
    ADD COLUMN dispositioned_by   TEXT NULL,
    ADD COLUMN dispositioned_at   TIMESTAMPTZ NULL,
    ADD COLUMN disposition_note   TEXT NOT NULL DEFAULT '';

-- ---- 2. imported_component_claim_dispositions (append-only audit) ----

CREATE TABLE imported_component_claim_dispositions (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL,
    claim_id      UUID NOT NULL
        REFERENCES imported_component_claims (id) ON DELETE CASCADE,
    -- The claim's status BEFORE this event (e.g. 'asserted', or a prior
    -- disposition being changed) and AFTER it.
    from_status   TEXT NOT NULL,
    to_status     TEXT NOT NULL,
    -- The disposing operator's credential id.
    actor         TEXT NOT NULL,
    -- Optional free-form operator note (the "why").
    note          TEXT NOT NULL DEFAULT '',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT iccd_actor_nonempty
        CHECK (length(actor) > 0),
    CONSTRAINT iccd_to_status_chk
        CHECK (to_status IN ('accepted', 'rejected', 'needs_info'))
);

CREATE INDEX idx_iccd_tenant_claim
    ON imported_component_claim_dispositions (tenant_id, claim_id, occurred_at DESC);

ALTER TABLE imported_component_claim_dispositions ENABLE ROW LEVEL SECURITY;
ALTER TABLE imported_component_claim_dispositions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON imported_component_claim_dispositions
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON imported_component_claim_dispositions
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON imported_component_claim_dispositions TO atlas_app;
