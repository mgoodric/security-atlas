// Package profileimport resolves an inbound OSCAL profile JSON document
// against one or more supplied catalogs and persists the resolved control
// set as a DISTINCT, provenance-labeled baseline (slice 511 — the resolve
// direction of constitutional invariant #8).
//
// A profile (e.g. a FedRAMP Low/Moderate/High baseline) does not list
// controls directly; it references one or more catalogs and applies
// import / merge / modify directives to produce a tailored control set.
// This package:
//
//  1. Validates the caller's role (grc_engineer / admin) and the inbound
//     bytes (size caps) on the Go side BEFORE the bytes cross the bridge.
//  2. Calls the Python oscal-bridge `ImportProfile` RPC, which resolves the
//     profile via compliance-trestle's profile-resolver inside an ISOLATED
//     trestle workspace and returns the resolved control projection. The
//     bridge NEVER dereferences an external `import.href` (P0-511-1): an
//     href that maps to no supplied catalog is a structured error, not a
//     fetch.
//  3. Persists the resolved baseline TRANSACTIONALLY (AC-5 / P0-511-3): a
//     resolution / validation failure commits nothing. Each resolved
//     control maps requirement -> SCF anchor only (invariant #7 /
//     P0-511-2), reusing the slice-492 deterministic crosswalk.
//  4. Writes an append-only audit row (AC-7 / threat-model R).
//
// The resolved baseline shares the slice-492 imported-catalog tables; it is
// distinguished by (source = 'oscal-profile-import', kind = 'profile') and
// carries the profile's declared title (slice-511 D4). The bundled SCF
// spine (scf_anchors) is never touched (P0-511-4).
package profileimport

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

// MaxProfileBytes bounds the inbound profile document on the Go side, BEFORE
// the bytes ever cross the bridge (defense-in-depth with the bridge's own
// cap). It matches the slice-492 catalog cap (16 MiB).
const MaxProfileBytes = 16 * 1024 * 1024

// MaxCatalogBytes bounds each supplied catalog the profile resolves against
// (same 16 MiB ceiling). A profile typically resolves against one catalog;
// the cap applies per supplied document.
const MaxCatalogBytes = 16 * 1024 * 1024

// MaxSuppliedCatalogs caps how many catalogs the caller may supply for a
// single resolution (threat-model D — bound the resolution working set).
const MaxSuppliedCatalogs = 16

// Sentinel errors the CLI maps to operator-facing messages.
var (
	// ErrUnauthorizedRole is returned when the caller's role is not a
	// catalog-author role (AC-6 / P0-511-5).
	ErrUnauthorizedRole = errors.New("profileimport: caller is not authorized to import profiles (requires grc_engineer or admin)")
	// ErrEmptyDocument is returned for a zero-byte profile document.
	ErrEmptyDocument = errors.New("profileimport: profile document is empty")
	// ErrNoCatalogs is returned when no catalog is supplied to resolve against.
	ErrNoCatalogs = errors.New("profileimport: at least one catalog must be supplied to resolve the profile against")
	// ErrTooManyCatalogs is returned when more than MaxSuppliedCatalogs are supplied.
	ErrTooManyCatalogs = errors.New("profileimport: too many catalogs supplied")
	// ErrDocumentTooLarge is returned when the profile or a supplied catalog
	// exceeds its byte cap.
	ErrDocumentTooLarge = errors.New("profileimport: a document exceeds the import size cap")
	// ErrMissingImporter is returned when imported_by is empty (provenance
	// is mandatory — AC-4).
	ErrMissingImporter = errors.New("profileimport: imported_by is required")
	// ErrResolutionFailed wraps the bridge's structured errors for a profile
	// that failed to resolve / validate, OR an unresolvable / external
	// import.href (P0-511-1). Nothing is persisted (AC-5).
	ErrResolutionFailed = errors.New("profileimport: profile failed to resolve against the supplied catalog(s)")
)

// authorizedRole reports whether role may import profiles. admin is the
// superset of grc_engineer in the canvas 5-role model.
func authorizedRole(role authz.Role) bool {
	return role == authz.RoleGRCEngineer || role == authz.RoleAdmin
}

// Bridge is the subset of the oscal bridge the importer needs. Declared here
// (not imported from internal/oscal) so the importer can be unit tested with
// a fake and to avoid a dependency cycle.
type Bridge interface {
	ImportProfile(ctx context.Context, profileJSON []byte, catalogs [][]byte, sourceLabel string) (*oscalv1.ImportProfileResponse, error)
}

