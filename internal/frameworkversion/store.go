package frameworkversion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Sentinel errors mapped to HTTP status by the admin handler.
var (
	// ErrVersionNotFound — no framework_version with that id.
	ErrVersionNotFound = errors.New("frameworkversion: version not found")
	// ErrMigrationNotFound — no review-queue row with that id.
	ErrMigrationNotFound = errors.New("frameworkversion: migration not found")
	// ErrAlreadyDecided — a review-queue row that is no longer pending.
	ErrAlreadyDecided = errors.New("frameworkversion: migration already decided")
	// ErrNotSameFramework — the two versions belong to different frameworks.
	ErrNotSameFramework = errors.New("frameworkversion: versions belong to different frameworks")
)

// Store performs the version-lifecycle + migration-suggest + approval acts
// against the catalog tables. It runs as the atlas_app role using the NARROW
// column-level grants (slice 484 D2): UPDATE(status) on framework_versions,
// UPDATE(latest_version_id) on frameworks, and SELECT/INSERT/UPDATE on the
// review queue + audit tables. No tenant GUC is applied: these are catalog
// tables with no tenant_id and no RLS.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the app pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Promotion is the result of a successful promote/revert.
type Promotion struct {
	FrameworkID     uuid.UUID
	PromotedID      uuid.UUID
	PromotedVersion string
	DemotedID       uuid.UUID // the prior version moved to legacy (zero if none)
	DemotedVersion  string
	ActorID         uuid.UUID
	CreatedAt       time.Time
}

// Promote moves the target version to `current`, demotes the framework's prior
// current version to `legacy` (the ADR's "superseded"), points
// frameworks.latest_version_id at the new version, and appends two audit rows
// (the promote + the implicit demote) — all in ONE transaction (AC-1 /
// threat-model R). Reversible via Revert. The legality (target not already
// current) is validated in Go before any write (ValidatePromotion).
func (s *Store) Promote(ctx context.Context, versionID, actorID uuid.UUID, note string) (Promotion, error) {
	var result Promotion
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		target, gErr := q.GetFrameworkVersionByIDForUpdate(ctx, pgUUID(versionID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return ErrVersionNotFound
		}
		if gErr != nil {
			return fmt.Errorf("frameworkversion: lock target version: %w", gErr)
		}

		if vErr := ValidatePromotion(StatusFromDB(target.Status)); vErr != nil {
			return vErr
		}

		// Demote the framework's current version (if any) to legacy. There is
		// at most one (the at-most-one-current invariant). Lock it too.
		var demotedID uuid.UUID
		var demotedVersion string
		cur, cErr := q.GetCurrentFrameworkVersion(ctx, target.FrameworkID)
		switch {
		case cErr == nil:
			if cur.ID != target.ID {
				if sErr := q.SetFrameworkVersionStatus(ctx, dbx.SetFrameworkVersionStatusParams{
					ID:     cur.ID,
					Status: StatusLegacy.DBStatus(),
				}); sErr != nil {
					return fmt.Errorf("frameworkversion: demote current: %w", sErr)
				}
				demotedID = uuid.UUID(cur.ID.Bytes)
				demotedVersion = cur.Version
				if _, aErr := q.InsertFrameworkVersionAudit(ctx, auditParams(
					target.FrameworkID, cur.ID, pgtype.UUID{},
					dbx.FrameworkVersionAuditActionPromote,
					ptrStatus(StatusCurrent), ptrStatus(StatusLegacy),
					actorID, "superseded by promotion of "+target.Version,
				)); aErr != nil {
					return fmt.Errorf("frameworkversion: audit demote: %w", aErr)
				}
			}
		case errors.Is(cErr, pgx.ErrNoRows):
			// No current version yet (first promotion) — nothing to demote.
		default:
			return fmt.Errorf("frameworkversion: read current version: %w", cErr)
		}

		// Promote the target to current.
		fromStatus := StatusFromDB(target.Status)
		if sErr := q.SetFrameworkVersionStatus(ctx, dbx.SetFrameworkVersionStatusParams{
			ID:     target.ID,
			Status: StatusCurrent.DBStatus(),
		}); sErr != nil {
			return fmt.Errorf("frameworkversion: promote target: %w", sErr)
		}
		if lErr := q.SetLatestVersion(ctx, dbx.SetLatestVersionParams{
			ID:              target.FrameworkID,
			LatestVersionID: target.ID,
		}); lErr != nil {
			return fmt.Errorf("frameworkversion: set latest version: %w", lErr)
		}

		audit, aErr := q.InsertFrameworkVersionAudit(ctx, auditParams(
			target.FrameworkID, target.ID, pgtype.UUID{},
			dbx.FrameworkVersionAuditActionPromote,
			ptrStatus(fromStatus), ptrStatus(StatusCurrent),
			actorID, note,
		))
		if aErr != nil {
			return fmt.Errorf("frameworkversion: audit promote: %w", aErr)
		}

		result = Promotion{
			FrameworkID:     uuid.UUID(target.FrameworkID.Bytes),
			PromotedID:      versionID,
			PromotedVersion: target.Version,
			DemotedID:       demotedID,
			DemotedVersion:  demotedVersion,
			ActorID:         actorID,
			CreatedAt:       audit.CreatedAt.Time,
		}
		return nil
	})
	if err != nil {
		return Promotion{}, err
	}
	return result, nil
}

