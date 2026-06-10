// Pure-Go unit coverage for the slice-671 post-seed evaluation driver's
// guard branches. These run with no Postgres and no build tag (the slice-353
// pure-Go pre-DB unit convention): the nil-pool and zero-tenant guards return
// before any DB access, so they are exercisable without real services.

package demoseed

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEvaluateSeededTenant_NilPool(t *testing.T) {
	t.Parallel()
	_, err := EvaluateSeededTenant(context.Background(), nil, uuid.New())
	if err == nil {
		t.Fatal("expected error on nil app pool")
	}
	if !strings.Contains(err.Error(), "nil app pool") {
		t.Errorf("error = %q; want it to mention the nil app pool", err.Error())
	}
}

func TestEvaluateSeededTenant_ZeroTenant(t *testing.T) {
	t.Parallel()
	// The zero-tenant guard runs only AFTER the nil-pool guard, so it needs a
	// non-nil pool to be reached. pgxpool.New connects lazily — it returns a
	// non-nil *Pool without opening a connection — so a bogus DSN gives us a
	// non-nil pool that never touches the network for this test (the guard
	// returns before any query). This isolates the zero-tenant branch from the
	// nil-pool branch.
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1:1/unused")
	if err != nil {
		t.Fatalf("pgxpool.New (lazy): %v", err)
	}
	defer pool.Close()

	_, err = EvaluateSeededTenant(context.Background(), pool, uuid.Nil)
	if err == nil {
		t.Fatal("expected error on zero tenant id")
	}
	if !strings.Contains(err.Error(), "zero tenant id") {
		t.Errorf("error = %q; want it to mention the zero tenant id", err.Error())
	}
}
