// Package componentimport ingests an inbound OSCAL component-definition JSON
// document and persists the vendor's control-implementation claims as
// provenance-labeled, vendor-attributed CLAIMS reconciled to the SCF spine
// (slice 512 — the vendor-claim ingest direction of constitutional
// invariant #8; the inbound complement to the platform's own SSP export).
//
// A component-definition is the VENDOR-side artifact: a software / service
// vendor ships it to describe how their product implements specific controls
// (per defined-component, a list of implemented-requirements, each targeting
// a control id with an implementation statement). The customer ingests it as
// control-implementation EVIDENCE — "Vendor X asserts their product
// satisfies AC-2 / SCF:IAC-01, here is their statement."
//
// The load-bearing boundary (P0-512-1 / threat-model E — elevation): a
// vendor's implemented-requirement is an ASSERTION, not platform-verified
// evidence. This package persists each one as a vendor-attributed CLAIM
// (with the vendor label + the source document hash); it does NOT
// auto-satisfy a control, does NOT mark anything active, and does NOT
// fabricate coverage. The persistence shape carries no satisfied/active
// boolean an import could flip (the claim row is CHECK-pinned
// is_vendor_claim = TRUE, claim_status = 'asserted'); surfacing a claim as
// "satisfied" requires the EXISTING operator action — never automatic. This
// keeps the import inside the CLAUDE.md fabricate-coverage boundary even
// though no LLM is involved.
//
// This package:
//
//  1. Validates the caller's role (grc_engineer / admin) and the inbound
//     bytes (size cap) on the Go side BEFORE the bytes cross the bridge.
//  2. Calls the Python oscal-bridge `ImportComponentDefinition` RPC, which
//     deserializes + validates the document via compliance-trestle and
//     returns the normalized component + vendor-claim projection. The bridge
//     NEVER dereferences any `href` the document references (P0-512-2).
//  3. Persists the components + their vendor claims TRANSACTIONALLY (AC-5 /
//     P0-512-4): a validation failure commits nothing. Each claim maps its
//     target requirement -> SCF anchor only (invariant #7 / P0-512-3),
//     reusing the slice-492 deterministic crosswalk.
//  4. Writes an append-only audit row (AC-8 / threat-model R).
//
// The import-run provenance row shares the slice-492 imported_catalogs table
// distinguished by (source = 'oscal-component-import', kind =
// 'component_definition') — slice-512 D1; the components + claims live in the
// two new sibling tables imported_components + imported_component_claims
// (slice-512 D2). The bundled SCF spine (scf_anchors) is never touched
// (P0-512-5).
package componentimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// MaxComponentDefBytes bounds the inbound component-definition document on
// the Go side, BEFORE the bytes ever cross the bridge (defense-in-depth with
// the bridge's own cap). It matches the slice-492 catalog cap (16 MiB).
const MaxComponentDefBytes = 16 * 1024 * 1024

// Sentinel errors the CLI maps to operator-facing messages.
var (
	// ErrUnauthorizedRole is returned when the caller's role is not an
	// import-author role (AC-6 / P0-512-6).
	ErrUnauthorizedRole = errors.New("componentimport: caller is not authorized to import component-definitions (requires grc_engineer or admin)")
	// ErrEmptyDocument is returned for a zero-byte document.
	ErrEmptyDocument = errors.New("componentimport: component-definition document is empty")
	// ErrDocumentTooLarge is returned when the document exceeds the byte cap.
	ErrDocumentTooLarge = errors.New("componentimport: component-definition document exceeds the import size cap")
	// ErrMissingImporter is returned when imported_by is empty (provenance is
	// mandatory — AC-4).
	ErrMissingImporter = errors.New("componentimport: imported_by is required")
	// ErrValidationFailed wraps the bridge's structured errors for a document
	// that failed OSCAL v1.1.x validation. Nothing is persisted (AC-5).
	ErrValidationFailed = errors.New("componentimport: component-definition failed OSCAL v1.1.x validation")
)

// authorizedRole reports whether role may import component-definitions. admin
// is the superset of grc_engineer in the canvas 5-role model.
func authorizedRole(role authz.Role) bool {
	return role == authz.RoleGRCEngineer || role == authz.RoleAdmin
}

// Bridge is the subset of the oscal bridge the importer needs. Declared here
// (not imported from internal/oscal) so the importer can be unit tested with
// a fake and to avoid a dependency cycle.
type Bridge interface {
	ImportComponentDefinition(ctx context.Context, oscalJSON []byte, sourceLabel string) (*oscalv1.ImportComponentDefinitionResponse, error)
}

// txBeginner is the minimum pool surface the importer needs (a *pgxpool.Pool
// satisfies it). Declared as an interface so tests can inject.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Importer validates + persists imported OSCAL component-definitions against
// a Postgres pool under the caller's tenant context, calling the bridge to
// deserialize + validate.
type Importer struct {
	pool   txBeginner
	bridge Bridge
}

