package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/demoprov"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrSCFAnchorUnknown is returned when the bundle's scf_anchor_id does not
// resolve to a row in the scf_anchors catalog. The HTTP layer maps this to
// 404 (canvas invariant 7 — no anchor, no control).
var ErrSCFAnchorUnknown = errors.New("control bundle: scf_anchor_id does not resolve to a known SCF anchor")

// ErrSCFReleaseUnknown is returned when the bundle declares an scf_release
// that is not loaded (#1770). Distinct from ErrSCFCodeNotInRelease so an
// author can tell a mistyped release from a code the release lacks.
var ErrSCFReleaseUnknown = scfrelease.ErrUnknownRelease

// ErrSCFCodeNotInRelease is returned when the bundle's declared scf_release
// is loaded but has no anchor for its scf_anchor_id (#1770).
var ErrSCFCodeNotInRelease = scfrelease.ErrCodeNotInRelease

// SCFReleaseDeprecationWarning is returned on an upload whose control.yaml
// has no scf_release (#1770). The bundle binds as before, to the current
// SCF release; the field becomes required in a later release.
const SCFReleaseDeprecationWarning = "control.yaml has no scf_release, so scf_anchor_id was bound in the current SCF release; declare scf_release, which will become required"

// KeptReviewedAnchorWarning is returned on an upload of a stock manifest
// that was not applied because a person re-anchored the control through an
// approved SCF remap proposal (#1776), or edited it after one (#2358).
const KeptReviewedAnchorWarning = "kept the reviewed SCF re-anchor of this stock control: a person approved its remap, so the shipped manifest was not applied; update the bundle to change the anchor"

// ErrControlChangedConcurrently is returned when the active version a
// description-only upload was about to update in place was superseded by a
// concurrent upload in the meantime. The HTTP layer maps this to 409; the
// caller can retry against the new active version.
var ErrControlChangedConcurrently = errors.New("control bundle: the active version changed during upload; retry")

// UploadResult is the persistence-side outcome of a bundle upload.
type UploadResult struct {
	ControlID uuid.UUID
	BundleID  string
	Version   int32
	// SupersededID is the predecessor row that was flagged superseded by
	// this upload. Zero when there was no predecessor (initial upload) OR
	// when the upload was a byte-identical-content no-op (nothing changed,
	// so nothing was superseded).
	SupersededID uuid.UUID
	// IsNewBundle is true only for an initial upload (no prior active
	// version existed). It is false both for a content-change version bump
	// (supersession) and for a byte-identical-content no-op re-upload.
	IsNewBundle bool
	// UpdatedInPlace is true when the manifest differed from the active
	// version's only in `description`, so the active row was updated in
	// place rather than superseded (issue #1908). ControlID and Version
	// are the existing row's; SupersededID is zero.
	UpdatedInPlace bool
	// AnchorRebound is true when the manifest's SCF code now resolves to a
	// different anchor row (a newer SCF release was imported, #1768) with
	// the same title, and the active row was moved to it in place rather
	// than superseded (#1971).
	AnchorRebound bool
	// NeedsReanchoring is true when the manifest's SCF code now resolves to
	// a newer release's row whose title differs: the code changed meaning,
	// so the control was left on its legacy anchor (#1971 D3). It earns no
	// framework credit until it is re-anchored.
	NeedsReanchoring bool
	// KeptReviewedAnchor is true when the upload was the shipped stock
	// manifest of a control whose active revision is a person's approved SCF
	// re-anchor (#1776), or descends from one (#2358): nothing changed, and
	// Warnings says why. The hold is recorded in stock_control_holdbacks.
	KeptReviewedAnchor bool
	// AnchorID is the SCF anchor the bundle's scf_anchor_id resolved to;
	// zero for an unanchored (anchor_fit: none) control.
	AnchorID uuid.UUID
	// SCFRelease is the SCF release label scf_anchor_id was resolved in
	// (#1770); empty for an unanchored control.
	SCFRelease string
	// Warnings are non-fatal notes for the uploader, such as
	// SCFReleaseDeprecationWarning.
	Warnings []string
}

// rebindOutcome is what rebindAnchor did with an unchanged manifest whose
// SCF code now resolves to a different anchor row.
type rebindOutcome int

const (
	rebindNone          rebindOutcome = iota // already on the resolved anchor
	rebindMoved                              // moved to the new row: same code, same title
	rebindNeedsReanchor                      // left in place: the code's meaning changed
)

