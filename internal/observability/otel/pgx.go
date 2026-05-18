// pgx tracer wiring (slice 121, Phase 3 — AC-8/9/10).
//
// Wraps the standard pgxpool.New flow so every SQL query becomes a child
// span under the request span. Uses otelpgx (github.com/exaring/otelpgx),
// the established library — no custom wrapper.
//
// AC-9 / AC-10 enforced via:
//
//   - WithIncludeQueryParameters is NOT enabled, so the otelpgx default
//     behaviour applies: db.statement keeps the parameterized form with
//     $N placeholders, parameter LITERAL values are NEVER attribute-recorded.
//   - The db.connection_string attribute is not emitted by otelpgx; only
//     db.system / db.operation / db.statement / db.name. The DSN never
//     touches an attribute. (AC-9.)

package otel

import (
	"context"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewTracedPool parses dsn, attaches the otelpgx tracer to the pgxpool
// config, and dials. Behaves identically to pgxpool.New when the OTel
// global TracerProvider is the no-op (AC-2): the tracer still runs but
// emits to /dev/null.
//
// The signature matches pgxpool.New so callers (cmd/atlas) substitute it
// with a one-line change.
func NewTracedPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("otel: parse pgx dsn: %w", err)
	}
	// AC-8: every connection in the pool gets the tracer. AC-9: default
	// attribute shape (db.system=postgresql, db.operation, db.statement
	// parameterized).
	//
	// AC-10: we deliberately do NOT pass otelpgx.WithIncludeQueryParameters().
	// otelpgx's default is to NOT inline parameter values; the SQL text
	// in db.statement keeps the $N placeholders. The integration test
	// in this package asserts no literal leaks.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	return pgxpool.NewWithConfig(ctx, cfg)
}
