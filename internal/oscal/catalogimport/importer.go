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
	"github.com/mgoodric/security-atlas/internal/oscal/scfbind"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
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
	// ErrSCFReleaseConflict is returned when --scf-release and the
	// document's "scf-release" prop name different SCF releases, or the
	// document names two. Nothing is persisted.
	ErrSCFReleaseConflict = scfbind.ErrConflict
)

// ErrSCFReleaseRequired is returned for an import that declares no SCF
// release (#1774); the catalog's control ids are never matched in the current release
// instead. The import fails before anything is written, and the message
// is the one-line fix.
var ErrSCFReleaseRequired = fmt.Errorf("catalogimport: %w: no SCF release declared; pass --scf-release <release> (e.g. --scf-release test-2026.3), or add an \"scf-release\" prop to the catalog's metadata", scfrelease.ErrReleaseRequired)

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
	// SCFRelease is the SCF release the control ids are matched in
	// (#2264), from atlas-oscal --scf-release. The catalog can declare it
	// instead with an "scf-release" prop on its metadata; when both are
	// given they must agree. A declared release that is not loaded fails
	// the import. Neither is ErrSCFReleaseRequired (#1774).
	SCFRelease string
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
	// SCFRelease is the SCF release the control ids were matched in.
	SCFRelease string
	// Warnings are non-fatal notes for the operator.
	Warnings []string
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

	release, err := scfbind.Declared("catalogimport", req.SCFRelease, req.OscalJSON, "catalog")
	if err != nil {
		return Report{}, err
	}
	// No release declared fails here, before the bridge round-trip and
	// before anything is written, not even a rejection audit row (#1774).
	if release == "" {
		return Report{}, ErrSCFReleaseRequired
	}

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
	report, err := im.persist(ctx, req, release, sourceSha, resp)
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
func (im *Importer) persist(ctx context.Context, req Request, release, sourceSha string, resp *oscalv1.ImportCatalogResponse) (Report, error) {
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

	// Pre-load the scf_ids of the release the catalog declares (#2264;
	// required since #1774), so the deterministic
	// requirement -> SCF-anchor mapping (D1) is a single query, not one per
	// control. A control whose OSCAL control-id exactly matches a scf_id in
	// that release maps TO that anchor; otherwise NULL.
	b, err := scfbind.Bind(ctx, tx, release, ErrSCFReleaseRequired)
	if err != nil {
		return Report{}, fmt.Errorf("catalogimport: scf release: %w", err)
	}
	anchorIDs := b.Anchors

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
	detail, _ := json.Marshal(map[string]any{"mapped": mapped, "unmapped": len(controls) - mapped, "scf_release": b.Release.Version})
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
		SCFRelease:   b.Release.Version,
		Warnings:     b.Warnings,
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