// Revert reverses a promotion: the now-current `versionID` is demoted back to
// legacy, the supplied `priorID` (which must currently be legacy) is restored to
// current and re-pointed by latest_version_id, audited in one tx. This is the
// reversibility promise of AC-1.
func (s *Store) Revert(ctx context.Context, versionID, priorID, actorID uuid.UUID, note string) (Promotion, error) {
	if versionID == priorID {
		return Promotion{}, fmt.Errorf("%w: version and prior are identical", ErrIllegalTransition)
	}
	var result Promotion
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		current, gErr := q.GetFrameworkVersionByIDForUpdate(ctx, pgUUID(versionID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return ErrVersionNotFound
		}
		if gErr != nil {
			return fmt.Errorf("frameworkversion: lock current version: %w", gErr)
		}
		prior, pErr := q.GetFrameworkVersionByIDForUpdate(ctx, pgUUID(priorID))
		if errors.Is(pErr, pgx.ErrNoRows) {
			return ErrVersionNotFound
		}
		if pErr != nil {
			return fmt.Errorf("frameworkversion: lock prior version: %w", pErr)
		}
		if current.FrameworkID != prior.FrameworkID {
			return ErrNotSameFramework
		}
		if vErr := ValidateRevert(StatusFromDB(current.Status), StatusFromDB(prior.Status)); vErr != nil {
			return vErr
		}

		if sErr := q.SetFrameworkVersionStatus(ctx, dbx.SetFrameworkVersionStatusParams{
			ID: current.ID, Status: StatusLegacy.DBStatus(),
		}); sErr != nil {
			return fmt.Errorf("frameworkversion: demote current on revert: %w", sErr)
		}
		if sErr := q.SetFrameworkVersionStatus(ctx, dbx.SetFrameworkVersionStatusParams{
			ID: prior.ID, Status: StatusCurrent.DBStatus(),
		}); sErr != nil {
			return fmt.Errorf("frameworkversion: restore prior on revert: %w", sErr)
		}
		if lErr := q.SetLatestVersion(ctx, dbx.SetLatestVersionParams{
			ID: current.FrameworkID, LatestVersionID: prior.ID,
		}); lErr != nil {
			return fmt.Errorf("frameworkversion: re-point latest on revert: %w", lErr)
		}

		if _, aErr := q.InsertFrameworkVersionAudit(ctx, auditParams(
			current.FrameworkID, current.ID, pgtype.UUID{},
			dbx.FrameworkVersionAuditActionRevert,
			ptrStatus(StatusCurrent), ptrStatus(StatusLegacy),
			actorID, note,
		)); aErr != nil {
			return fmt.Errorf("frameworkversion: audit revert demote: %w", aErr)
		}
		audit, aErr := q.InsertFrameworkVersionAudit(ctx, auditParams(
			prior.FrameworkID, prior.ID, pgtype.UUID{},
			dbx.FrameworkVersionAuditActionRevert,
			ptrStatus(StatusLegacy), ptrStatus(StatusCurrent),
			actorID, note,
		))
		if aErr != nil {
			return fmt.Errorf("frameworkversion: audit revert restore: %w", aErr)
		}

		result = Promotion{
			FrameworkID:     uuid.UUID(prior.FrameworkID.Bytes),
			PromotedID:      priorID,
			PromotedVersion: prior.Version,
			DemotedID:       versionID,
			DemotedVersion:  current.Version,
			ActorID:         actorID,
			CreatedAt:       audit.CreatedAt.Time,
		}
		return nil
	})
	if err != nil {
		return Promotion{}, err
	}
	return result, nil
}

