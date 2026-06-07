// Package catalogimport ingests an inbound OSCAL catalog JSON document
// into a tenant's catalog as a DISTINCT, provenance-labeled set (slice
// 492 — the ingest direction of constitutional invariant #8).
//
// The flow:
//
//  1. Validate the caller's role (grc_engineer / admin) and the inbound
//     bytes (size cap) on the Go side BEFORE the bytes cross the bridge.
//  2. Call the Python oscal-bridge `ImportCatalog` RPC, which deserializes
//     + validates the document against OSCAL v1.1.x via compliance-trestle
//     and returns a normalized control projection. The bridge NEVER
//     dereferences any href the document references (P0-492-2).
//  3. Persist the imported controls TRANSACTIONALLY (AC-5 / P0-492-3): a
//     validation failure or any partial error commits nothing. Each
//     control maps requirement -> SCF anchor only (invariant #7 /
//     P0-492-1) — a deterministic scf_id match populates scf_anchor_id;
//     otherwise it is left NULL ("needs operator mapping", decision D1).
//  4. Write an append-only audit row (AC-7 / threat-model R).
//
// Imported catalogs never touch the bundled SCF spine (`scf_anchors`,
// P0-492-4) — they are tenant-scoped rows that point AT SCF anchors.
package catalogimport

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

// MaxCatalogBytes bounds the inbound document on the Go side, BEFORE the
// bytes ever cross the bridge (defense-in-depth with the bridge's own cap;
// see decisions log D3). 16 MiB ~ 4x a full NIST 800-53 rev5 catalog.
const MaxCatalogBytes = 16 * 1024 * 1024

// Sentinel errors the CLI maps to operator-facing messages.
var (
	// ErrUnauthorizedRole is returned when the caller's role is not a
	// catalog-author role (AC-6 / P0-492-5).
	ErrUnauthorizedRole = errors.New("catalogimport: caller is not authorized to import catalogs (requires grc_engineer or admin)")
	// ErrEmptyDocument is returned for a zero-byte document.
	ErrEmptyDocument = errors.New("catalogimport: catalog document is empty")
	// ErrDocumentTooLarge is returned when the document exceeds MaxCatalogBytes.
	ErrDocumentTooLarge = errors.New("catalogimport: catalog document exceeds the import size cap")
	// ErrMissingImporter is returned when imported_by is empty (provenance
	// is mandatory — AC-4).
	ErrMissingImporter = errors.New("catalogimport: imported_by is required")
	// ErrValidationFailed wraps the bridge's structured validation errors
	// for a malformed / schema-invalid catalog (AC-10). Nothing is persisted.
	ErrValidationFailed = errors.New("catalogimport: catalog failed OSCAL v1.1.x validation")
)

// authorizedRole reports whether role may import catalogs. admin is the
// superset of grc_engineer in the canvas 5-role model.
func authorizedRole(role authz.Role) bool {
	return role == authz.RoleGRCEngineer || role == authz.RoleAdmin
}

// Bridge is the subset of the oscal bridge the importer needs. Declared
// here (not imported from internal/oscal) so the importer can be unit
// tested with a fake and to avoid a dependency cycle.
type Bridge interface {
	ImportCatalog(ctx context.Context, oscalJSON []byte, sourceLabel string) (*oscalv1.ImportCatalogResponse, error)
}

// Importer persists imported OSCAL catalogs against a Postgres pool under
// the caller's tenant context, calling the bridge to validate + normalize.
type Importer struct {
	pool   txBeginner
	bridge Bridge
}

// txBeginner is the minimum pool surface the importer needs (a *pgxpool.Pool
// satisfies it). Declared as an interface so tests can inject.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// NewImporter constructs an Importer.
func NewImporter(pool txBeginner, bridge Bridge) *Importer {
	return &Importer{pool: pool, bridge: bridge}
}

// Request is one catalog-import invocation.
type Request struct {
	// OscalJSON is the raw inbound OSCAL catalog document bytes.
	OscalJSON []byte
	// SourceLabel is the operator-declared framework label (provenance).
	SourceLabel string
	// ImportedBy is the operator/credential performing the import (AC-4).
	ImportedBy string
	// Role is the caller's role; must be a catalog-author role (AC-6).
	Role authz.Role
}

// Report summarizes a successful import.
type Report struct {
	CatalogID    uuid.UUID
	SourceSha256 string
	OSCALVersion string
	CatalogTitle string
	SourceLabel  string
	ControlCount int
	// MappedCount is how many imported controls matched an SCF anchor
	// deterministically (the rest are flagged NULL for operator mapping).
	MappedCount int
}

// Import runs the full pipeline. On any validation or persistence failure
// it commits nothing (AC-5 / P0-492-3) but DOES record an 'import_rejected'
// audit row in a separate committed transaction (AC-7).
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
	if len(req.OscalJSON) > MaxCatalogBytes {
		return Report{}, fmt.Errorf("%w (%d bytes > %d)", ErrDocumentTooLarge, len(req.OscalJSON), MaxCatalogBytes)
	}

	sum := sha256.Sum256(req.OscalJSON)
	sourceSha := hex.EncodeToString(sum[:])

	// --- Bridge validate + normalize ---
	resp, err := im.bridge.ImportCatalog(ctx, req.OscalJSON, req.SourceLabel)
	if err != nil {
		return Report{}, fmt.Errorf("catalogimport: bridge ImportCatalog: %w", err)
	}
	if !resp.GetValid() {
		// AC-10: nothing persisted. Record the rejection (AC-7), then
		// surface the structured validation errors.
		_ = im.writeAuditRejected(ctx, req, sourceSha, resp.GetErrors())
		return Report{}, fmt.Errorf("%w: %v", ErrValidationFailed, resp.GetErrors())
	}

	// --- Transactional persistence (AC-5) ---
	report, err := im.persist(ctx, req, sourceSha, resp)
	if err != nil {
		// A persistence failure rolls the catalog back; record rejection.
		_ = im.writeAuditRejected(ctx, req, sourceSha, []string{err.Error()})
		return Report{}, err
	}
	return report, nil
}