// rebindAnchor handles an unchanged manifest (byte-identical, or differing
// only in in-place metadata) whose SCF code now resolves to a different
// anchor row than the active row's: a new SCF release minted a new row for
// the code (#1768). Matt's call (#1971 D3): move the control only when the
// new row has the same code AND the same title as the old one, i.e. the
// meaning did not change. Otherwise the control stays where it is and is
// reported as needing re-anchoring, never silently re-pointed.
//
// anchorID is what bindSCFAnchor resolved the manifest to. A bundle pinned
// to one release is never re-bound: one that declares scf_release (#1770),
// and one whose scf_anchor_id is an anchor UUID, which scfrelease.Resolve
// binds to that exact row in whichever release holds it. Moving either is
// the silent re-point #1770 and D3 forbid, and for a UUID ref would flip
// the control between releases on every boot and re-upload. An unanchored
// control (anchor_fit: none) has nothing to re-bind. As a backstop, the
// control only ever moves to a row in the current SCF release.
func rebindAnchor(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, prior dbx.LockActiveControlByBundleIDRow, anchorID uuid.UUID, pinnedRelease, anchorRef string) (rebindOutcome, error) {
	if pinnedRelease != "" || anchorID == uuid.Nil {
		return rebindNone, nil
	}
	if _, err := uuid.Parse(strings.TrimSpace(anchorRef)); err == nil {
		return rebindNone, nil
	}
	if !prior.ScfAnchorID.Valid || uuid.UUID(prior.ScfAnchorID.Bytes) == anchorID {
		return rebindNone, nil
	}
	oldRow, err := lookupAnchorRow(ctx, q, uuid.UUID(prior.ScfAnchorID.Bytes))
	if err != nil {
		return rebindNone, fmt.Errorf("rebind control anchor: read bound anchor: %w", err)
	}
	newRow, err := lookupAnchorRow(ctx, q, anchorID)
	if err != nil {
		return rebindNone, fmt.Errorf("rebind control anchor: read resolved anchor: %w", err)
	}
	release, err := q.GetFrameworkVersionByID(ctx, newRow.FrameworkVersionID)
	if err != nil {
		return rebindNone, fmt.Errorf("rebind control anchor: read resolved anchor's release: %w", err)
	}
	if release.Status != dbx.FrameworkVersionStatusCurrent {
		return rebindNone, nil
	}
	// #2236: record the move or the flag in this transaction, read before
	// the move, while the control is still on its legacy anchor.
	legacy, found, err := legacyAnchorRow(ctx, q, tenantID, prior.ID)
	if err != nil {
		return rebindNone, err
	}
	record := func(status string) error {
		if !found {
			// Not expected: the bound anchor is outside the current
			// release. The re-bind itself is still right, so the upload
			// goes ahead without its record rather than failing.
			slog.WarnContext(ctx, "control: scf re-bind not recorded; control not on a legacy anchor",
				"control_id", uuid.UUID(prior.ID.Bytes).String(), "status", status)
			return nil
		}
		_, _, err := scfimport.RecordRemapProposal(ctx, q, legacy, status, scfimport.SourceUpload)
		return err
	}
	if oldRow.ScfID != newRow.ScfID || oldRow.Title != newRow.Title {
		if err := record(scfimport.ProposalOpen); err != nil {
			return rebindNone, err
		}
		return rebindNeedsReanchor, nil
	}
	n, err := q.RebindControlAnchorInPlace(ctx, dbx.RebindControlAnchorInPlaceParams{
		TenantID:    pgUUID(tenantID),
		ID:          prior.ID,
		ScfAnchorID: pgUUID(anchorID),
		ScfID:       &newRow.ScfID,
	})
	if err != nil {
		return rebindNone, fmt.Errorf("rebind control anchor: %w", err)
	}
	if n != 1 {
		return rebindNone, ErrControlChangedConcurrently
	}
	if err := record(scfimport.ProposalRebound); err != nil {
		return rebindNone, err
	}
	return rebindMoved, nil
}

// legacyAnchorRow reads the control's row of ListControlsOnLegacySCFAnchors:
// its legacy anchor and where SCF says the anchor's concept went, which the
// re-bind record (#2236) carries. rebindAnchor calls it only for a control
// whose anchor is not in the current release, so found should be true.
func legacyAnchorRow(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, controlID pgtype.UUID) (row dbx.ListControlsOnLegacySCFAnchorsRow, found bool, err error) {
	rows, err := q.ListControlsOnLegacySCFAnchors(ctx, pgUUID(tenantID))
	if err != nil {
		return dbx.ListControlsOnLegacySCFAnchorsRow{}, false, fmt.Errorf("rebind control anchor: list controls on legacy anchors: %w", err)
	}
	for _, r := range rows {
		if r.ControlID == controlID {
			return r, true, nil
		}
	}
	return dbx.ListControlsOnLegacySCFAnchorsRow{}, false, nil
}

