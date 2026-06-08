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