// persist writes the provenance row + every imported control inside ONE
// transaction under app.current_tenant. RLS scopes every write to the
// caller's tenant (P0-492-5 / AC-11). The audit row is written in the SAME
// transaction on success so the success record is atomic with the catalog.
func (im *Importer) persist(ctx context.Context, req Request, sourceSha string, resp *oscalv1.ImportCatalogResponse) (Report, error) {
	tx, err := im.pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("catalogimport: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return Report{}, fmt.Errorf("catalogimport: apply tenant: %w", err)
	}
	q := dbx.New(tx)
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return Report{}, err
	}

	controls := resp.GetControls()
	catalogID := uuid.New()
	if _, err := q.InsertImportedCatalog(ctx, dbx.InsertImportedCatalogParams{
		ID:           pgUUID(catalogID),
		TenantID:     pgUUID(tenantID),
		ImportedBy:   req.ImportedBy,
		SourceSha256: sourceSha,
		SourceLabel:  req.SourceLabel,
		OscalVersion: resp.GetOscalVersion(),
		CatalogTitle: resp.GetCatalogTitle(),
		ControlCount: int32(len(controls)),
	}); err != nil {
		return Report{}, fmt.Errorf("catalogimport: insert catalog: %w", err)
	}

	// Pre-load the set of SCF anchor scf_ids for the current SCF version so
	// the deterministic requirement -> SCF-anchor mapping (D1) is a single
	// query, not one per control. A control whose OSCAL control-id exactly
	// matches a known SCF scf_id maps TO that anchor; otherwise NULL.
	anchorIDs, err := loadSCFAnchorIDs(ctx, tx)
	if err != nil {
		return Report{}, err
	}

	mapped := 0
	for _, c := range controls {
		var scfAnchor *string
		if _, ok := anchorIDs[c.GetControlId()]; ok {
			id := c.GetControlId()
			scfAnchor = &id
			mapped++
		}
		if _, err := q.InsertImportedCatalogControl(ctx, dbx.InsertImportedCatalogControlParams{
			ID:                pgUUID(uuid.New()),
			TenantID:          pgUUID(tenantID),
			ImportedCatalogID: pgUUID(catalogID),
			SourceControlID:   c.GetControlId(),
			Title:             c.GetTitle(),
			Statement:         c.GetStatement(),
			GroupPath:         c.GetGroupPath(),
			ScfAnchorID:       scfAnchor,
		}); err != nil {
			return Report{}, fmt.Errorf("catalogimport: insert control %q: %w", c.GetControlId(), err)
		}
	}

	// Success audit row, atomic with the catalog (AC-7).
	detail, _ := json.Marshal(map[string]any{"mapped": mapped, "unmapped": len(controls) - mapped})
	if _, err := q.InsertImportedCatalogAuditLog(ctx, dbx.InsertImportedCatalogAuditLogParams{
		ID:           pgUUID(uuid.New()),
		TenantID:     pgUUID(tenantID),
		CatalogID:    pgUUID(catalogID),
		Action:       "catalog_imported",
		Actor:        req.ImportedBy,
		SourceSha256: sourceSha,
		SourceLabel:  req.SourceLabel,
		ControlCount: int32(len(controls)),
		Detail:       detail,
	}); err != nil {
		return Report{}, fmt.Errorf("catalogimport: insert audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("catalogimport: commit: %w", err)
	}
	return Report{
		CatalogID:    catalogID,
		SourceSha256: sourceSha,
		OSCALVersion: resp.GetOscalVersion(),
		CatalogTitle: resp.GetCatalogTitle(),
		SourceLabel:  req.SourceLabel,
		ControlCount: len(controls),
		MappedCount:  mapped,
	}, nil
}

// writeAuditRejected records an 'import_rejected' row in its OWN committed
// transaction so the rejection is durable while the catalog persistence (a
// separate, rolled-back transaction) committed nothing (AC-5 + AC-7).
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
	detail, _ := json.Marshal(map[string]any{"errors": errs})
	q := dbx.New(tx)
	if _, err := q.InsertImportedCatalogAuditLog(ctx, dbx.InsertImportedCatalogAuditLogParams{
		ID:           pgUUID(uuid.New()),
		TenantID:     pgUUID(tenantID),
		CatalogID:    pgtype.UUID{}, // NULL — no catalog was committed.
		Action:       "import_rejected",
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
// framework version. The set is the deterministic crosswalk surface for
// D1: an imported control whose OSCAL control-id matches a key maps TO that
// SCF anchor. scf_anchors is the read-only bundled spine — never written.
func loadSCFAnchorIDs(ctx context.Context, tx pgx.Tx) (map[string]struct{}, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.scf_id
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND fv.status = 'current'`)
	if err != nil {
		return nil, fmt.Errorf("catalogimport: load scf anchors: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var scfID string
		if err := rows.Scan(&scfID); err != nil {
			return nil, fmt.Errorf("catalogimport: scan scf anchor: %w", err)
		}
		out[scfID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalogimport: iterate scf anchors: %w", err)
	}
	return out, nil
}

func tenantUUID(ctx context.Context) (uuid.UUID, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("catalogimport: %w", err)
	}
	id, err := uuid.Parse(tenant)
	if err != nil {
		return uuid.Nil, fmt.Errorf("catalogimport: tenant id is not a UUID: %w", err)
	}
	return id, nil
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
