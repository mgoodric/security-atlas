package crosswalkedit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Store performs content edits against the catalog tables. It runs as the
// atlas_app role using the D-536b-2 column-level UPDATE (relationship_type,
// strength, rationale) grant (migration 20260804000000) — it can rewrite the
// reviewer-curated content but not provenance, the edge endpoints, or (through
// this package) the trust tier. No tenant GUC is applied: fw_to_scf_edges and
// the audit table are catalog tables with no tenant_id and no RLS.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the app pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// EditInput is the edit request. EditorID is the acting admin's atlas user id,
// taken from the verified JWT subject — never from the request body.
type EditInput struct {
	EdgeID   uuid.UUID
	EditorID uuid.UUID
	Patch    ContentPatch
	Note     string
}

// Edit is the result of a successful content edit: the audit row that was
// appended, plus the from/to content and the (unchanged) tier for the
// caller's response.
type Edit struct {
	EdgeID      uuid.UUID
	EditorID    uuid.UUID
	From        Content
	To          Content
	Note        string
	MappingTier crosswalktier.Tier
	CreatedAt   time.Time
}

// Edit rewrites an edge's STRM content and appends an immutable before/after
// audit row in the SAME transaction (threat-model R — no edit bypasses the
// trail; on any failure the tx rolls back and neither is written). The
// current content + tier are read FOR UPDATE inside the tx so a concurrent
// edit or tier transition cannot race the read-validate-write window.
// Returns:
//
//   - ErrEdgeNotFound            — no edge with that id (HTTP 404)
//   - ErrTierNotEditable         — verified/rejected edge (HTTP 409; demote
//     via the slice-483 tier endpoint first)
//   - ErrNoFields                — empty patch (HTTP 400)
//   - ErrUnknownRelationshipType — bad STRM type (HTTP 400)
//   - ErrStrengthOutOfRange      — strength outside [0, 1] (HTTP 400)
//   - ErrNoChange                — patch identical to current content (HTTP 422)
//
// The mapping_tier is never written here: an edit does not transition a tier
// in either direction (no auto-approve, no auto-demote).
func (s *Store) Edit(ctx context.Context, in EditInput) (Edit, error) {
	var result Edit
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetFwToScfEdgeContentForUpdate(ctx, pgUUID(in.EdgeID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return ErrEdgeNotFound
		}
		if gErr != nil {
			return fmt.Errorf("crosswalkedit: lock edge content: %w", gErr)
		}

		tier := crosswalktier.TierFromDB(row.MappingTier)
		if !TierEditable(tier) {
			return fmt.Errorf("%w: %s", ErrTierNotEditable, tier)
		}

		current := Content{
			RelationshipType: row.RelationshipType,
			Strength:         row.Strength,
			Rationale:        row.Rationale,
		}
		target, rErr := Resolve(current, in.Patch)
		if rErr != nil {
			return rErr
		}

		if uErr := q.UpdateFwToScfEdgeContent(ctx, dbx.UpdateFwToScfEdgeContentParams{
			ID:               pgUUID(in.EdgeID),
			RelationshipType: target.RelationshipType,
			Strength:         target.Strength,
			Rationale:        target.Rationale,
		}); uErr != nil {
			return fmt.Errorf("crosswalkedit: update edge content: %w", uErr)
		}

		audit, aErr := q.InsertFwToScfEdgeContentEdit(ctx, dbx.InsertFwToScfEdgeContentEditParams{
			EdgeID:               pgUUID(in.EdgeID),
			EditorID:             pgUUID(in.EditorID),
			FromRelationshipType: current.RelationshipType,
			ToRelationshipType:   target.RelationshipType,
			FromStrength:         current.Strength,
			ToStrength:           target.Strength,
			FromRationale:        current.Rationale,
			ToRationale:          target.Rationale,
			Note:                 in.Note,
		})
		if aErr != nil {
			return fmt.Errorf("crosswalkedit: insert content-edit audit: %w", aErr)
		}

		result = Edit{
			EdgeID:      in.EdgeID,
			EditorID:    in.EditorID,
			From:        current,
			To:          target,
			Note:        audit.Note,
			MappingTier: tier,
			CreatedAt:   audit.CreatedAt.Time,
		}
		return nil
	})
	if err != nil {
		return Edit{}, err
	}
	return result, nil
}

// ListEdits returns an edge's content-edit history (newest first). This is
// the admin/maintainer-scoped read — editor identity stays behind the admin
// boundary, mirroring the slice-483 tier-transition trail (P0-483-6).
func (s *Store) ListEdits(ctx context.Context, edgeID uuid.UUID) ([]Edit, error) {
	rows, err := dbx.New(s.pool).ListFwToScfEdgeContentEdits(ctx, pgUUID(edgeID))
	if err != nil {
		return nil, fmt.Errorf("crosswalkedit: list content edits: %w", err)
	}
	out := make([]Edit, 0, len(rows))
	for _, r := range rows {
		out = append(out, Edit{
			EdgeID:   uuid.UUID(r.EdgeID.Bytes),
			EditorID: uuid.UUID(r.EditorID.Bytes),
			From: Content{
				RelationshipType: r.FromRelationshipType,
				Strength:         r.FromStrength,
				Rationale:        r.FromRationale,
			},
			To: Content{
				RelationshipType: r.ToRelationshipType,
				Strength:         r.ToStrength,
				Rationale:        r.ToRationale,
			},
			Note:      r.Note,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return out, nil
}

// inTx runs fn inside a transaction. No tenant GUC is applied — these are
// catalog tables (no RLS).
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("crosswalkedit: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
