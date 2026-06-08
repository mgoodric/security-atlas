-- migrations/sql/20260608010000_oscal_component_definitions.sql
--
-- Slice 512 — OSCAL component-definition import (the vendor-claim ingest
-- direction of invariant #8; the inbound complement to the platform's own
-- SSP export).
--
-- A component-definition is structurally DIFFERENT from a catalog/profile.
-- A catalog/profile resolves to a control SET; a component-definition is a
-- vendor's set of CLAIMS: per defined-component, a list of
-- implemented-requirements each asserting "this product implements control
-- X this way". These are vendor ASSERTIONS, not platform-verified evidence,
-- and they must never auto-satisfy a control (P0-512-1 / threat-model E —
-- the dominant invariant for this model). So slice 512 does NOT reuse the
-- imported_catalog_controls table (a control-set shape); it adds two NEW
-- sibling tables modelling the component → vendor-claim hierarchy, while
-- REUSING the slice-492 imported_catalogs provenance row + the shared
-- append-only audit log (extended via the slice-511 `kind` discriminator).
-- See slice-512 decisions-log D1/D2.
--
-- Reused / extended (slice 492 + 511 tables):
--   * imported_catalogs.kind         — extend the CHECK to allow
--                                      'component_definition' (alongside the
--                                      slice-511 'catalog' | 'profile'). The
--                                      provenance row carries the source
--                                      ('oscal-component-import'), the vendor
--                                      label (source_label), the source
--                                      SHA-256, and the OSCAL version.
--   * imported_catalogs.source CHECK — extend to allow
--                                      'oscal-component-import'.
--   * imported_catalog_audit_log     — extend the action CHECK to allow
--                                      'component_definition_imported' +
--                                      'component_definition_import_rejected'.
--
-- New tables:
--   imported_components               — one row per defined-component.
--                                       Provenance: component_uuid (the OSCAL
--                                       uuid), component_type, title,
--                                       description. FK -> imported_catalogs
--                                       (the import-run / provenance row).
--   imported_component_claims         — one row per implemented-requirement.
--                                       A VENDOR-ATTRIBUTED CLAIM: the target
--                                       control_id, the vendor's statement,
--                                       the requirement uuid, and a NULLABLE
--                                       scf_anchor_id (requirement -> SCF
--                                       anchor only — invariant #7). The CLAIM
--                                       is distinguishable from a control set
--                                       and from platform-verified evidence by
--                                       table identity + the
--                                       is_vendor_claim/claim_status columns.
--
-- Constitutional invariants honored:
--   #2  Ingestion / evaluation separation. Imported claims are
--       vendor-attributed CLAIMS in their own table; nothing here writes to
--       control_evaluations or marks a control satisfied (P0-512-1 — the
--       schema carries no satisfied/active boolean an importer could flip).
--   #6  Tenant isolation at the DB layer. Both new tables use the four-policy
--       split under FORCE ROW LEVEL SECURITY (the slice-492 precedent).
--   #7  SCF is the canonical control catalog. scf_anchor_id maps a claim's
--       target requirement TO an SCF anchor's scf_id; there is no FK to
--       another imported requirement.
--   #8  OSCAL is the wire format (component-definition ingest direction).
--
-- P0-512-5: the bundled SCF spine (scf_anchors) is untouched — the ALTERs
-- touch only slice-492/511 tenant-scoped tables; the new tables are NEW.
--
-- NOT-NULL discipline: the new tables are NEW (no ALTER adds a NOT-NULL
-- column to an existing populated table), so no integration-test fixture
-- helper requires patching. The kind CHECK is widened (additive), never
-- narrowed.
--
-- Reversibility: paired .down.sql drops the new tables + restores the
-- slice-511 CHECK shapes.

-- ---- extend the kind discriminator + source + audit-action CHECKs ----

ALTER TABLE imported_catalogs
    DROP CONSTRAINT imported_catalogs_kind_chk;
ALTER TABLE imported_catalogs
    ADD CONSTRAINT imported_catalogs_kind_chk
        CHECK (kind IN ('catalog', 'profile', 'component_definition'));

ALTER TABLE imported_catalogs
    DROP CONSTRAINT imported_catalogs_source_chk;
ALTER TABLE imported_catalogs
    ADD CONSTRAINT imported_catalogs_source_chk
        CHECK (source IN ('oscal-import', 'oscal-profile-import', 'oscal-component-import'));

ALTER TABLE imported_catalog_audit_log
    DROP CONSTRAINT imported_catalog_audit_log_action_chk;
ALTER TABLE imported_catalog_audit_log
    ADD CONSTRAINT imported_catalog_audit_log_action_chk
        CHECK (action IN (
            'catalog_imported',
            'import_rejected',
            'profile_imported',
            'profile_import_rejected',
            'component_definition_imported',
            'component_definition_import_rejected'
        ));

-- A partial index over component-definition provenance rows so listing a
-- tenant's imported component-definitions is index-served (most-recent-first)
-- without slowing catalog/profile reads.
CREATE INDEX idx_imported_catalogs_tenant_components
    ON imported_catalogs (tenant_id, imported_at DESC)
    WHERE kind = 'component_definition';

-- ===== imported_components =====
--
-- One row per defined-component in an imported component-definition. The
-- import-run / provenance row is imported_catalogs (kind =
-- 'component_definition'); a component FKs to it and CASCADE-deletes with it.

CREATE TABLE imported_components (
    id                    UUID PRIMARY KEY,
    tenant_id             UUID NOT NULL,
    imported_catalog_id   UUID NOT NULL
        REFERENCES imported_catalogs (id) ON DELETE CASCADE,
    -- The component's OSCAL uuid, exactly as authored (provenance anchor).
    component_uuid        TEXT NOT NULL,
    -- The OSCAL component `type` (e.g. "software", "service", "hardware").
    component_type        TEXT NOT NULL DEFAULT '',
    title                 TEXT NOT NULL DEFAULT '',
    description           TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT imported_components_uuid_nonempty
        CHECK (length(component_uuid) > 0),
    CONSTRAINT imported_components_unique_per_import
        UNIQUE (imported_catalog_id, component_uuid)
);

CREATE INDEX idx_imported_components_tenant_import
    ON imported_components (tenant_id, imported_catalog_id);

ALTER TABLE imported_components ENABLE ROW LEVEL SECURITY;
ALTER TABLE imported_components FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON imported_components
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON imported_components
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON imported_components
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON imported_components
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON imported_components TO atlas_app;

-- ===== imported_component_claims =====
--
-- One row per implemented-requirement: a VENDOR-ATTRIBUTED CLAIM that a
-- component implements a specific control. This is the load-bearing model
-- shape (P0-512-1 / threat-model E): the row is a CLAIM, never
-- platform-verified evidence and never an active control satisfaction.
--
--   * is_vendor_claim is a constant TRUE (a NOT-NULL CHECK-pinned column) so
--     a reader can never mistake a row here for a platform-verified record,
--     and there is NO satisfied/active boolean an importer could flip.
--   * claim_status defaults to 'asserted' (the vendor's raw assertion) and
--     can only become 'accepted' / 'rejected' via the EXISTING operator
--     action (out of scope for this slice) — the import never writes anything
--     other than 'asserted'.
--   * scf_anchor_id is NULLABLE: NULL = "the importer found no deterministic
--     SCF crosswalk for this claim's target control; it needs operator
--     mapping" (the slice-492 import-unmapped-and-flag pattern). The claim is
--     NEVER dropped for being unmappable. Requirement -> SCF anchor only
--     (invariant #7); a free-form scf_id string (the slice-492 precedent).

CREATE TABLE imported_component_claims (
    id                    UUID PRIMARY KEY,
    tenant_id             UUID NOT NULL,
    imported_component_id UUID NOT NULL
        REFERENCES imported_components (id) ON DELETE CASCADE,
    -- The OSCAL control-id this implemented-requirement targets.
    control_id            TEXT NOT NULL,
    -- The vendor's implementation narrative (requirement description +
    -- flattened statement prose).
    statement             TEXT NOT NULL DEFAULT '',
    -- The implemented-requirement's OSCAL uuid (provenance / de-dup anchor).
    requirement_uuid      TEXT NOT NULL DEFAULT '',
    -- Mapping TO the canonical SCF anchor (free-form scf_id). NULL = needs
    -- operator mapping. Requirement -> SCF anchor only (invariant #7).
    scf_anchor_id         TEXT NULL,
    -- Constant TRUE: this row is a vendor CLAIM, never platform-verified.
    is_vendor_claim       BOOLEAN NOT NULL DEFAULT TRUE,
    -- The claim's operator-disposition. The IMPORT only ever writes
    -- 'asserted'; an operator action (existing, out of this slice) can move
    -- it to 'accepted' / 'rejected'. A claim is NEVER 'accepted' at import.
    claim_status          TEXT NOT NULL DEFAULT 'asserted',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT imported_component_claims_control_id_nonempty
        CHECK (length(control_id) > 0),
    -- P0-512-1 schema-level guard: a row in this table is ALWAYS a vendor
    -- claim. There is no path by which an import marks a control satisfied.
    CONSTRAINT imported_component_claims_is_vendor_claim_chk
        CHECK (is_vendor_claim = TRUE),
    CONSTRAINT imported_component_claims_status_chk
        CHECK (claim_status IN ('asserted', 'accepted', 'rejected')),
    CONSTRAINT imported_component_claims_unique_per_component
        UNIQUE (imported_component_id, control_id, requirement_uuid)
);

CREATE INDEX idx_imported_component_claims_tenant_component
    ON imported_component_claims (tenant_id, imported_component_id);
CREATE INDEX idx_imported_component_claims_tenant_anchor
    ON imported_component_claims (tenant_id, scf_anchor_id)
    WHERE scf_anchor_id IS NOT NULL;

ALTER TABLE imported_component_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE imported_component_claims FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON imported_component_claims
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON imported_component_claims
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON imported_component_claims
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON imported_component_claims
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON imported_component_claims TO atlas_app;