// txBeginner is the minimum pool surface the importer needs (a *pgxpool.Pool
// satisfies it). Declared as an interface so tests can inject.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Importer resolves + persists imported OSCAL profiles against a Postgres
// pool under the caller's tenant context, calling the bridge to resolve +
// validate.
type Importer struct {
	pool   txBeginner
	bridge Bridge
}

// NewImporter constructs an Importer.
func NewImporter(pool txBeginner, bridge Bridge) *Importer {
	return &Importer{pool: pool, bridge: bridge}
}

// Request is one profile-import invocation.
type Request struct {
	// ProfileJSON is the raw inbound OSCAL profile document bytes.
	ProfileJSON []byte
	// Catalogs are the catalog document(s) the profile resolves against. At
	// least one is required. The bridge resolves import.href references ONLY
	// against these (never an external fetch).
	Catalogs [][]byte
	// SourceLabel is the operator-declared baseline label (provenance).
	SourceLabel string
	// ImportedBy is the operator/credential performing the import (AC-4).
	ImportedBy string
	// Role is the caller's role; must be a catalog-author role (AC-6).
	Role authz.Role
}

// Report summarizes a successful profile import.
type Report struct {
	ProfileID    uuid.UUID
	SourceSha256 string
	OSCALVersion string
	ProfileTitle string
	SourceLabel  string
	ControlCount int
	// MappedCount is how many resolved controls matched an SCF anchor
	// deterministically (the rest are flagged NULL for operator mapping).
	MappedCount int
}

// Import runs the full pipeline. On any resolution or persistence failure it
// commits nothing (AC-5 / P0-511-3) but DOES record a
// 'profile_import_rejected' audit row in a separate committed transaction
// (AC-7).
func (im *Importer) Import(ctx context.Context, req Request) (Report, error) {
	// --- Go-side gates (run before the bytes touch the bridge) ---
	if !authorizedRole(req.Role) {
		return Report{}, ErrUnauthorizedRole
	}
	if req.ImportedBy == "" {
		return Report{}, ErrMissingImporter
	}
	if len(req.ProfileJSON) == 0 {
		return Report{}, ErrEmptyDocument
	}
	if len(req.ProfileJSON) > MaxProfileBytes {
		return Report{}, fmt.Errorf("%w (profile %d bytes > %d)", ErrDocumentTooLarge, len(req.ProfileJSON), MaxProfileBytes)
	}
	if len(req.Catalogs) == 0 {
		return Report{}, ErrNoCatalogs
	}
	if len(req.Catalogs) > MaxSuppliedCatalogs {
		return Report{}, fmt.Errorf("%w (%d > %d)", ErrTooManyCatalogs, len(req.Catalogs), MaxSuppliedCatalogs)
	}
	for i, c := range req.Catalogs {
		if len(c) == 0 {
			return Report{}, fmt.Errorf("%w: supplied catalog #%d is empty", ErrNoCatalogs, i)
		}
		if len(c) > MaxCatalogBytes {
			return Report{}, fmt.Errorf("%w (catalog #%d %d bytes > %d)", ErrDocumentTooLarge, i, len(c), MaxCatalogBytes)
		}
	}

	// Provenance hash is over the PROFILE bytes (the imported artifact); the
	// supplied catalogs are inputs to resolution, not the imported document.
	sum := sha256.Sum256(req.ProfileJSON)
	sourceSha := hex.EncodeToString(sum[:])

	// --- Bridge resolve + validate ---
	resp, err := im.bridge.ImportProfile(ctx, req.ProfileJSON, req.Catalogs, req.SourceLabel)
	if err != nil {
		return Report{}, fmt.Errorf("profileimport: bridge ImportProfile: %w", err)
	}
	if !resp.GetValid() {
		// AC-5: nothing persisted. Record the rejection (AC-7), then surface
		// the structured errors (a validation failure OR an external /
		// unresolvable import.href — P0-511-1).
		_ = im.writeAuditRejected(ctx, req, sourceSha, resp.GetErrors())
		return Report{}, fmt.Errorf("%w: %v", ErrResolutionFailed, resp.GetErrors())
	}

	// --- Transactional persistence (AC-5) ---
	report, err := im.persist(ctx, req, sourceSha, resp)
	if err != nil {
		// A persistence failure rolls the baseline back; record rejection.
		_ = im.writeAuditRejected(ctx, req, sourceSha, []string{err.Error()})
		return Report{}, err
	}
	return report, nil
}

