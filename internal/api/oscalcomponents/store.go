// Package oscalcomponents serves the slice-589 vendor-claim read API plus the
// operator accept/reject/needs-info disposition workflow over the slice-512
// imported component-definitions:
//
//	GET  /v1/oscal/component-definitions             list a tenant's imports
//	GET  /v1/oscal/component-definitions/{id}        one import + its claims
//	POST /v1/oscal/component-claims/{id}:accept      operator credits a claim
//	POST /v1/oscal/component-claims/{id}:reject       operator declines a claim
//	POST /v1/oscal/component-claims/{id}:needs-info    operator parks a claim
//
// Slice 512 lands a vendor's component-definition as VENDOR-ATTRIBUTED CLAIMS
// (imported_component_claims, is_vendor_claim=TRUE, claim_status='asserted').
// A vendor claim is an ASSERTION, not platform-verified evidence — the
// disposition records the human decision and NEVER auto-satisfies a control
// (canvas invariant #2, P0-512-1, the slice-589 anti-criteria). Accepting a
// claim records that the operator credits the vendor's assertion; the
// is_vendor_claim=TRUE schema CHECK stays, and nothing here writes to
// control_evaluations or the evidence ledger.
//
// The reads + the disposition write are pure SQL over persisted rows — this
// package never touches the compliance-trestle bridge, so the integration
// suite runs against seeded DB rows with no Python oscal-bridge.
//
// The Store wraps the sqlc Queries with the tenancy plumbing required for
// RLS: it opens a transaction, applies the tenant GUC via internal/tenancy,
// and runs the query inside that transaction so RLS policies see the tenant
// id. This mirrors oscalprovenance.Store.inTx (slice 599) / controldetail.
package oscalcomponents

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrClaimNotFound is returned when a disposition or mapping targets a claim
// id that does not resolve within the caller's tenant (cross-tenant or
// unknown). The handler maps it to 404.
var ErrClaimNotFound = errors.New("oscalcomponents: component claim not found")

// ErrAnchorNotFound is returned when an SCF-anchor mapping targets an scf_id
// that does not resolve to a bundled SCF anchor in the current SCF framework
// version. The handler maps it to 422 — the request is well-formed but the
// referenced anchor does not exist (invariant #7: a mapping must target a real
// SCF anchor, never a free-form string the operator typed).
var ErrAnchorNotFound = errors.New("oscalcomponents: scf anchor not found")

// Store is the OSCAL component-claim read + disposition layer over the
// application pgx pool.
type Store struct {
	pool *pgxpool.Pool
	// idgen lets tests inject deterministic ids; production uses uuid.New.
	idgen func() uuid.UUID
}

// NewStore wires a Store over the application pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, idgen: uuid.New} }

func (s *Store) newID() uuid.UUID {
	if s.idgen != nil {
		return s.idgen()
	}
	return uuid.New()
}

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors oscalprovenance.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("oscalcomponents: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("oscalcomponents: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("oscalcomponents: commit: %w", err)
	}
	return nil
}

// ListDefinitions returns every imported component-definition for the tenant,
// most recent first. RLS scopes the read.
func (s *Store) ListDefinitions(ctx context.Context) ([]dbx.ImportedCatalog, error) {
	var rows []dbx.ImportedCatalog
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListImportedComponentDefinitions(ctx, pgUUID(tenantID))
		return qerr
	})
	return rows, err
}

// DefinitionWithClaims is the assembled read for one imported
// component-definition: the provenance row + the flattened vendor-claim list
// (joined to the owning component for display). A cross-tenant or
// non-component-definition id yields pgx.ErrNoRows from the provenance read.
type DefinitionWithClaims struct {
	Definition dbx.ImportedCatalog
	Claims     []dbx.ListImportedComponentClaimsForDefinitionRow
}

// GetDefinitionWithClaims reads one imported component-definition + its vendor
// claims. The provenance read is RLS-scoped and kind-pinned
// ('component_definition'); a cross-tenant / unknown / wrong-kind id returns
// pgx.ErrNoRows.
func (s *Store) GetDefinitionWithClaims(ctx context.Context, defID uuid.UUID) (DefinitionWithClaims, error) {
	var out DefinitionWithClaims
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		def, qerr := q.GetImportedComponentDefinitionByID(ctx, dbx.GetImportedComponentDefinitionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(defID),
		})
		if qerr != nil {
			return qerr
		}
		claims, qerr := q.ListImportedComponentClaimsForDefinition(ctx, dbx.ListImportedComponentClaimsForDefinitionParams{
			TenantID:          pgUUID(tenantID),
			ImportedCatalogID: pgUUID(defID),
		})
		if qerr != nil {
			return qerr
		}
		out = DefinitionWithClaims{Definition: def, Claims: claims}
		return nil
	})
	return out, err
}

