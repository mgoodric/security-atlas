//go:build integration

package advisorylock_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/advisorylock"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// TestNamespacesDoNotCollide holds every registered namespace's key 0 in one
// transaction and proves a second transaction can take key 0 in no
// namespace that is held, every other namespace's key 0, and the same key
// value in the single-bigint lock space (#2273).
func TestNamespacesDoNotCollide(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	const held = advisorylock.SuperAdminDemote
	if err := advisorylock.Lock(ctx, holder, held, 0); err != nil {
		t.Fatalf("hold %d/0: %v", held, err)
	}

	for _, e := range advisorylock.All() {
		probe, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin probe: %v", err)
		}
		var got bool
		if err := probe.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1::int4, 0)`, int32(e.Namespace)).Scan(&got); err != nil {
			t.Fatalf("try %s: %v", e.Name, err)
		}
		_ = probe.Rollback(ctx)
		if want := e.Namespace != held; got != want {
			t.Errorf("try %s (%d/0) while %d/0 is held = %v, want %v", e.Name, e.Namespace, held, got, want)
		}
	}

	// The two-int key (ns, 0) is not the bigint key ns<<32: Postgres keeps
	// the two spaces apart.
	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin probe: %v", err)
	}
	defer func() { _ = probe.Rollback(ctx) }()
	var got bool
	if err := probe.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1::bigint)`, int64(held)<<32).Scan(&got); err != nil {
		t.Fatalf("try bigint: %v", err)
	}
	if !got {
		t.Errorf("bigint lock %d blocked by two-int lock %d/0", int64(held)<<32, held)
	}
}
