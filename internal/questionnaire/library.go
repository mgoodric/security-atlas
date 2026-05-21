// Slice 155 — AnswerLibrary suggestion lookup.
//
// Decision D6 carves this query out of sqlc. The shape ("most-recent-N
// for this anchor", with an optional LIMIT and a stable secondary
// ordering for ties) is shoehorned through sqlc v1.31.1 with no benefit
// over a 20-line raw pgx query, and the raw form keeps the suggestion
// path well-clear of the generated CRUD surface.
//
// RLS posture (canvas invariant #6): the query goes through atlas_app
// via the pgx pool — the same RLS policies that scope INSERTs scope the
// SELECT here. A tenant cannot see another tenant's library entries
// even if it knew an anchor id to ask about. T-5 asserts this end-to-end.
package questionnaire

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultSuggestionLimit is the default page size for SuggestForAnchor.
// 10 covers the realistic library-depth ceiling for the tracer-bullet
// scope; larger result sets would imply a library-management UI which
// is a v2 follow-on.
const DefaultSuggestionLimit = 10

// Suggestion is one prior canonical answer surfaced by the library.
type Suggestion struct {
	ID            string
	ScfAnchorID   string
	CanonicalText string
	SourceLabel   string
	UpdatedAt     time.Time
}

// LibraryReader is the minimal pgx-backed interface for suggestion
// lookup. Real callers pass a *pgxpool.Pool; tests use a stub.
type LibraryReader interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// SuggestForAnchor returns up to `limit` prior canonical answers for
// the given SCF anchor, most-recent-first (D2). Tenant scoping is
// enforced by RLS — caller MUST have set `app.current_tenant` on the
// connection via internal/tenancy.SetCurrentTenant.
//
// Returns an empty slice (NOT an error) when no priors exist — the
// "no library entries yet" case is the common one for a fresh tenant.
func SuggestForAnchor(ctx context.Context, db LibraryReader, anchorID string, limit int) ([]Suggestion, error) {
	if anchorID == "" {
		return nil, errors.New("questionnaire: SuggestForAnchor requires non-empty anchorID")
	}
	if limit <= 0 || limit > 100 {
		limit = DefaultSuggestionLimit
	}

	const sql = `
		SELECT id::text, scf_anchor_id, canonical_text, source_label, updated_at
		FROM answer_library
		WHERE scf_anchor_id = $1
		ORDER BY updated_at DESC, id ASC
		LIMIT $2
	`
	rows, err := db.Query(ctx, sql, anchorID, limit)
	if err != nil {
		return nil, fmt.Errorf("questionnaire: suggest lookup: %w", err)
	}
	defer rows.Close()

	out := make([]Suggestion, 0, limit)
	for rows.Next() {
		var s Suggestion
		if err := rows.Scan(&s.ID, &s.ScfAnchorID, &s.CanonicalText, &s.SourceLabel, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("questionnaire: scan suggestion: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("questionnaire: row iter: %w", err)
	}
	return out, nil
}

// Ensure *pgxpool.Pool satisfies the interface at compile time. The
// blank-identifier var keeps this purely a type assertion — it never
// runs.
var _ LibraryReader = (*pgxpool.Pool)(nil)
