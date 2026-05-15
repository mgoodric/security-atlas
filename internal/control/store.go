package control

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

// ErrSCFAnchorUnknown is returned when the bundle's scf_anchor_id does not
// resolve to a row in the scf_anchors catalog. The HTTP layer maps this to
// 404 (canvas invariant 7 — no anchor, no control).
var ErrSCFAnchorUnknown = errors.New("control bundle: scf_anchor_id does not resolve to a known SCF anchor")

// UploadResult is the persistence-side outcome of a bundle upload.
type UploadResult struct {
	ControlID    uuid.UUID
	BundleID     string
	Version      int32
	SupersededID uuid.UUID // zero if this was the initial upload.
	IsNewBundle  bool      // true for an initial upload, false for supersession.
}

// Store persists parsed bundles to the controls table under RLS.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Upload persists a validated bundle as a new control row. If an active row
// with the same (tenant, bundle_id) exists, the prior row is marked
// superseded and a new row is inserted in the same transaction.
//
// AC-6: re-uploading the same bundle id is a version bump (creates a new
// control row, supersedes the prior).
//
// Pre-conditions:
//   - b has been parsed and ValidateStructural has passed.
//   - b.ValidateApplicabilityExpr has passed.
//   - b.ValidateEvidenceKinds has passed if a registry is in use.
//   - ctx carries a tenancy value set via tenancy.WithTenant.
//
// `uploadedBy` is recorded for audit; typically the credential id from
// authctx.CredentialFromContext.
func (s *Store) Upload(ctx context.Context, b *Bundle, uploadedBy string) (UploadResult, error) {
	if b == nil {
		return UploadResult{}, errors.New("control bundle: nil bundle")
	}

	var result UploadResult
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// 1. Resolve scf_anchor_id. Accept either a uuid (FK target) or a
		//    human-readable SCF code (e.g. "IAC-06") that resolves via the
		//    slice-006 importer. Both shapes are common: hand-written
		//    bundles use the code, machine-generated bundles use the uuid.
		anchorID, err := resolveSCFAnchor(ctx, q, b.Manifest.SCFAnchorID)
		if err != nil {
			return err
		}

		// 2. Marshal JSONB fields.
		applJSON, err := b.Manifest.ApplicabilityExprJSON()
		if err != nil {
			return err
		}
		queriesJSON, err := b.Manifest.EvidenceQueriesJSON()
		if err != nil {
			return err
		}
		manualJSON, err := b.Manifest.ManualEvidenceSchemaJSON()
		if err != nil {
			return err
		}

		// 3. Look up the active predecessor for this bundle_id.
		var prior dbx.GetActiveControlByBundleIDRow
		prior, err = q.GetActiveControlByBundleID(ctx, dbx.GetActiveControlByBundleIDParams{
			TenantID: pgUUID(tenantID),
			BundleID: b.Manifest.BundleID,
		})
		isNew := false
		if errors.Is(err, pgx.ErrNoRows) {
			isNew = true
		} else if err != nil {
			return fmt.Errorf("lookup prior version: %w", err)
		}

		// 4. Insert the new row. version = prior.version + 1 (or 1 for new).
		newID := uuid.New()
		nextVersion := int32(1)
		if !isNew {
			nextVersion = prior.Version + 1
		}

		scfCode := b.Manifest.SCFAnchorID
		if anchorRow, lookupErr := lookupAnchorRow(ctx, q, anchorID); lookupErr == nil {
			scfCode = anchorRow.ScfID
		}

		family := b.Manifest.ControlFamily
		if family == "" {
			if anchorRow, lookupErr := lookupAnchorRow(ctx, q, anchorID); lookupErr == nil {
				family = anchorRow.Family
			} else {
				family = "UNKNOWN"
			}
		}

		lifecycle := dbx.ControlLifecycleState(b.Manifest.LifecycleStateOrDefault())
		var uploader *string
		if uploadedBy != "" {
			u := uploadedBy
			uploader = &u
		}
		var freshness *string
		if b.Manifest.FreshnessClass != "" {
			f := b.Manifest.FreshnessClass
			freshness = &f
		}

		// 5. Supersede the predecessor (if any) FIRST, then insert the new
		//    active row.
		//
		//    Ordering matters and is NOT interchangeable. The partial unique
		//    index `controls_one_active_version_per_bundle` allows at most
		//    one row per (tenant_id, bundle_id) with superseded_by IS NULL.
		//    If we inserted the new (superseded_by-NULL) row before flipping
		//    the predecessor, there would momentarily be TWO active rows for
		//    the bundle and the INSERT would fail with SQLSTATE 23505 — which
		//    is exactly the bug that broke every control-bundle RE-upload
		//    (the self-host bundle's idempotency check surfaced it).
		//
		//    Flipping the predecessor first means MarkControlSuperseded sets
		//    `prior.superseded_by = newID` while `newID` does not yet exist
		//    as a controls row. That is sound because
		//    `controls_superseded_by_fk` is DEFERRABLE INITIALLY DEFERRED
		//    (migration 20260511000032) — the FK is validated at COMMIT, by
		//    which point the new row below has been inserted. This is the
		//    order `internal/db/queries/controls.sql` documents.
		if !isNew {
			priorID := uuid.UUID(prior.ID.Bytes)
			if err := q.MarkControlSuperseded(ctx, dbx.MarkControlSupersededParams{
				TenantID:     pgUUID(tenantID),
				ID:           pgUUID(priorID),
				SupersededBy: pgUUID(newID),
			}); err != nil {
				return fmt.Errorf("mark superseded: %w", err)
			}
			result.SupersededID = priorID
		}

		// 6. Insert the new active row (superseded_by NULL). After step 5
		//    the predecessor is no longer NULL, so the partial unique index
		//    has room for exactly this row.
		scfPtr := &scfCode
		row, err := q.InsertControlVersion(ctx, dbx.InsertControlVersionParams{
			ID:                   pgUUID(newID),
			TenantID:             pgUUID(tenantID),
			BundleID:             b.Manifest.BundleID,
			Version:              nextVersion,
			ScfID:                scfPtr,
			ScfAnchorID:          pgUUID(anchorID),
			Title:                b.Manifest.Title,
			Description:          b.Manifest.Description,
			ControlFamily:        family,
			ImplementationType:   dbx.ControlImplementationType(b.Manifest.ImplementationType),
			OwnerRole:            b.Manifest.OwnerRole,
			LifecycleState:       lifecycle,
			ApplicabilityExpr:    string(applJSON),
			EvidenceQueries:      queriesJSON,
			ManualEvidenceSchema: manualJSON,
			LinkedPolicyIds:      coerceStringSlice(b.Manifest.LinkedPolicyIDs),
			FreshnessClass:       freshness,
			BundleManifestYaml:   string(b.ManifestYAMLRaw),
			BundleManifestHash:   b.ManifestHashHex,
			BundleUploadedBy:     uploader,
		})
		if err != nil {
			return fmt.Errorf("insert control version: %w", err)
		}

		result.ControlID = uuid.UUID(row.ID.Bytes)
		result.BundleID = b.Manifest.BundleID
		result.Version = nextVersion
		result.IsNewBundle = isNew
		return nil
	})

	return result, err
}

