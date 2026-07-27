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

// Store applies content edits against the catalog tables. It runs as the
// atlas_app role using the slice-536b column grant — relationship_type,
// strength, rationale, mapping_tier, updated_at and nothing else. No tenant GUC
// is applied: fw_to_scf_edges and both audit tables are catalog tables with no
// tenant_id and no RLS.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the app pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// EditInput is an edit request that has already been parsed but not yet
// validated. EditorID comes from the verified admin JWT at the HTTP layer,
// never from the request body (the same rule 483 applies to reviewer_id).
type EditInput struct {
	EdgeID   uuid.UUID
	Content  Content
	Note     string
	EditorID uuid.UUID
}

// Edit is the committed result: the before/after content, the audit row's
// identity, and — when the edit demoted a verified mapping — the tier move that
// accompanied it.
type Edit struct {
	EdgeID   uuid.UUID
	EditID   uuid.UUID
	From     Content
	To       Content
	Note     string
	EditorID uuid.UUID
	// TierDemotedFrom / TierDemotedTo are set only when the edit demoted a
	// verified mapping back to under_review (D-536b-1). Empty otherwise.
	TierDemotedFrom crosswalktier.Tier
	TierDemotedTo   crosswalktier.Tier
	CreatedAt       time.Time
}

// EditContent applies a reviewer's content edit and appends its immutable audit
// row in the SAME transaction (threat-model R). There is no other write path to
// the STRM content columns from the app role, so no edit can bypass the trail.
//
// The sequence inside the transaction:
//
//  1. Lock the edge FOR UPDATE and read its current content + tier. An unknown
//     id is ErrEdgeNotFound (HTTP 404).
//  2. Refuse the edit outright when the mapping sits in the terminal `rejected`
//     tier (ErrEdgeRejected, HTTP 422).
//  3. Refuse a no-op (ErrNoChange, HTTP 422) — an audit row recording no change
//     is noise in the trail.
//  4. Write the three content columns.
//  5. Append the content-edit audit row.
//  6. If the mapping was `verified`, demote it to `under_review` through
//     crosswalktier's own state machine and append THAT machine's audit row too
//     (D-536b-1). The demotion is validated by crosswalktier.ValidateTransition
//     rather than written blind, so the one state machine stays authoritative.
//
// Any failure rolls the whole transaction back: no content change without its
// audit row, and no demotion without its transition row.
//
// Nothing here promotes trust. A mapping can only reach `verified` through the
// slice-483 admin tier endpoint, driven by a human — the constitutional
// "no auto-approve its own mappings" boundary, and the slice-536 anti-criterion
// against a second approval workflow.
func (s *Store) EditContent(ctx context.Context, in EditInput) (Edit, error) {
	in.Content.Rationale = normalizeText(in.Content.Rationale)
	in.Note = normalizeText(in.Note)

	if err := in.Content.Validate(); err != nil {
		return Edit{}, err
	}
	if err := validateNote(in.Note); err != nil {
		return Edit{}, err
	}

	var result Edit
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetFwToScfEdgeContentForUpdate(ctx, pgUUID(in.EdgeID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return ErrEdgeNotFound
		}
		if gErr != nil {
			return fmt.Errorf("crosswalkedit: lock edge content: %w", gErr)
		}

		currentTier := crosswalktier.TierFromDB(row.MappingTier)
		if currentTier == crosswalktier.TierRejected {
			return ErrEdgeRejected
		}

		from := Content{
			RelationshipType: RelationshipTypeFromDB(row.RelationshipType),
			Strength:         row.Strength,
			Rationale:        row.Rationale,
		}
		if from.Equal(in.Content) {
			return ErrNoChange
		}

		if uErr := q.SetFwToScfEdgeContent(ctx, dbx.SetFwToScfEdgeContentParams{
			ID:               pgUUID(in.EdgeID),
			RelationshipType: in.Content.RelationshipType.DBType(),
			Strength:         in.Content.Strength,
			Rationale:        in.Content.Rationale,
		}); uErr != nil {
			return fmt.Errorf("crosswalkedit: set edge content: %w", uErr)
		}

		audit, aErr := q.InsertFwToScfEdgeContentEdit(ctx, dbx.InsertFwToScfEdgeContentEditParams{
			EdgeID:               pgUUID(in.EdgeID),
			EditorID:             pgUUID(in.EditorID),
			FromRelationshipType: from.RelationshipType.DBType(),
			ToRelationshipType:   in.Content.RelationshipType.DBType(),
			FromStrength:         from.Strength,
			ToStrength:           in.Content.Strength,
			FromRationale:        from.Rationale,
			ToRationale:          in.Content.Rationale,
			Note:                 in.Note,
		})
		if aErr != nil {
			return fmt.Errorf("crosswalkedit: insert content-edit audit: %w", aErr)
		}

		result = Edit{
			EdgeID:    in.EdgeID,
			EditID:    uuid.UUID(audit.ID.Bytes),
			From:      from,
			To:        in.Content,
			Note:      audit.Note,
			EditorID:  in.EditorID,
			CreatedAt: audit.CreatedAt.Time,
		}

		// D-536b-1: the edited mapping is no longer the mapping that was
		// verified, so it re-enters review. Routed through crosswalktier's
		// state machine + audit table so 483 remains the only lifecycle.
		if currentTier == crosswalktier.TierVerified {
			const demoteTo = crosswalktier.TierUnderReview
			if vErr := crosswalktier.ValidateTransition(currentTier, demoteTo); vErr != nil {
				return fmt.Errorf("crosswalkedit: demote edited verified mapping: %w", vErr)
			}
			if tErr := q.SetFwToScfEdgeTier(ctx, dbx.SetFwToScfEdgeTierParams{
				ID:          pgUUID(in.EdgeID),
				MappingTier: demoteTo.DBTier(),
			}); tErr != nil {
				return fmt.Errorf("crosswalkedit: demote edge tier: %w", tErr)
			}
			if _, tErr := q.InsertFwToScfEdgeTierTransition(ctx, dbx.InsertFwToScfEdgeTierTransitionParams{
				EdgeID:     pgUUID(in.EdgeID),
				ReviewerID: pgUUID(in.EditorID),
				FromTier:   currentTier.DBTier(),
				ToTier:     demoteTo.DBTier(),
				Note:       demotionNote(in.Note),
			}); tErr != nil {
				return fmt.Errorf("crosswalkedit: insert demotion audit: %w", tErr)
			}
			result.TierDemotedFrom = currentTier
			result.TierDemotedTo = demoteTo
		}
		return nil
	})
	if err != nil {
		return Edit{}, err
	}
	return result, nil
}

