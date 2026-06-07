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
-- ('catalog_imported') and on rejection ('import_rejected').
INSERT INTO imported_catalog_audit_log
    (id, tenant_id, catalog_id, action, actor, source_sha256, source_label, control_count, detail)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;