// resolveSCFAnchor accepts either a uuid string or an SCF code (e.g.,
// "IAC-06") and returns the anchor's uuid. Unknown values fail with
// ErrSCFAnchorUnknown.
func resolveSCFAnchor(ctx context.Context, q *dbx.Queries, ref string) (uuid.UUID, error) {
	if ref == "" {
		return uuid.Nil, ErrSCFAnchorUnknown
	}
	// First, try as a uuid.
	if u, err := uuid.Parse(ref); err == nil {
		row, err := q.GetSCFAnchorByID(ctx, pgUUID(u))
		if err == nil {
			return uuid.UUID(row.ID.Bytes), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("get scf anchor by id: %w", err)
		}
	}
	// Fall back: SCF code lookup.
	row, err := q.GetSCFAnchorBySCFID(ctx, ref)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrSCFAnchorUnknown
		}
		return uuid.Nil, fmt.Errorf("get scf anchor by code: %w", err)
	}
	return uuid.UUID(row.ID.Bytes), nil
}

// lookupAnchorRow returns the anchor row by uuid; thin helper so the Upload
// path can hydrate family/code from the same source of truth.
func lookupAnchorRow(ctx context.Context, q *dbx.Queries, id uuid.UUID) (dbx.ScfAnchor, error) {
	return q.GetSCFAnchorByID(ctx, pgUUID(id))
}

// inTx mirrors scope.Store.inTx. Opens a transaction, applies the tenant
// GUC, runs fn, commits on success.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("control: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("control: begin tx: %w", err)
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
		return fmt.Errorf("control: commit: %w", err)
	}
	return nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// coerceStringSlice returns the input slice or a fresh empty slice when nil
// — the DB column has NOT NULL DEFAULT '{}' on a text[] column, and the pgx
// driver maps Go nil to SQL NULL by default which would trip the NOT NULL.
func coerceStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
