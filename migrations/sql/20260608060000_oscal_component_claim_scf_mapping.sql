-- migrations/sql/20260608060000_oscal_component_claim_scf_mapping.sql
--
-- Slice 620 — operator maps an UNMAPPED vendor claim to a canonical SCF
-- anchor.
--
-- Slice 512 lands vendor claims with a NULLABLE scf_anchor_id: NULL means the
-- importer found no deterministic SCF crosswalk for the claim's target control
-- and it needs operator mapping (the claim is never dropped for being
-- unmappable). Slice 589's vendor-claims view SHOWS the unmapped claims (an
-- "Unmapped to SCF" badge) but provides no affordance to map one. This slice
-- adds the operator mapping: from the vendor-claims view, an operator maps an
-- unmapped claim to a canonical SCF anchor (requirement -> SCF anchor only,
-- invariant #7). The mapping is the human-approved crosswalk; once set, it is
-- canonical for that claim.
--
-- THE LOAD-BEARING BOUNDARY (unchanged): a vendor claim is an ASSERTION, not
-- platform-verified evidence (canvas invariant #2 / P0-512-1). Mapping a claim
-- to an anchor sets the CROSSWALK only — it does NOT manufacture control
-- coverage. The claim stays a claim (is_vendor_claim=TRUE is untouched); the
-- mapping writes ONLY the claim's scf_anchor_id + an append-only audit row,
-- never control_evaluations or the evidence ledger.
--
-- One schema change, plus an audit-row reuse:
--
--   1. The mapping writes to imported_component_claims.scf_anchor_id — the
--      NULLABLE TEXT column ALREADY present from slice 512 (a free-form scf_id
--      like 'IAC-06', the slice-492 precedent). No new column on the claims
--      table; this migration only widens the disposition-audit table so the
--      same append-only log records mapping events alongside disposition
--      events.
--
--   2. GENERALIZE imported_component_claim_dispositions (slice 589) from a
--      disposition-only event log into a claim EVENT log so the mapping audit
--      REUSES the existing table (the slice-589 append-only pattern: never
--      UPDATEd/DELETEd, four-policy-minus-mutation RLS, SELECT+INSERT grant).
--      A disposition transition is status-typed (from_status -> to_status); a
--      mapping transition is anchor-typed (from_scf_anchor_id ->
--      to_scf_anchor_id). The two share the same who/when/why columns (actor,
--      occurred_at, note) and the same append-only semantics, so the table is
--      generalized rather than duplicated:
--        * add event_kind TEXT NOT NULL DEFAULT 'disposition' (the discriminator).
--          Existing rows + the slice-589 disposition INSERT default to
--          'disposition' — no data migration, no fixture churn.
--        * add from_scf_anchor_id / to_scf_anchor_id TEXT NULL (the anchor
--          transition for a 'scf_mapping' event; NULL for a 'disposition'
--          event).
--        * relax iccd_to_status_chk so the status CHECK applies ONLY to
--          'disposition' events. A 'scf_mapping' event carries no status
--          transition (from_status/to_status are '' sentinels for it).
--        * add iccd_scf_mapping_anchor_chk: a 'scf_mapping' event MUST carry a
--          non-empty to_scf_anchor_id (you cannot map to nothing). This is the
--          schema-level guard that the mapping write sets a real crosswalk.
--
-- Constitutional invariants honored:
--   #2  Ingestion / evaluation separation. The mapping writes ONLY the claim's
--       scf_anchor_id + the append-only event row; it never touches
--       control_evaluations or evidence_records. Setting a crosswalk does not
--       manufacture a passing evaluation.
--   #6  Tenant isolation at the DB layer. The claim UPDATE rides the existing
--       slice-512 tenant_update policy; the audit INSERT rides the slice-589
--       tenant_write policy. No new RLS surface.
--   #7  SCF is the canonical control catalog. scf_anchor_id maps a claim's
--       target requirement TO an SCF anchor's scf_id; there is no FK to
--       another imported claim (no claim -> claim mapping).
--
-- NOT-NULL discipline: the one NOT-NULL column added (event_kind) carries a
-- DEFAULT, so the ALTER backfills existing rows and no integration-test
-- fixture helper needs patching. The two anchor columns are NULLABLE.
--
-- Reversibility: paired .down.sql drops the three new columns + restores the
-- slice-589 unconditional iccd_to_status_chk.

ALTER TABLE imported_component_claim_dispositions
    ADD COLUMN event_kind        TEXT NOT NULL DEFAULT 'disposition',
    ADD COLUMN from_scf_anchor_id TEXT NULL,
    ADD COLUMN to_scf_anchor_id   TEXT NULL;

ALTER TABLE imported_component_claim_dispositions
    ADD CONSTRAINT iccd_event_kind_chk
        CHECK (event_kind IN ('disposition', 'scf_mapping'));

-- Relax the slice-589 to_status CHECK so it constrains ONLY disposition
-- events. A 'scf_mapping' event carries no status transition.
ALTER TABLE imported_component_claim_dispositions
    DROP CONSTRAINT iccd_to_status_chk;
ALTER TABLE imported_component_claim_dispositions
    ADD CONSTRAINT iccd_to_status_chk
        CHECK (
            event_kind <> 'disposition'
            OR to_status IN ('accepted', 'rejected', 'needs_info')
        );

-- A mapping event must set a real crosswalk (you cannot map to nothing).
ALTER TABLE imported_component_claim_dispositions
    ADD CONSTRAINT iccd_scf_mapping_anchor_chk
        CHECK (
            event_kind <> 'scf_mapping'
            OR (to_scf_anchor_id IS NOT NULL AND length(to_scf_anchor_id) > 0)
        );
