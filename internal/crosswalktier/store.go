package crosswalktier

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

// ErrEdgeNotFound is returned when the target edge id does not exist. The HTTP
// layer maps it to 404.
var ErrEdgeNotFound = errors.New("crosswalktier: edge not found")

// Store performs tier transitions against the catalog tables. It runs as the
// atlas_app role using the NARROW column-level UPDATE(mapping_tier) grant
// (slice 483 D1) — it can flip the trust tier but not the STRM edge content.
// No tenant GUC is applied: fw_to_scf_edges and the audit table are catalog
// tables with no tenant_id and no RLS.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the app pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Transition is the result of a successful tier change: the audit row that was
// appended, plus the from/to tiers for the caller's response.
type Transition struct {
	EdgeID     uuid.UUID
	FromTier   Tier
	ToTier     Tier
	ReviewerID uuid.UUID
	Note       string
	CreatedAt  time.Time
}

// TransitionInput is the validated transition request.
type TransitionInput struct {
	EdgeID     uuid.UUID
	ToTier     Tier
	ReviewerID uuid.UUID
	Note       string
}

// Transition moves an edge's trust tier and appends an immutable audit row in
// the SAME transaction (threat-model R / P0-483-4). The legality of the move is
// validated server-side against the state machine (P0-483-1 / threat-model T):
// the current tier is read FOR UPDATE inside the tx, the from -> to edge is
// checked, and only a legal move writes. Returns:
//
//   - ErrEdgeNotFound       — no edge with that id (HTTP 404)
//   - ErrUnknownTier        — to-tier malformed (HTTP 400; the handler should
//     have caught this, but the store is the backstop)
//   - ErrIllegalTransition  — illegal skip / move out of terminal / no-op
//     (HTTP 422)
//
// On success the tier change and the audit row are committed atomically; on any
// validation failure the tx rolls back and neither is written.
func (s *Store) Transition(ctx context.Context, in TransitionInput) (Transition, error) {
	if !in.ToTier.IsValid() {
		return Transition{}, fmt.Errorf("%w: to %q", ErrUnknownTier, in.ToTier)
	}

	var result Transition
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		// Row-lock the current tier so a concurrent transition cannot race the
		// read-validate-write window.
		row, gErr := q.GetFwToScfEdgeTierForUpdate(ctx, pgUUID(in.EdgeID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return ErrEdgeNotFound
		}
		if gErr != nil {
			return fmt.Errorf("crosswalktier: lock edge tier: %w", gErr)
		}

		from := TierFromDB(row.MappingTier)
		if vErr := ValidateTransition(from, in.ToTier); vErr != nil {
			return vErr
		}

		if sErr := q.SetFwToScfEdgeTier(ctx, dbx.SetFwToScfEdgeTierParams{
			ID:          pgUUID(in.EdgeID),
			MappingTier: in.ToTier.DBTier(),
		}); sErr != nil {
			return fmt.Errorf("crosswalktier: set edge tier: %w", sErr)
		}

		audit, aErr := q.InsertFwToScfEdgeTierTransition(ctx, dbx.InsertFwToScfEdgeTierTransitionParams{
			EdgeID:     pgUUID(in.EdgeID),
			ReviewerID: pgUUID(in.ReviewerID),
			FromTier:   from.DBTier(),
			ToTier:     in.ToTier.DBTier(),
			Note:       in.Note,
		})
		if aErr != nil {
			return fmt.Errorf("crosswalktier: insert transition audit: %w", aErr)
		}

		result = Transition{
			EdgeID:     in.EdgeID,
			FromTier:   from,
			ToTier:     in.ToTier,
			ReviewerID: in.ReviewerID,
			Note:       audit.Note,
			CreatedAt:  audit.CreatedAt.Time,
		}
		return nil
	})
	if err != nil {
		return Transition{}, err
	}
	return result, nil
}

// CurrentTier reads the current trust tier of an edge without locking. Returns
// ErrEdgeNotFound for an unknown id.
func (s *Store) CurrentTier(ctx context.Context, edgeID uuid.UUID) (Tier, error) {
	row, err := dbx.New(s.pool).GetFwToScfEdgeTier(ctx, pgUUID(edgeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrEdgeNotFound
	}
	if err != nil {
		return "", fmt.Errorf("crosswalktier: read edge tier: %w", err)
	}
	return TierFromDB(row.MappingTier), nil
}

// ListTransitions returns an edge's transition history (newest first). This is
// the admin/maintainer-scoped read — reviewer identity stays behind the admin
// boundary, never on the public /anchors payload (threat-model I / P0-483-6).
func (s *Store) ListTransitions(ctx context.Context, edgeID uuid.UUID) ([]Transition, error) {
	rows, err := dbx.New(s.pool).ListFwToScfEdgeTierTransitions(ctx, pgUUID(edgeID))
	if err != nil {
		return nil, fmt.Errorf("crosswalktier: list transitions: %w", err)
	}
	out := make([]Transition, 0, len(rows))
	for _, r := range rows {
		out = append(out, Transition{
			EdgeID:     uuid.UUID(r.EdgeID.Bytes),
			FromTier:   TierFromDB(r.FromTier),
			ToTier:     TierFromDB(r.ToTier),
			ReviewerID: uuid.UUID(r.ReviewerID.Bytes),
			Note:       r.Note,
			CreatedAt:  r.CreatedAt.Time,
		})
	}
	return out, nil
}

// inTx runs fn inside a transaction. No tenant GUC is applied — these are
// catalog tables (no RLS).
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("crosswalktier: begin tx: %w", err)
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