// NewImporter constructs an Importer.
func NewImporter(pool txBeginner, bridge Bridge) *Importer {
	return &Importer{pool: pool, bridge: bridge}
}

// Request is one component-definition-import invocation.
type Request struct {
	// OscalJSON is the raw inbound OSCAL component-definition document bytes.
	OscalJSON []byte
	// SourceLabel is the operator-declared vendor / product label (provenance).
	SourceLabel string
	// ImportedBy is the operator/credential performing the import (AC-4).
	ImportedBy string
	// Role is the caller's role; must be an import-author role (AC-6).
	Role authz.Role
}

// Report summarizes a successful component-definition import.
type Report struct {
	ImportID       uuid.UUID
	SourceSha256   string
	OSCALVersion   string
	Title          string
	SourceLabel    string
	ComponentCount int
	// ClaimCount is the total number of vendor claims persisted across all
	// components.
	ClaimCount int
	// MappedCount is how many vendor claims' target controls matched an SCF
	// anchor deterministically (the rest are flagged NULL for operator
	// mapping).
	MappedCount int
}

// Import runs the full pipeline. On any validation or persistence failure it
// commits nothing (AC-5 / P0-512-4) but DOES record a
// 'component_definition_import_rejected' audit row in a separate committed
// transaction (AC-8).
func (im *Importer) Import(ctx context.Context, req Request) (Report, error) {
	// --- Go-side gates (run before the bytes touch the bridge) ---
	if !authorizedRole(req.Role) {
		return Report{}, ErrUnauthorizedRole
	}
	if req.ImportedBy == "" {
		return Report{}, ErrMissingImporter
	}
	if len(req.OscalJSON) == 0 {
		return Report{}, ErrEmptyDocument
	}
	if len(req.OscalJSON) > MaxComponentDefBytes {
		return Report{}, fmt.Errorf("%w (%d bytes > %d)", ErrDocumentTooLarge, len(req.OscalJSON), MaxComponentDefBytes)
	}

	// Provenance hash is over the exact inbound bytes (tamper-evident —
	// threat-model S/T).
	sum := sha256.Sum256(req.OscalJSON)
	sourceSha := hex.EncodeToString(sum[:])

	// --- Bridge deserialize + validate ---
	resp, err := im.bridge.ImportComponentDefinition(ctx, req.OscalJSON, req.SourceLabel)
	if err != nil {
		return Report{}, fmt.Errorf("componentimport: bridge ImportComponentDefinition: %w", err)
	}
	if !resp.GetValid() {
		// AC-5: nothing persisted. Record the rejection (AC-8), then surface
		// the structured validation errors.
		_ = im.writeAuditRejected(ctx, req, sourceSha, resp.GetErrors())
		return Report{}, fmt.Errorf("%w: %v", ErrValidationFailed, resp.GetErrors())
	}

	// --- Transactional persistence (AC-5) ---
	report, err := im.persist(ctx, req, sourceSha, resp)
	if err != nil {
		// A persistence failure rolls everything back; record rejection.
		_ = im.writeAuditRejected(ctx, req, sourceSha, []string{err.Error()})
		return Report{}, err
	}
	return report, nil
}