// SuggestMigrations runs the migration-suggest job for the (from -> to) version
// pair of ONE framework: it computes the exact-code carryovers + flags the rest
// (pure Suggest), then writes each as a 'pending' review-queue row. It NEVER
// mutates a requirement or an edge (P0-484-1 / AC-3). Idempotent: re-running for
// the same pair leaves already-queued rows untouched (ON CONFLICT DO NOTHING).
// Returns the computed summary.
func (s *Store) SuggestMigrations(ctx context.Context, fromVersionID, toVersionID uuid.UUID) (SuggestSummary, error) {
	if fromVersionID == toVersionID {
		return SuggestSummary{}, fmt.Errorf("%w: from and to are identical", ErrIllegalTransition)
	}
	var summary SuggestSummary
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		fromV, fErr := q.GetFrameworkVersionByID(ctx, pgUUID(fromVersionID))
		if errors.Is(fErr, pgx.ErrNoRows) {
			return ErrVersionNotFound
		}
		if fErr != nil {
			return fmt.Errorf("frameworkversion: read from version: %w", fErr)
		}
		toV, tErr := q.GetFrameworkVersionByID(ctx, pgUUID(toVersionID))
		if errors.Is(tErr, pgx.ErrNoRows) {
			return ErrVersionNotFound
		}
		if tErr != nil {
			return fmt.Errorf("frameworkversion: read to version: %w", tErr)
		}
		if fromV.FrameworkID != toV.FrameworkID {
			return ErrNotSameFramework
		}

		fromReqs, err := loadRefs(ctx, q, fromVersionID)
		if err != nil {
			return err
		}
		toReqs, err := loadRefs(ctx, q, toVersionID)
		if err != nil {
			return err
		}

		suggestions := Suggest(fromReqs, toReqs)
		summary = Summarize(suggestions)

		for _, sug := range suggestions {
			if _, iErr := q.InsertFrameworkVersionMigration(ctx, dbx.InsertFrameworkVersionMigrationParams{
				FrameworkID:       toV.FrameworkID,
				FromVersionID:     pgUUID(fromVersionID),
				ToVersionID:       pgUUID(toVersionID),
				FromRequirementID: optUUIDFromString(sug.FromID),
				ToRequirementID:   optUUIDFromString(sug.ToID),
				RequirementCode:   sug.Code,
				MatchKind:         dbx.FrameworkVersionMigrationMatchKind(sug.MatchKind),
			}); iErr != nil {
				return fmt.Errorf("frameworkversion: insert suggestion %s/%s: %w", sug.Code, sug.MatchKind, iErr)
			}
		}
		return nil
	})
	if err != nil {
		return SuggestSummary{}, err
	}
	return summary, nil
}

// Decision is the result of an approve/reject on one review-queue row.
type Decision struct {
	MigrationID uuid.UUID
	Status      string
	ReviewerID  uuid.UUID
	DecidedAt   time.Time
}