// Disposition records one operator disposition on a vendor claim. It reads the
// claim's current status (the from_status of the audit trail), writes the new
// claim_status + disposition metadata, and appends an append-only audit row —
// all in one RLS-scoped transaction. It NEVER touches control_evaluations or
// the evidence ledger: a vendor claim is an assertion, and a disposition is
// metadata on that claim (invariant #2 / P0-512-1).
//
// toStatus must be one of 'accepted' / 'rejected' / 'needs_info'. actor is the
// disposing operator's credential id. A claim id that does not resolve in the
// tenant returns ErrClaimNotFound.
func (s *Store) Disposition(ctx context.Context, claimID uuid.UUID, toStatus, actor, note string) (dbx.ImportedComponentClaim, error) {
	var updated dbx.ImportedComponentClaim
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		current, qerr := q.GetImportedComponentClaimByID(ctx, dbx.GetImportedComponentClaimByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(claimID),
		})
		if qerr != nil {
			if errors.Is(qerr, pgx.ErrNoRows) {
				return ErrClaimNotFound
			}
			return qerr
		}

		actorCopy := actor
		updated, qerr = q.DispositionImportedComponentClaim(ctx, dbx.DispositionImportedComponentClaimParams{
			TenantID:        pgUUID(tenantID),
			ID:              pgUUID(claimID),
			ClaimStatus:     toStatus,
			DispositionedBy: &actorCopy,
			DispositionNote: note,
		})
		if qerr != nil {
			return qerr
		}

		if _, qerr = q.InsertImportedComponentClaimDisposition(ctx, dbx.InsertImportedComponentClaimDispositionParams{
			ID:         pgUUID(s.newID()),
			TenantID:   pgUUID(tenantID),
			ClaimID:    pgUUID(claimID),
			FromStatus: current.ClaimStatus,
			ToStatus:   toStatus,
			Actor:      actor,
			Note:       note,
		}); qerr != nil {
			return qerr
		}
		return nil
	})
	return updated, err
}

// MapScfAnchor maps one unmapped (or re-maps) vendor claim to a canonical SCF
// anchor. It validates the target scf_id resolves to a bundled SCF anchor in
// the current SCF framework version, reads the claim's current scf_anchor_id
// (the from_anchor of the audit trail), sets the new scf_anchor_id, and
// appends an append-only mapping-audit row — all in one RLS-scoped
// transaction.
//
// THE LOAD-BEARING BOUNDARY: this NEVER touches control_evaluations or the
// evidence ledger. The claim stays a claim (is_vendor_claim + claim_status are
// untouched); mapping only sets the crosswalk (invariant #2 / #7 / P0-512-1).
//
// scfAnchorID is the SCF code (e.g. "IAC-06"), validated against the bundled
// catalog. actor is the mapping operator's credential id. A claim id that does
// not resolve in the tenant returns ErrClaimNotFound; an scf_id that does not
// resolve to a bundled anchor returns ErrAnchorNotFound.
func (s *Store) MapScfAnchor(ctx context.Context, claimID uuid.UUID, scfAnchorID, actor string) (dbx.ImportedComponentClaim, error) {
	var updated dbx.ImportedComponentClaim
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Validate the target anchor exists in the bundled SCF catalog. The
		// scf_anchors spine is catalog-global (tenant_id IS NULL); a bad
		// scf_id must NOT silently set a dangling crosswalk (invariant #7).
		if _, qerr := q.GetSCFAnchorBySCFID(ctx, scfAnchorID); qerr != nil {
			if errors.Is(qerr, pgx.ErrNoRows) {
				return ErrAnchorNotFound
			}
			return qerr
		}

		current, qerr := q.GetImportedComponentClaimByID(ctx, dbx.GetImportedComponentClaimByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(claimID),
		})
		if qerr != nil {
			if errors.Is(qerr, pgx.ErrNoRows) {
				return ErrClaimNotFound
			}
			return qerr
		}

		anchorCopy := scfAnchorID
		updated, qerr = q.MapImportedComponentClaimScfAnchor(ctx, dbx.MapImportedComponentClaimScfAnchorParams{
			TenantID:    pgUUID(tenantID),
			ID:          pgUUID(claimID),
			ScfAnchorID: &anchorCopy,
		})
		if qerr != nil {
			return qerr
		}

		if _, qerr = q.InsertImportedComponentClaimScfMapping(ctx, dbx.InsertImportedComponentClaimScfMappingParams{
			ID:              pgUUID(s.newID()),
			TenantID:        pgUUID(tenantID),
			ClaimID:         pgUUID(claimID),
			FromScfAnchorID: current.ScfAnchorID, // nil when the claim was unmapped
			ToScfAnchorID:   &anchorCopy,
			Actor:           actor,
			Note:            "",
		}); qerr != nil {
			return qerr
		}
		return nil
	})
	return updated, err
}

// pgUUID converts a uuid.UUID to the pgtype.UUID the sqlc params expect.
func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