// apply records a rebind outcome on the upload result.
func (o rebindOutcome) apply(r *UploadResult) {
	r.AnchorRebound = o == rebindMoved
	r.NeedsReanchoring = o == rebindNeedsReanchor
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
// control row, supersedes the prior). Two exceptions: byte-identical content
// is a no-op, and a change confined to `description` updates the active row
// in place (issue #1908).
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
		var err error
		result, err = upload(ctx, q, tenantID, b, uploadedBy)
		return err
	})
	return result, err
}

// UploadTx is Upload inside the caller's transaction, so a caller can commit
// the control together with its own writes (the control import's approval
// and audit row, #1782). tx must already carry the caller's tenant
// (tenancy.ApplyTenant), and ctx names the same tenant. UploadTx neither
// commits nor rolls back.
func UploadTx(ctx context.Context, tx pgx.Tx, b *Bundle, uploadedBy string) (UploadResult, error) {
	if b == nil {
		return UploadResult{}, errors.New("control bundle: nil bundle")
	}
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return UploadResult{}, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return UploadResult{}, fmt.Errorf("control: parse tenant id: %w", err)
	}
	return upload(ctx, dbx.New(tx), tenantID, b, uploadedBy)
}

// keepReviewedAnchor guards the boot seed (#1776). Bootstrap re-uploads
// every stock manifest under controls/soc2/ on each boot. When a person
// approved an SCF remap of that stock control, its active revision is the
// approved re-anchor, or a person's later edit on top of it (#2358), and
// superseding it with the shipped manifest would silently undo that work
// every boot, or fail outright when the shipped code is gone from the
// current release (a split or withdrawn anchor). The upload is kept as a
// no-op with a warning instead, and the hold is recorded so an admin can
// list the shipped updates the tenant is not receiving (#2358). It runs
// before the manifest's anchor is resolved for that reason. ok is false for
// every other upload, which proceeds as before.
func keepReviewedAnchor(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, b *Bundle) (UploadResult, bool, error) {
	if !scfimport.IsStockManifest(b.Manifest.BundleID, string(b.ManifestYAMLRaw)) {
		return UploadResult{}, false, nil
	}
	prior, err := q.LockActiveControlByBundleID(ctx, dbx.LockActiveControlByBundleIDParams{
		TenantID: pgUUID(tenantID),
		BundleID: b.Manifest.BundleID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UploadResult{}, false, nil
	}
	if err != nil {
		return UploadResult{}, false, fmt.Errorf("lookup prior version: %w", err)
	}
	if prior.BundleManifestHash == b.ManifestHashHex {
		// The tenant already has the shipped manifest, so nothing is held
		// back; clear any row an earlier shipped manifest left (#2599).
		if err := q.ClearStockControlHoldback(ctx, dbx.ClearStockControlHoldbackParams{
			TenantID: pgUUID(tenantID),
			BundleID: b.Manifest.BundleID,
		}); err != nil {
			return UploadResult{}, false, fmt.Errorf("clear held-back stock control: %w", err)
		}
		return UploadResult{}, false, nil
	}
	approved, err := q.ApprovedRemapLineageRevision(ctx, dbx.ApprovedRemapLineageRevisionParams{
		TenantID:  pgUUID(tenantID),
		ControlID: prior.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UploadResult{}, false, nil
	}
	if err != nil {
		return UploadResult{}, false, fmt.Errorf("check approved SCF remap: %w", err)
	}
	if err := q.RecordStockControlHoldback(ctx, dbx.RecordStockControlHoldbackParams{
		TenantID:            pgUUID(tenantID),
		BundleID:            b.Manifest.BundleID,
		ApprovedControlID:   approved,
		ShippedManifestHash: b.ManifestHashHex,
	}); err != nil {
		return UploadResult{}, false, fmt.Errorf("record held-back stock control: %w", err)
	}
	return UploadResult{
		ControlID:          uuid.UUID(prior.ID.Bytes),
		BundleID:           b.Manifest.BundleID,
		Version:            prior.Version,
		KeptReviewedAnchor: true,
		AnchorID:           uuid.UUID(prior.ScfAnchorID.Bytes),
		Warnings:           []string{KeptReviewedAnchorWarning},
	}, true, nil
}

// upload is the body of Upload and UploadTx.
func upload(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, b *Bundle, uploadedBy string) (UploadResult, error) {
	if kept, ok, err := keepReviewedAnchor(ctx, q, tenantID, b); err != nil || ok {
		return kept, err
	}
	var result UploadResult
	err := func() error {
		// 1. Resolve scf_anchor_id. Accept either a uuid (FK target) or a
		//    human-readable SCF code (e.g. "IAC-37") that resolves via the
		//    slice-006 importer. Both shapes are common: hand-written
		//    bundles use the code, machine-generated bundles use the uuid.
		//
		//    An empty scf_anchor_id passed ValidateStructural only with
		//    anchor_fit: none (issue #1736): the control stands unanchored
		//    and anchorID stays uuid.Nil (stored as NULL).
		//
		//    #1770: a declared scf_release binds the code within that
		//    release. Without one the code binds in the current release,
		//    as before, and the upload carries a deprecation warning.
		var anchorID uuid.UUID
		if b.Manifest.SCFAnchorID != "" {
			anchor, rel, err := bindSCFAnchor(ctx, q, b.Manifest.SCFRelease, b.Manifest.SCFAnchorID)
			if err != nil {
				return err
			}
			anchorID = uuid.UUID(anchor.ID.Bytes)
			result.AnchorID = anchorID
			result.SCFRelease = rel.Version
		}
		if b.Manifest.SCFRelease == "" {
			result.Warnings = append(result.Warnings, SCFReleaseDeprecationWarning)
		}
		result.Warnings = append(result.Warnings, b.Manifest.UnkindedQueryWarnings()...)

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
		refsJSON, err := b.Manifest.ExternalRefsJSON()
		if err != nil {
			return err
		}

		// 3. Look up the active predecessor for this bundle_id, locking it
		//    (FOR UPDATE) so a concurrent upload of the same bundle waits
		//    here and then decides against what this one commits. Without
		//    the lock two in-place updates could both copy the same
		//    manifest into control_manifest_revisions, and the first
		//    one's text would appear nowhere (issue #1958).
		var prior dbx.LockActiveControlByBundleIDRow
		prior, err = q.LockActiveControlByBundleID(ctx, dbx.LockActiveControlByBundleIDParams{
			TenantID: pgUUID(tenantID),
			BundleID: b.Manifest.BundleID,
		})
		isNew := false
		if errors.Is(err, pgx.ErrNoRows) {
			isNew = true
		} else if err != nil {
			return fmt.Errorf("lookup prior version: %w", err)
		}

		// 3a. Re-upload of BYTE-IDENTICAL content is a true no-op — return
		//     the existing active row, do not version-bump.
		//
		//     `bundle_manifest_hash` is the sha256 of the manifest YAML;
		//     when the active version's hash equals the incoming bundle's
		//     hash, nothing about the control changed and creating a
		//     "version 2" that is identical to "version 1" would be
		//     meaningless version churn. Slice 009 AC-6 ("re-uploading the
		//     same bundle id creates a new control row and supersedes the
		//     prior") is about re-uploading CHANGED content; identical
		//     content has nothing to supersede.
		//
		//     This is also what makes the docker-compose self-host bundle's
		//     bootstrap genuinely idempotent: `bootstrap.sh` re-runs phase 6
		//     (control-bundle upload) on every `docker compose up`, and its
		//     header documents control upload as an upsert. Without this
		//     guard a restart would version-bump all 50 SOC 2 controls to
		//     v2, v3, ... on every boot — the slice-065 idempotency
		//     assertion (AC-7) exists precisely to catch that.
		if !isNew && prior.BundleManifestHash == b.ManifestHashHex {
			outcome, err := rebindAnchor(ctx, q, tenantID, prior, anchorID, b.Manifest.SCFRelease, b.Manifest.SCFAnchorID)
			if err != nil {
				return err
			}
			outcome.apply(&result)
			result.ControlID = uuid.UUID(prior.ID.Bytes)
			result.BundleID = b.Manifest.BundleID
			result.Version = prior.Version
			result.IsNewBundle = false
			return nil
		}

		var uploader *string
		if uploadedBy != "" {
			u := uploadedBy
			uploader = &u
		}

		// 3b. A manifest that differs from the active version's ONLY in
		//     `description` (prose edit, reflow, whitespace),
		//     `external_refs` (backfilling or correcting trace ids) and/or
		//     `anchor_fit_note` (rewording why the fit is what it is,
		//     issue #2023) and/or `evidence_queries[].description` (query
		//     prose, issue #2891) updates the active row in place. Superseding would mint a
		//     new control id and orphan everything keyed on the old one:
		//     owners, evidence, evaluations, exceptions, risk and other
		//     links (issues #1908, #1958). Any other difference still
		//     versions, as below. Decisions:
		//     docs/audit-log/1908-control-description-in-place-decisions.md,
		//     docs/audit-log/1958-control-external-refs-in-place-decisions.md.
		if !isNew {
			same, cmpErr := manifestsEqualIgnoringMetadata([]byte(prior.BundleManifestYaml), b.ManifestYAMLRaw)
			if cmpErr != nil {
				return cmpErr
			}
			if same {
				// Keep the manifest being overwritten: the prose and refs
				// history lives append-only in control_manifest_revisions.
				n, err := q.InsertControlManifestRevision(ctx, dbx.InsertControlManifestRevisionParams{
					TenantID:  pgUUID(tenantID),
					ControlID: prior.ID,
				})
				if err != nil {
					return fmt.Errorf("record control manifest revision: %w", err)
				}
				if n != 1 {
					return ErrControlChangedConcurrently
				}
				n, err = q.UpdateControlMetadataInPlace(ctx, dbx.UpdateControlMetadataInPlaceParams{
					TenantID:           pgUUID(tenantID),
					ID:                 prior.ID,
					Description:        b.Manifest.Description,
					ExternalRefs:       refsJSON,
					BundleManifestYaml: string(b.ManifestYAMLRaw),
					BundleManifestHash: b.ManifestHashHex,
					BundleUploadedBy:   uploader,
					AnchorFitNote:      b.Manifest.AnchorFitNotePtr(),
					EvidenceQueries:    queriesJSON,
				})
				if err != nil {
					return fmt.Errorf("update control metadata in place: %w", err)
				}
				if n != 1 {
					return ErrControlChangedConcurrently
				}
				outcome, err := rebindAnchor(ctx, q, tenantID, prior, anchorID, b.Manifest.SCFRelease, b.Manifest.SCFAnchorID)
				if err != nil {
					return err
				}
				outcome.apply(&result)
				result.ControlID = uuid.UUID(prior.ID.Bytes)
				result.BundleID = b.Manifest.BundleID
				result.Version = prior.Version
				result.UpdatedInPlace = true
				return nil
			}
		}

		// 4. Insert the new row. version = prior.version + 1 (or 1 for new).
		newID := uuid.New()
		nextVersion := int32(1)
		if !isNew {
			nextVersion = prior.Version + 1
		}

		anchored := anchorID != uuid.Nil
		scfCode := b.Manifest.SCFAnchorID
		if anchored {
			if anchorRow, lookupErr := lookupAnchorRow(ctx, q, anchorID); lookupErr == nil {
				scfCode = anchorRow.ScfID
			}
		}

		family := b.Manifest.ControlFamily
		if family == "" {
			if !anchored {
				family = "UNKNOWN"
			} else if anchorRow, lookupErr := lookupAnchorRow(ctx, q, anchorID); lookupErr == nil {
				family = anchorRow.Family
			} else {
				family = "UNKNOWN"
			}
		}

		lifecycle := dbx.ControlLifecycleState(b.Manifest.LifecycleStateOrDefault())
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
		//    (migration 20260511000033) — the FK is validated at COMMIT, by
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
		var scfPtr *string
		var anchorPG pgtype.UUID // invalid (NULL) for an unanchored control
		if anchored {
			scfPtr = &scfCode
			anchorPG = pgUUID(anchorID)
		}
		row, err := q.InsertControlVersion(ctx, dbx.InsertControlVersionParams{
			ID:                   pgUUID(newID),
			TenantID:             pgUUID(tenantID),
			BundleID:             b.Manifest.BundleID,
			Version:              nextVersion,
			ScfID:                scfPtr,
			ScfAnchorID:          anchorPG,
			AnchorFit:            b.Manifest.AnchorFitOrDefault(),
			AnchorFitNote:        b.Manifest.AnchorFitNotePtr(),
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
			ExternalRefs:         refsJSON,
		})
		if err != nil {
			return fmt.Errorf("insert control version: %w", err)
		}

		result.ControlID = uuid.UUID(row.ID.Bytes)
		result.BundleID = b.Manifest.BundleID
		result.Version = nextVersion
		result.IsNewBundle = isNew
		return nil
	}()

	return result, err
}

// ActiveControl is the read-side projection returned by List. Carries the
// columns the slice-151 risk-create form's control-link multi-select needs
// to render a useful picker (id + title + family + scf code + lifecycle).
// The struct shape is deliberately minimal — adding fields later is
// additive and does not break the JSON envelope.
type ActiveControl struct {
	ID             uuid.UUID
	Title          string
	ControlFamily  string
	SCFID          string
	LifecycleState string
	BundleID       string
}

// List returns every active (non-superseded) control for the active tenant.
// RLS enforces tenant isolation — the sqlc query filters by the GUC-resolved
// tenant id (slice 033 plumbing). Returns an empty slice (never nil) when
// the tenant has no controls, matching the slice-150 empty-set convention.
func (s *Store) List(ctx context.Context) ([]ActiveControl, error) {
	out := []ActiveControl{}
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListActiveControls(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list active controls: %w", err)
		}
		for _, r := range rows {
			scfCode := ""
			if r.ScfID != nil {
				scfCode = *r.ScfID
			}
			out = append(out, ActiveControl{
				ID:             uuid.UUID(r.ID.Bytes),
				Title:          r.Title,
				ControlFamily:  r.ControlFamily,
				SCFID:          scfCode,
				LifecycleState: string(r.LifecycleState),
				BundleID:       r.BundleID,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListSortKey names one ordering of ListWithState (#2131). Each key maps
// to a fixed SQL expression in ListActiveControlsWithState; the column is
// never interpolated.
type ListSortKey string

// The ListWithState sort keys, one per column of the /controls table.
// SortDefault is failing controls first, then the stalest evidence.
const (
	SortDefault        ListSortKey = "default"
	SortSCFID          ListSortKey = "scf_id"
	SortName           ListSortKey = "name"
	SortFamily         ListSortKey = "family"
	SortState          ListSortKey = "state"
	SortFreshness      ListSortKey = "freshness"
	SortLastObservedAt ListSortKey = "last_observed_at"
	SortOwner          ListSortKey = "owner"
	SortFrameworks     ListSortKey = "frameworks"
)

var listSortKeys = map[ListSortKey]bool{
	SortSCFID: true, SortName: true, SortFamily: true, SortState: true,
	SortFreshness: true, SortLastObservedAt: true, SortOwner: true,
	SortFrameworks: true,
}

// ErrInvalidListSort is returned by ParseListSort for a sort value that is
// not on the allow-list. The HTTP layer maps it to 400.
var ErrInvalidListSort = errors.New("control: sort must be one of scf_id|name|family|state|freshness|last_observed_at|owner|frameworks, optionally prefixed with -")

// ParseListSort reads `?sort=`: empty is the default order, `key` sorts
// ascending and `-key` descending. Anything else is ErrInvalidListSort.
func ParseListSort(raw string) (ListSortKey, bool, error) {
	if raw == "" {
		return SortDefault, false, nil
	}
	desc := strings.HasPrefix(raw, "-")
	key := ListSortKey(strings.TrimPrefix(raw, "-"))
	if !listSortKeys[key] {
		return SortDefault, false, ErrInvalidListSort
	}
	return key, desc, nil
}

// ListFilter narrows and orders ListWithState. Zero fields are no filter;
// a zero Sort is SortDefault.
type ListFilter struct {
	// ScopeCell narrows the state rollup to one scope cell (slice 224).
	ScopeCell pgtype.UUID
	// AnchorFit is exact|partial|none (issue #1736).
	AnchorFit string
	// Owner is the assigned person (#2128); Unowned keeps only controls
	// with nobody assigned. The handler never sets both.
	Owner   pgtype.UUID
	Unowned bool
	// Result is the rolled-up evaluation result (an evidence_result value).
	Result string
	// Freshness is the rolled-up freshness status.
	Freshness string
	// Family matches control_family, case-insensitively.
	Family string
	// Framework is a catalog framework slug the control's anchor satisfies.
	Framework string
	// LegacyAnchorOnly keeps only controls with an open or rejected SCF
	// remap proposal (#2392).
	LegacyAnchorOnly bool
	Sort             ListSortKey
	SortDesc         bool
}

func optText(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// ListWithState returns one page of the active tenant's active controls,
// each with its own worst-state-wins evaluation rollup and its anchor's
// framework slugs, plus the total matching the filter (issues #1741,
// #2131). Every filter narrows both the page and the total.
func (s *Store) ListWithState(ctx context.Context, limit, offset int32, f ListFilter) ([]dbx.ListActiveControlsWithStateRow, int64, error) {
	sortKey := f.Sort
	if sortKey == "" {
		sortKey = SortDefault
	}
	var (
		rows  []dbx.ListActiveControlsWithStateRow
		total int64
	)
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var err error
		rows, err = q.ListActiveControlsWithState(ctx, dbx.ListActiveControlsWithStateParams{
			TenantID:         pgUUID(tenantID),
			RowLimit:         limit,
			RowOffset:        offset,
			ScopeCellID:      f.ScopeCell,
			AnchorFit:        optText(f.AnchorFit),
			OwnerUserID:      f.Owner,
			Unowned:          f.Unowned,
			Result:           optText(f.Result),
			Freshness:        optText(f.Freshness),
			Family:           optText(f.Family),
			Framework:        optText(f.Framework),
			LegacyAnchorOnly: f.LegacyAnchorOnly,
			SortKey:          string(sortKey),
			SortDesc:         f.SortDesc,
		})
		if err != nil {
			return fmt.Errorf("list active controls with state: %w", err)
		}
		total, err = q.CountActiveControls(ctx, dbx.CountActiveControlsParams{
			TenantID:         pgUUID(tenantID),
			ScopeCellID:      f.ScopeCell,
			AnchorFit:        optText(f.AnchorFit),
			OwnerUserID:      f.Owner,
			Unowned:          f.Unowned,
			Result:           optText(f.Result),
			Freshness:        optText(f.Freshness),
			Family:           optText(f.Family),
			Framework:        optText(f.Framework),
			LegacyAnchorOnly: f.LegacyAnchorOnly,
		})
		if err != nil {
			return fmt.Errorf("count active controls: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListFamilies returns the distinct families of the active tenant's active
// controls, for the /controls Family filter (#2131).
func (s *Store) ListFamilies(ctx context.Context) ([]string, error) {
	var out []string
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var err error
		out, err = q.ListActiveControlFamilies(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list active control families: %w", err)
		}
		return nil
	})
	return out, err
}

// ListOnLegacySCFAnchors returns the active tenant's active controls still
// bound to an anchor of an SCF release that is no longer current (#2176).
// The query is filtered by the explicit tenant id as well as by RLS. The
// caller applies the #1971 D3 rule (scfimport.Classify) to decide which
// rows need a person.
func (s *Store) ListOnLegacySCFAnchors(ctx context.Context) ([]dbx.ListControlsOnLegacySCFAnchorsRow, error) {
	var rows []dbx.ListControlsOnLegacySCFAnchorsRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var err error
		rows, err = q.ListControlsOnLegacySCFAnchors(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list controls on legacy scf anchors: %w", err)
		}
		return nil
	})
	return rows, err
}

// resolveSCFAnchor accepts either a uuid string or an SCF code (e.g.,
// "IAC-37") resolved in the current SCF release, and returns the anchor's
// uuid. Unknown values fail with ErrSCFAnchorUnknown.
func resolveSCFAnchor(ctx context.Context, q *dbx.Queries, ref string) (uuid.UUID, error) {
	row, _, err := bindSCFAnchor(ctx, q, "", ref)
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.UUID(row.ID.Bytes), nil
}

// bindSCFAnchor resolves a bundle's scf_anchor_id in its declared
// scf_release (#1770). With a release named, the two scfrelease errors
// (ErrSCFReleaseUnknown, ErrSCFCodeNotInRelease) pass through wrapped, so
// the caller can tell them apart. With none, any miss is
// ErrSCFAnchorUnknown, the pre-#1770 error.
func bindSCFAnchor(ctx context.Context, q scfrelease.Querier, release, ref string) (dbx.ScfAnchor, scfrelease.Release, error) {
	if ref == "" {
		return dbx.ScfAnchor{}, scfrelease.Release{}, ErrSCFAnchorUnknown
	}
	row, rel, err := scfrelease.Resolve(ctx, q, release, ref)
	switch {
	case err == nil:
		return row, rel, nil
	case release == "" && (errors.Is(err, scfrelease.ErrCodeNotInRelease) || errors.Is(err, scfrelease.ErrNoCurrentRelease)):
		return dbx.ScfAnchor{}, scfrelease.Release{}, ErrSCFAnchorUnknown
	case errors.Is(err, scfrelease.ErrUnknownRelease), errors.Is(err, scfrelease.ErrCodeNotInRelease):
		return dbx.ScfAnchor{}, scfrelease.Release{}, fmt.Errorf("control bundle: %w", err)
	default:
		return dbx.ScfAnchor{}, scfrelease.Release{}, err
	}
}

// lookupAnchorRow returns the anchor row by uuid; thin helper so the Upload
// path can hydrate family/code from the same source of truth.
func lookupAnchorRow(ctx context.Context, q *dbx.Queries, id uuid.UUID) (dbx.ScfAnchor, error) {
	return q.GetSCFAnchorByID(ctx, pgUUID(id))
}

// DemoControls returns which of ids the in-place demo seed added (#2043), so
// read surfaces and exports can mark them "demo".
func (s *Store) DemoControls(ctx context.Context, ids []uuid.UUID) (demoprov.Set, error) {
	var set demoprov.Set
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, _ uuid.UUID) error {
		var err error
		set, err = demoprov.Lookup(ctx, q, demoprov.TableControls, ids)
		return err
	})
	return set, err
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

// WithReadOnlyTenantTx runs fn inside a READ-ONLY, tenant-scoped transaction
// that is ALWAYS rolled back. It exists so the slice-574 upload gate can
// evaluate a bundle's SQL-language test fixtures (which need a real Postgres
// subtransaction — see internal/eval/sql.go) without ever writing: the tx is
// opened `READ ONLY`, the tenant RLS context is applied, fn runs, and the tx is
// discarded. This upholds constitutional invariant #2 — the gate evaluates, it
// never mutates evidence or any source-of-truth row. fn receives the live
// pgx.Tx; it must NOT attempt a write (the READ ONLY mode would reject one
// anyway).
func (s *Store) WithReadOnlyTenantTx(ctx context.Context, fn func(pgx.Tx) error) error {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("control: begin read-only tx: %w", err)
	}
	// Always roll back — this path never commits (gate is read-only).
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	return fn(tx)
}

// DefaultBundleGateMode is the policy returned when a tenant has no explicit
// value (no tenants row, or a row predating the slice-608 column). It is
// "strict" so a tenant that has done nothing keeps the safe slice-574 default.
// The api/controls package validates the returned string into its GateMode type.
const DefaultBundleGateMode = "strict"

// BundleGateMode returns the caller-tenant's control-bundle upload gate policy
// (slice 608) as the raw column string. RLS scopes the read to the caller's own
// tenant row. A missing tenants row (pgx.ErrNoRows) resolves to
// DefaultBundleGateMode — existing/bare-UUID tenants keep the slice-574
// behaviour with no backfill. The read runs in a READ-ONLY tenant tx (the gate
// is read-only; invariant #2).
func (s *Store) BundleGateMode(ctx context.Context) (string, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return "", err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return "", fmt.Errorf("control: parse tenant id: %w", err)
	}
	mode := DefaultBundleGateMode
	rerr := s.WithReadOnlyTenantTx(ctx, func(tx pgx.Tx) error {
		q := dbx.New(tx)
		got, qerr := q.GetTenantBundleGateMode(ctx, pgUUID(tenantID))
		if qerr != nil {
			if errors.Is(qerr, pgx.ErrNoRows) {
				// No tenants row for this caller — keep the default.
				return nil
			}
			return fmt.Errorf("read bundle gate mode: %w", qerr)
		}
		if got != "" {
			mode = got
		}
		return nil
	})
	if rerr != nil {
		return "", rerr
	}
	return mode, nil
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

// RemapProposalFilter narrows and pages ListRemapProposals. A nil filter
// field means any.
type RemapProposalFilter struct {
	Class    *string
	ClassNot *string
	Status   *string
	// After is the last row of the previous page; nil for the first page.
	After *RemapProposalCursor
	Limit int32
}

// RemapProposalCursor is a position in the list's order (created_at DESC,
// bundle_id, id).
type RemapProposalCursor struct {
	CreatedAt time.Time
	BundleID  string
	ID        uuid.UUID
}

// ListRemapProposals returns one page of the active tenant's SCF remap
// proposals (#1775) and the total the filter matches (#2325). The query is
// filtered by the explicit tenant id as well as by RLS.
func (s *Store) ListRemapProposals(ctx context.Context, f RemapProposalFilter) ([]dbx.ListSCFRemapProposalsRow, int64, error) {
	var (
		rows  []dbx.ListSCFRemapProposalsRow
		total int64
	)
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		params := dbx.ListSCFRemapProposalsParams{
			TenantID: pgUUID(tenantID),
			Class:    f.Class,
			ClassNot: f.ClassNot,
			Status:   f.Status,
			RowLimit: f.Limit,
		}
		if f.After != nil {
			params.AfterCreatedAt = pgtype.Timestamptz{Time: f.After.CreatedAt, Valid: true}
			params.AfterBundleID = &f.After.BundleID
			params.AfterID = pgUUID(f.After.ID)
		}
		var err error
		rows, err = q.ListSCFRemapProposals(ctx, params)
		if err != nil {
			return fmt.Errorf("list scf remap proposals: %w", err)
		}
		total, err = q.CountSCFRemapProposals(ctx, dbx.CountSCFRemapProposalsParams{
			TenantID: pgUUID(tenantID),
			Class:    f.Class,
			ClassNot: f.ClassNot,
			Status:   f.Status,
		})
		if err != nil {
			return fmt.Errorf("count scf remap proposals: %w", err)
		}
		return nil
	})
	return rows, total, err
}

// ErrControlNotFound is a control id with no row in the caller's tenant.
var ErrControlNotFound = errors.New("control: not found")

// BundleManifest returns a control's bundle_id and the control.yaml the
// store holds for it, byte for byte (#2110 Download bundle). RLS confines
// the read to the caller's tenant; an id in another tenant is
// ErrControlNotFound.
func (s *Store) BundleManifest(ctx context.Context, id uuid.UUID) (bundleID string, manifestYAML []byte, err error) {
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, qerr := q.GetControlByID(ctx, dbx.GetControlByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(qerr, pgx.ErrNoRows) {
			return ErrControlNotFound
		}
		if qerr != nil {
			return fmt.Errorf("read control: %w", qerr)
		}
		bundleID = row.BundleID
		manifestYAML = []byte(row.BundleManifestYaml)
		return nil
	})
	return bundleID, manifestYAML, err
}