// DecideMigration records a human's approve/reject on ONE review-queue row and
// appends an audit row in the same tx (AC-4 / threat-model R). Only a 'pending'
// row can be decided; a double-decide returns ErrAlreadyDecided. Approval does
// NOT auto-apply the carryover to the catalog — it records human acceptance of
// the SUGGESTION (P0-484-1: the platform suggests, the human approves; applying
// the carryover edges is the loader's job under a separate human-driven import).
func (s *Store) DecideMigration(ctx context.Context, migrationID, reviewerID uuid.UUID, approve bool, note string) (Decision, error) {
	newStatus := dbx.FrameworkVersionMigrationStatusRejected
	auditAction := dbx.FrameworkVersionAuditActionMigrationReject
	if approve {
		newStatus = dbx.FrameworkVersionMigrationStatusApproved
		auditAction = dbx.FrameworkVersionAuditActionMigrationApprove
	}

	var result Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetFrameworkVersionMigrationForUpdate(ctx, pgUUID(migrationID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return ErrMigrationNotFound
		}
		if gErr != nil {
			return fmt.Errorf("frameworkversion: lock migration: %w", gErr)
		}
		if row.Status != dbx.FrameworkVersionMigrationStatusPending {
			return ErrAlreadyDecided
		}

		decided, dErr := q.SetFrameworkVersionMigrationDecision(ctx, dbx.SetFrameworkVersionMigrationDecisionParams{
			ID:         pgUUID(migrationID),
			Status:     newStatus,
			ReviewerID: pgUUID(reviewerID),
			Note:       note,
		})
		if errors.Is(dErr, pgx.ErrNoRows) {
			// Lost the race between the FOR UPDATE read and the guarded update.
			return ErrAlreadyDecided
		}
		if dErr != nil {
			return fmt.Errorf("frameworkversion: set migration decision: %w", dErr)
		}

		if _, aErr := q.InsertFrameworkVersionAudit(ctx, dbx.InsertFrameworkVersionAuditParams{
			FrameworkID:        row.FrameworkID,
			FrameworkVersionID: row.ToVersionID,
			MigrationID:        pgUUID(migrationID),
			Action:             auditAction,
			ActorID:            pgUUID(reviewerID),
			Note:               note,
		}); aErr != nil {
			return fmt.Errorf("frameworkversion: audit migration decision: %w", aErr)
		}

		result = Decision{
			MigrationID: migrationID,
			Status:      string(decided.Status),
			ReviewerID:  reviewerID,
			DecidedAt:   decided.DecidedAt.Time,
		}
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	return result, nil
}

// ListMigrations returns the review queue for one version pair (read-only).
func (s *Store) ListMigrations(ctx context.Context, fromVersionID, toVersionID uuid.UUID) ([]dbx.FrameworkVersionMigration, error) {
	rows, err := dbx.New(s.pool).ListFrameworkVersionMigrations(ctx, dbx.ListFrameworkVersionMigrationsParams{
		FromVersionID: pgUUID(fromVersionID),
		ToVersionID:   pgUUID(toVersionID),
	})
	if err != nil {
		return nil, fmt.Errorf("frameworkversion: list migrations: %w", err)
	}
	return rows, nil
}

// --- helpers ---

func loadRefs(ctx context.Context, q *dbx.Queries, versionID uuid.UUID) ([]RequirementRef, error) {
	rows, err := q.ListFrameworkVersionRequirementCodes(ctx, pgUUID(versionID))
	if err != nil {
		return nil, fmt.Errorf("frameworkversion: list requirement codes for %s: %w", versionID, err)
	}
	refs := make([]RequirementRef, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, RequirementRef{ID: uuid.UUID(r.ID.Bytes).String(), Code: r.Code})
	}
	return refs, nil
}

func auditParams(
	frameworkID, versionID, migrationID pgtype.UUID,
	action dbx.FrameworkVersionAuditAction,
	from, to *dbx.FrameworkVersionStatus,
	actorID uuid.UUID, note string,
) dbx.InsertFrameworkVersionAuditParams {
	return dbx.InsertFrameworkVersionAuditParams{
		FrameworkID:        frameworkID,
		FrameworkVersionID: versionID,
		MigrationID:        migrationID,
		Action:             action,
		FromStatus:         from,
		ToStatus:           to,
		ActorID:            pgUUID(actorID),
		Note:               note,
	}
}

func ptrStatus(s Status) *dbx.FrameworkVersionStatus {
	d := s.DBStatus()
	return &d
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("frameworkversion: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func optUUIDFromString(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}
