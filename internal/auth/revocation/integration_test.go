//go:build integration

package revocation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 435 / 742: the inline atlas_app pool boilerplate (openPool /
// DATABASE_URL_APP / pgxpool dial) this file used to re-derive now lives in
// the shared internal/dbtest harness. dbtest.NewAppPool opens the
// RLS-enforcing application-role pool — these tables are not tenant-scoped,
// so no tenant context is applied (production path exercised exactly).

// uniqueJTI returns a fresh jti string so concurrent runs do not
// collide on the PK.
func uniqueJTI(prefix string) string {
	return prefix + "-" + uuid.New().String()
}

// TestRevokeAndCheck covers the Revoke -> IsRevoked happy path.
func TestRevokeAndCheck(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := revocation.New(pool)
	ctx := context.Background()

	jti := uniqueJTI("ut-revoke")
	exp := time.Now().Add(time.Hour)

	if err := s.Revoke(ctx, jti, exp, "user:test", ""); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	got, err := s.IsRevoked(ctx, jti)
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !got {
		t.Fatalf("IsRevoked: want true after Revoke")
	}
}

// TestIsRevokedFalseForUnknown covers the negative path.
func TestIsRevokedFalseForUnknown(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := revocation.New(pool)
	ctx := context.Background()

	got, err := s.IsRevoked(ctx, uniqueJTI("ut-not-present"))
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if got {
		t.Fatalf("IsRevoked: want false for unrevoked jti")
	}
}

// TestRevokeIdempotent covers the ON CONFLICT DO UPDATE path: a
// second call must not error.
func TestRevokeIdempotent(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := revocation.New(pool)
	ctx := context.Background()

	jti := uniqueJTI("ut-idempotent")
	exp := time.Now().Add(time.Hour)

	if err := s.Revoke(ctx, jti, exp, "user:test", ""); err != nil {
		t.Fatalf("Revoke 1: %v", err)
	}
	if err := s.Revoke(ctx, jti, exp, "user:test2", ""); err != nil {
		t.Fatalf("Revoke 2 (idempotent): %v", err)
	}

	got, err := s.IsRevoked(ctx, jti)
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !got {
		t.Fatalf("IsRevoked after idempotent revoke: want true")
	}
}

// TestIsRevokedFalseAfterExpiry: a revoked row with expires_at in the
// past must NOT be reported as revoked (the JWT validator's exp check
// rejects it independently; the row is dead weight pending sweeper).
func TestIsRevokedFalseAfterExpiry(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := revocation.New(pool)
	ctx := context.Background()

	jti := uniqueJTI("ut-expired")
	pastExp := time.Now().Add(-time.Hour)

	if err := s.Revoke(ctx, jti, pastExp, "user:test", ""); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	got, err := s.IsRevoked(ctx, jti)
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if got {
		t.Fatalf("IsRevoked: want false for past-exp row")
	}
}

// TestSweepDeletesExpiredRows covers the sweeper's garbage-collection
// behaviour.
func TestSweepDeletesExpiredRows(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := revocation.New(pool)
	ctx := context.Background()

	pastJTI := uniqueJTI("ut-sweep-past")
	futureJTI := uniqueJTI("ut-sweep-future")

	if err := s.Revoke(ctx, pastJTI, time.Now().Add(-time.Hour), "user:test", ""); err != nil {
		t.Fatalf("Revoke past: %v", err)
	}
	if err := s.Revoke(ctx, futureJTI, time.Now().Add(time.Hour), "user:test", ""); err != nil {
		t.Fatalf("Revoke future: %v", err)
	}

	n, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n < 1 {
		t.Fatalf("Sweep: deleted %d rows, want at least 1", n)
	}

	// Past row is gone; future row remains.
	gotFuture, err := s.IsRevoked(ctx, futureJTI)
	if err != nil {
		t.Fatalf("IsRevoked future: %v", err)
	}
	if !gotFuture {
		t.Fatalf("IsRevoked future: want true after Sweep")
	}
}

// TestAuditLogAppended: every Revoke writes one row to
// oauth_revocation_events. Idempotent re-revoke appends a second row
// — the audit log answers "how many revocation calls have we seen
// for this jti" which is forensically valuable.
func TestAuditLogAppended(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := revocation.New(pool)
	ctx := context.Background()

	jti := uniqueJTI("ut-audit")
	exp := time.Now().Add(time.Hour)

	if err := s.Revoke(ctx, jti, exp, "user:test", "203.0.113.42"); err != nil {
		t.Fatalf("Revoke 1: %v", err)
	}
	if err := s.Revoke(ctx, jti, exp, "user:test", "203.0.113.43"); err != nil {
		t.Fatalf("Revoke 2: %v", err)
	}

	const q = `SELECT count(*) FROM oauth_revocation_events WHERE jti = $1`
	var count int
	if err := pool.QueryRow(ctx, q, jti).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("audit count = %d, want 2 (one per Revoke call)", count)
	}
}