// persist writes the profile provenance row + every resolved control inside
// ONE transaction under app.current_tenant. RLS scopes every write to the
// caller's tenant (P0-511-5 / AC-12). The audit row is written in the SAME
// transaction on success so the success record is atomic with the baseline.
func (im *Importer) persist(ctx context.Context, req Request, sourceSha string, resp *oscalv1.ImportProfileResponse) (Report, error) {
	tx, err := im.pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("profileimport: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return Report{}, fmt.Errorf("profileimport: apply tenant: %w", err)
	}
	q := dbx.New(tx)
	tenantID, err := tenantUUID(ctx)
	if err != nil {
		return Report{}, err
	}

	controls := resp.GetControls()
	profileID := uuid.New()
	if _, err := q.InsertImportedProfile(ctx, dbx.InsertImportedProfileParams{
		ID:           pgUUID(profileID),
		TenantID:     pgUUID(tenantID),
		ImportedBy:   req.ImportedBy,
		SourceSha256: sourceSha,
		SourceLabel:  req.SourceLabel,
		OscalVersion: resp.GetOscalVersion(),
		// A resolved profile baseline has no catalog title of its own; the
		// catalog_title column carries the empty default and profile_title
		// carries the resolved profile's declared title.
		CatalogTitle: "",
		ProfileTitle: resp.GetProfileTitle(),
		ControlCount: int32(len(controls)),
	}); err != nil {
		return Report{}, fmt.Errorf("profileimport: insert profile: %w", err)
	}

	// Pre-load the SCF anchor scf_ids for the current SCF version so the
	// deterministic requirement -> SCF-anchor mapping (slice-511 D3, reusing
	// slice-492's crosswalk) is a single query, not one per control.
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
			ImportedCatalogID: pgUUID(profileID),
			SourceControlID:   c.GetControlId(),
			Title:             c.GetTitle(),
			Statement:         c.GetStatement(),
			GroupPath:         c.GetGroupPath(),
			ScfAnchorID:       scfAnchor,
		}); err != nil {
			return Report{}, fmt.Errorf("profileimport: insert control %q: %w", c.GetControlId(), err)
		}
	}

	// Success audit row, atomic with the baseline (AC-7).
	detail, _ := json.Marshal(map[string]any{
		"mapped":   mapped,
		"unmapped": len(controls) - mapped,
		"kind":     "profile",
	})
	if _, err := q.InsertImportedCatalogAuditLog(ctx, dbx.InsertImportedCatalogAuditLogParams{
		ID:           pgUUID(uuid.New()),
		TenantID:     pgUUID(tenantID),
		CatalogID:    pgUUID(profileID),
		Action:       "profile_imported",
		Actor:        req.ImportedBy,
		SourceSha256: sourceSha,
		SourceLabel:  req.SourceLabel,
		ControlCount: int32(len(controls)),
		Detail:       detail,
	}); err != nil {
		return Report{}, fmt.Errorf("profileimport: insert audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("profileimport: commit: %w", err)
	}
	return Report{
		ProfileID:    profileID,
		SourceSha256: sourceSha,
		OSCALVersion: resp.GetOscalVersion(),
		ProfileTitle: resp.GetProfileTitle(),
		SourceLabel:  req.SourceLabel,
		ControlCount: len(controls),
		MappedCount:  mapped,
	}, nil
}

// writeAuditRejected records a 'profile_import_rejected' row in its OWN
// committed transaction so the rejection is durable while the baseline
// persistence (a separate, rolled-back transaction) committed nothing
// (AC-5 + AC-7).
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
	detail, _ := json.Marshal(map[string]any{"errors": errs, "kind": "profile"})
	q := dbx.New(tx)
	if _, err := q.InsertImportedCatalogAuditLog(ctx, dbx.InsertImportedCatalogAuditLogParams{
		ID:           pgUUID(uuid.New()),
		TenantID:     pgUUID(tenantID),
		CatalogID:    pgtype.UUID{}, // NULL — no baseline was committed.
		Action:       "profile_import_rejected",
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
// framework version — the deterministic crosswalk surface (slice-511 D3 /
// slice-492 D1). A resolved control whose OSCAL control-id matches a key
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
		return nil, fmt.Errorf("profileimport: load scf anchors: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var scfID string
		if err := rows.Scan(&scfID); err != nil {
			return nil, fmt.Errorf("profileimport: scan scf anchor: %w", err)
		}
		out[scfID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("profileimport: iterate scf anchors: %w", err)
	}
	return out, nil
}

func tenantUUID(ctx context.Context) (uuid.UUID, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("profileimport: %w", err)
	}
	id, err := uuid.Parse(tenant)
	if err != nil {
		return uuid.Nil, fmt.Errorf("profileimport: tenant id is not a UUID: %w", err)
	}
	return id, nil
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