// persist writes the import provenance row + every component + every vendor
// claim inside ONE transaction under app.current_tenant. RLS scopes every
// write to the caller's tenant (P0-512-6 / AC-12). The success audit row is
// written in the SAME transaction so the success record is atomic with the
// import.
func (im *Importer) persist(ctx context.Context, req Request, sourceSha string, resp *oscalv1.ImportComponentDefinitionResponse) (Report, error) {
	tx, err := im.pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("componentimport: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return Report{}, fmt.Errorf("componentimport: apply tenant: %w", err)
	}
	q := dbx.New(tx)
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return Report{}, err
	}

	components := resp.GetComponents()
	totalClaims := 0
	for _, c := range components {
		totalClaims += len(c.GetClaims())
	}

	importID := uuid.New()
	if _, err := q.InsertImportedComponentDefinition(ctx, dbx.InsertImportedComponentDefinitionParams{
		ID:           pgUUID(importID),
		TenantID:     pgUUID(tenantID),
		ImportedBy:   req.ImportedBy,
		SourceSha256: sourceSha,
		SourceLabel:  req.SourceLabel,
		OscalVersion: resp.GetOscalVersion(),
		// catalog_title carries the component-definition's declared title.
		CatalogTitle: resp.GetComponentDefinitionTitle(),
		ControlCount: int32(totalClaims),
	}); err != nil {
		return Report{}, fmt.Errorf("componentimport: insert import row: %w", err)
	}

	// Pre-load the SCF anchor scf_ids for the current SCF version so the
	// deterministic requirement -> SCF-anchor mapping (slice-512 D3, reusing
	// slice-492's crosswalk) is a single query, not one per claim. scf_anchors
	// is the read-only bundled spine — never written (P0-512-5).
	anchorIDs, err := loadSCFAnchorIDs(ctx, tx)
	if err != nil {
		return Report{}, err
	}

	mapped := 0
	for _, comp := range components {
		componentID := uuid.New()
		if _, err := q.InsertImportedComponent(ctx, dbx.InsertImportedComponentParams{
			ID:                pgUUID(componentID),
			TenantID:          pgUUID(tenantID),
			ImportedCatalogID: pgUUID(importID),
			ComponentUuid:     comp.GetComponentUuid(),
			ComponentType:     comp.GetComponentType(),
			Title:             comp.GetTitle(),
			Description:       comp.GetDescription(),
		}); err != nil {
			return Report{}, fmt.Errorf("componentimport: insert component %q: %w", comp.GetComponentUuid(), err)
		}
		for _, claim := range comp.GetClaims() {
			var scfAnchor *string
			if _, ok := anchorIDs[claim.GetControlId()]; ok {
				id := claim.GetControlId()
				scfAnchor = &id
				mapped++
			}
			if _, err := q.InsertImportedComponentClaim(ctx, dbx.InsertImportedComponentClaimParams{
				ID:                  pgUUID(uuid.New()),
				TenantID:            pgUUID(tenantID),
				ImportedComponentID: pgUUID(componentID),
				ControlID:           claim.GetControlId(),
				Statement:           claim.GetStatement(),
				RequirementUuid:     claim.GetRequirementUuid(),
				ScfAnchorID:         scfAnchor,
			}); err != nil {
				return Report{}, fmt.Errorf("componentimport: insert claim %q: %w", claim.GetControlId(), err)
			}
		}
	}

	// Success audit row, atomic with the import (AC-8).
	detail, _ := json.Marshal(map[string]any{
		"components": len(components),
		"claims":     totalClaims,
		"mapped":     mapped,
		"unmapped":   totalClaims - mapped,
		"kind":       "component_definition",
	})
	if _, err := q.InsertImportedCatalogAuditLog(ctx, dbx.InsertImportedCatalogAuditLogParams{
		ID:           pgUUID(uuid.New()),
		TenantID:     pgUUID(tenantID),
		CatalogID:    pgUUID(importID),
		Action:       "component_definition_imported",
		Actor:        req.ImportedBy,
		SourceSha256: sourceSha,
		SourceLabel:  req.SourceLabel,
		ControlCount: int32(totalClaims),
		Detail:       detail,
	}); err != nil {
		return Report{}, fmt.Errorf("componentimport: insert audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("componentimport: commit: %w", err)
	}
	return Report{
		ImportID:       importID,
		SourceSha256:   sourceSha,
		OSCALVersion:   resp.GetOscalVersion(),
		Title:          resp.GetComponentDefinitionTitle(),
		SourceLabel:    req.SourceLabel,
		ComponentCount: len(components),
		ClaimCount:     totalClaims,
		MappedCount:    mapped,
	}, nil
}

// writeAuditRejected records a 'component_definition_import_rejected' row in
// its OWN committed transaction so the rejection is durable while the import
// persistence (a separate, rolled-back transaction) committed nothing
// (AC-5 + AC-8).
func (im *Importer) writeAuditRejected(ctx context.Context, req Request, sourceSha string, errs []string) error {
	tx, err := im.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"errors": errs, "kind": "component_definition"})
	q := dbx.New(tx)
	if _, err := q.InsertImportedCatalogAuditLog(ctx, dbx.InsertImportedCatalogAuditLogParams{
		ID:           pgUUID(uuid.New()),
		TenantID:     pgUUID(tenantID),
		CatalogID:    pgtype.UUID{}, // NULL — no import was committed.
		Action:       "component_definition_import_rejected",
		Actor:        req.ImportedBy,
		SourceSha256: sourceSha,
		SourceLabel:  req.SourceLabel,
		ControlCount: 0,
		Detail:       detail,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// loadSCFAnchorIDs returns the set of scf_id strings in the current SCF
// framework version — the deterministic crosswalk surface (slice-512 D3 /
// slice-492 D1). A vendor claim whose target OSCAL control-id matches a key
// maps TO that SCF anchor. scf_anchors is the read-only bundled spine —
// never written.
func loadSCFAnchorIDs(ctx context.Context, tx pgx.Tx) (map[string]struct{}, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.scf_id
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND fv.status = 'current'`)
	if err != nil {
		return nil, fmt.Errorf("componentimport: load scf anchors: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var scfID string
		if err := rows.Scan(&scfID); err != nil {
			return nil, fmt.Errorf("componentimport: scan scf anchor: %w", err)
		}
		out[scfID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("componentimport: iterate scf anchors: %w", err)
	}
	return out, nil
}

func tenantUUID(ctx context.Context) (uuid.UUID, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("componentimport: %w", err)
	}
	id, err := uuid.Parse(tenant)
	if err != nil {
		return uuid.Nil, fmt.Errorf("componentimport: tenant id is not a UUID: %w", err)
	}
	return id, nil
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
