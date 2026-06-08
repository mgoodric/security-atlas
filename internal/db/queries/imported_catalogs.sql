-- Slice 492: OSCAL catalog-import queries.
--
-- CRUD against the three new tables (imported_catalogs,
-- imported_catalog_controls, imported_catalog_audit_log). Every query is
-- tenant-bound via the leading $1 parameter (defense-in-depth behind RLS).
-- The importer (internal/oscal/catalogimport) runs these inside ONE
-- transaction under app.current_tenant so the import is atomic (AC-5).

-- name: InsertImportedCatalog :one
-- Create one imported-catalog provenance row. source defaults to
-- 'oscal-import' (the table default) and is not set here.
INSERT INTO imported_catalogs
    (id, tenant_id, imported_by, source_sha256, source_label, oscal_version, catalog_title, control_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: InsertImportedCatalogControl :one
-- Append one imported control mapped (or flagged NULL for mapping) to an
-- SCF anchor. The (imported_catalog_id, source_control_id) UNIQUE
-- constraint rejects a duplicate control within one catalog.
INSERT INTO imported_catalog_controls
    (id, tenant_id, imported_catalog_id, source_control_id, title, statement, group_path, scf_anchor_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetImportedCatalogByID :one
-- Fetch one imported catalog by id. RLS scopes to the caller's tenant; a
-- cross-tenant id returns ErrNoRows.
SELECT * FROM imported_catalogs
WHERE tenant_id = $1 AND id = $2;

-- name: ListImportedCatalogs :many
-- Enumerate every imported catalog for the tenant, most recent first.
SELECT * FROM imported_catalogs
WHERE tenant_id = $1
ORDER BY imported_at DESC, id ASC;

-- name: ListImportedCatalogControls :many
-- Every control for one imported catalog, ordered for stable rendering.
SELECT * FROM imported_catalog_controls
WHERE tenant_id = $1 AND imported_catalog_id = $2
ORDER BY group_path ASC, source_control_id ASC;

-- name: InsertImportedCatalogAuditLog :one
-- Append one append-only import audit row (AC-7). Written on success
-- ('catalog_imported' / 'profile_imported') and on rejection
-- ('import_rejected' / 'profile_import_rejected').
INSERT INTO imported_catalog_audit_log
    (id, tenant_id, catalog_id, action, actor, source_sha256, source_label, control_count, detail)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- ===== slice 511: profile import (resolve direction) =====

-- name: InsertImportedProfile :one
-- Create one imported-PROFILE provenance row: source 'oscal-profile-import',
-- kind 'profile', carrying the resolved profile's declared title. The
-- resolved baseline is, structurally, an imported control set distinguished
-- from a catalog import by (source, kind) — slice-511 D4.
INSERT INTO imported_catalogs
    (id, tenant_id, source, kind, imported_by, source_sha256, source_label,
     oscal_version, catalog_title, profile_title, control_count)
VALUES ($1, $2, 'oscal-profile-import', 'profile', $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListImportedProfiles :many
-- Enumerate every resolved profile baseline for the tenant, most recent
-- first (index-served by idx_imported_catalogs_tenant_profiles).
SELECT * FROM imported_catalogs
WHERE tenant_id = $1 AND kind = 'profile'
ORDER BY imported_at DESC, id ASC;

-- ===== slice 599: resolved-chain provenance read =====

-- name: GetProfileImportProvenance :one
-- Read the resolved-chain provenance for one imported PROFILE baseline. The
-- chain (the ordered {role, sha256, bytes} array slice 578 records) plus
-- chain_depth live in the `profile_imported` success-audit row's detail JSONB,
-- keyed by catalog_id = the baseline's imported_catalogs.id. The join to
-- imported_catalogs both confirms the id is a PROFILE baseline (kind =
-- 'profile') and carries the baseline's display metadata for the read surface.
-- RLS scopes both tables to the caller's tenant; the leading $1 tenant_id
-- predicate is defense-in-depth behind RLS. A cross-tenant or non-profile id,
-- or a baseline with no success-audit row, returns ErrNoRows.
SELECT
    ic.id              AS baseline_id,
    ic.profile_title   AS profile_title,
    ic.source_label    AS source_label,
    ic.oscal_version   AS oscal_version,
    ic.imported_at     AS imported_at,
    al.source_sha256   AS source_sha256,
    al.occurred_at     AS occurred_at,
    al.detail          AS detail
FROM imported_catalogs ic
JOIN imported_catalog_audit_log al
    ON al.tenant_id = ic.tenant_id
   AND al.catalog_id = ic.id
   AND al.action = 'profile_imported'
WHERE ic.tenant_id = $1
  AND ic.id = $2
  AND ic.kind = 'profile'
ORDER BY al.occurred_at DESC
LIMIT 1;

-- ===== slice 512: component-definition import (vendor-claim ingest) =====

-- name: InsertImportedComponentDefinition :one
-- Create one imported-COMPONENT-DEFINITION provenance row: source
-- 'oscal-component-import', kind 'component_definition'. The vendor/product
-- label rides in source_label and the document title in catalog_title; the
-- per-component + per-claim rows live in imported_components +
-- imported_component_claims (slice-512 D1/D2). control_count carries the
-- TOTAL vendor-claim count across all components (for provenance display).
INSERT INTO imported_catalogs
    (id, tenant_id, source, kind, imported_by, source_sha256, source_label,
     oscal_version, catalog_title, control_count)
VALUES ($1, $2, 'oscal-component-import', 'component_definition', $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListImportedComponentDefinitions :many
-- Enumerate every imported component-definition for the tenant, most recent
-- first (index-served by idx_imported_catalogs_tenant_components).
SELECT * FROM imported_catalogs
WHERE tenant_id = $1 AND kind = 'component_definition'
ORDER BY imported_at DESC, id ASC;

-- name: InsertImportedComponent :one
-- Append one defined-component for an imported component-definition. The
-- (imported_catalog_id, component_uuid) UNIQUE constraint rejects a duplicate
-- component within one import.
INSERT INTO imported_components
    (id, tenant_id, imported_catalog_id, component_uuid, component_type, title, description)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: InsertImportedComponentClaim :one
-- Append one vendor-attributed CLAIM (an implemented-requirement) mapped (or
-- flagged NULL for mapping) to an SCF anchor. is_vendor_claim defaults TRUE
-- and claim_status defaults 'asserted' (the table defaults) — the import never
-- writes anything else (P0-512-1).
INSERT INTO imported_component_claims
    (id, tenant_id, imported_component_id, control_id, statement, requirement_uuid, scf_anchor_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListImportedComponentsForDefinition :many
-- Every defined-component for one imported component-definition, ordered for
-- stable rendering.
SELECT * FROM imported_components
WHERE tenant_id = $1 AND imported_catalog_id = $2
ORDER BY title ASC, component_uuid ASC;

-- name: ListImportedComponentClaims :many
-- Every vendor claim for one imported component, ordered for stable
-- rendering.
SELECT * FROM imported_component_claims
WHERE tenant_id = $1 AND imported_component_id = $2
ORDER BY control_id ASC, requirement_uuid ASC;

-- ===== slice 589: vendor-claim read + operator disposition =====

-- name: GetImportedComponentDefinitionByID :one
-- Fetch one imported component-definition provenance row by id, confirming it
-- is a component-definition (kind = 'component_definition'). RLS scopes to the
-- caller's tenant; a cross-tenant or non-component-definition id returns
-- ErrNoRows.
SELECT * FROM imported_catalogs
WHERE tenant_id = $1 AND id = $2 AND kind = 'component_definition';

-- name: ListImportedComponentClaimsForDefinition :many
-- Every vendor claim across every component of one imported
-- component-definition, joined to the component for display. Ordered for
-- stable rendering. RLS scopes both tables to the caller's tenant; the
-- leading $1 tenant_id predicate is defense-in-depth behind RLS.
SELECT
    cc.id                    AS claim_id,
    cc.imported_component_id AS imported_component_id,
    cmp.component_uuid       AS component_uuid,
    cmp.title                AS component_title,
    cmp.component_type       AS component_type,
    cc.control_id            AS control_id,
    cc.statement             AS statement,
    cc.requirement_uuid      AS requirement_uuid,
    cc.scf_anchor_id         AS scf_anchor_id,
    cc.is_vendor_claim       AS is_vendor_claim,
    cc.claim_status          AS claim_status,
    cc.dispositioned_by      AS dispositioned_by,
    cc.dispositioned_at      AS dispositioned_at,
    cc.disposition_note      AS disposition_note,
    cc.created_at            AS created_at
FROM imported_component_claims cc
JOIN imported_components cmp
    ON cmp.id = cc.imported_component_id
   AND cmp.tenant_id = cc.tenant_id
WHERE cc.tenant_id = $1
  AND cmp.imported_catalog_id = $2
ORDER BY cmp.title ASC, cmp.component_uuid ASC, cc.control_id ASC, cc.requirement_uuid ASC;

-- name: GetImportedComponentClaimByID :one
-- Fetch one vendor claim by id (for disposition pre-read: existence + the
-- current claim_status drives the from_status audit field). RLS scopes to the
-- caller's tenant; a cross-tenant id returns ErrNoRows.
SELECT * FROM imported_component_claims
WHERE tenant_id = $1 AND id = $2;

-- name: DispositionImportedComponentClaim :one
-- Record an operator disposition on one vendor claim: set claim_status to one
-- of 'accepted' / 'rejected' / 'needs_info', the disposing actor, the time,
-- and an optional note. is_vendor_claim is NOT touched (a claim is always a
-- claim — P0-512-1 / P0-589). This NEVER writes to control_evaluations: the
-- disposition is metadata on the claim, not a control satisfaction
-- (invariant #2). RLS rides the slice-512 tenant_update policy.
UPDATE imported_component_claims
SET claim_status      = $3,
    dispositioned_by  = $4,
    dispositioned_at  = now(),
    disposition_note  = $5
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: InsertImportedComponentClaimDisposition :one
-- Append one append-only disposition-audit row recording the
-- from_status -> to_status transition, the actor, and an optional note.
INSERT INTO imported_component_claim_dispositions
    (id, tenant_id, claim_id, from_status, to_status, actor, note)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListImportedComponentClaimDispositions :many
-- The append-only disposition history for one vendor claim, most recent
-- first.
SELECT * FROM imported_component_claim_dispositions
WHERE tenant_id = $1 AND claim_id = $2
ORDER BY occurred_at DESC, id ASC;

-- ===== slice 620: operator maps an unmapped claim to an SCF anchor =====

-- name: MapImportedComponentClaimScfAnchor :one
-- Set one vendor claim's scf_anchor_id (the human-approved crosswalk). This
-- maps a claim's target requirement TO an SCF anchor's scf_id (requirement ->
-- SCF anchor only, invariant #7) — it is NOT a claim -> claim mapping and does
-- NOT touch control_evaluations or the evidence ledger (the claim stays a
-- claim; mapping only sets the crosswalk — invariant #2 / P0-512-1).
-- is_vendor_claim + claim_status are NOT touched. RLS rides the slice-512
-- tenant_update policy.
UPDATE imported_component_claims
SET scf_anchor_id = $3
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: InsertImportedComponentClaimScfMapping :one
-- Append one append-only mapping-audit row recording the
-- from_scf_anchor_id -> to_scf_anchor_id transition, the actor, and an
-- optional note. REUSES the slice-589 imported_component_claim_dispositions
-- table generalized into a claim EVENT log (event_kind='scf_mapping'); the
-- status columns are '' sentinels for a mapping event (the slice-620
-- iccd_to_status_chk only constrains them for 'disposition' events).
INSERT INTO imported_component_claim_dispositions
    (id, tenant_id, claim_id, event_kind, from_status, to_status,
     from_scf_anchor_id, to_scf_anchor_id, actor, note)
VALUES ($1, $2, $3, 'scf_mapping', '', '', $4, $5, $6, $7)
RETURNING *;