// ContentEdit is one row of an edge's content-edit history.
type ContentEdit struct {
	ID        uuid.UUID
	EdgeID    uuid.UUID
	EditorID  uuid.UUID
	From      Content
	To        Content
	Note      string
	CreatedAt time.Time
}

// ListEdits returns an edge's content-edit history, newest first. This is the
// admin/maintainer-scoped read: editor identity stays behind the admin
// boundary, never on the public /anchors payload (the slice-483 P0-483-6 line).
func (s *Store) ListEdits(ctx context.Context, edgeID uuid.UUID) ([]ContentEdit, error) {
	rows, err := dbx.New(s.pool).ListFwToScfEdgeContentEdits(ctx, pgUUID(edgeID))
	if err != nil {
		return nil, fmt.Errorf("crosswalkedit: list content edits: %w", err)
	}
	out := make([]ContentEdit, 0, len(rows))
	for _, r := range rows {
		out = append(out, ContentEdit{
			ID:       uuid.UUID(r.ID.Bytes),
			EdgeID:   uuid.UUID(r.EdgeID.Bytes),
			EditorID: uuid.UUID(r.EditorID.Bytes),
			From: Content{
				RelationshipType: RelationshipTypeFromDB(r.FromRelationshipType),
				Strength:         r.FromStrength,
				Rationale:        r.FromRationale,
			},
			To: Content{
				RelationshipType: RelationshipTypeFromDB(r.ToRelationshipType),
				Strength:         r.ToStrength,
				Rationale:        r.ToRationale,
			},
			Note:      r.Note,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return out, nil
}

// demotionNote labels the tier-transition row so the trail says WHY the
// mapping left verified. A reader of fw_to_scf_edge_tier_transitions alone can
// tell an operator-driven demotion from an edit-driven one without joining the
// content-edit table.
func demotionNote(editNote string) string {
	const prefix = "content edited (slice 536b auto-demotion)"
	if editNote == "" {
		return prefix
	}
	return prefix + ": " + editNote
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
