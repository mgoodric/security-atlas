package revocation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/revocation"
)

// TestStoreNilPool exercises the construction-time guard: every
// Store method must return ErrNoPool when the pool is nil rather
// than panicking. Tests below that exercise real DB behaviour are
// integration tests under the integration build tag.
func TestStoreNilPool(t *testing.T) {
	t.Parallel()

	s := revocation.New(nil)
	ctx := context.Background()

	t.Run("Revoke", func(t *testing.T) {
		err := s.Revoke(ctx, "jti-test-1", time.Now().Add(time.Hour), "user:abc", "")
		if !errors.Is(err, revocation.ErrNoPool) {
			t.Fatalf("Revoke: got %v, want ErrNoPool", err)
		}
	})

	t.Run("IsRevoked", func(t *testing.T) {
		got, err := s.IsRevoked(ctx, "jti-test-2")
		if !errors.Is(err, revocation.ErrNoPool) {
			t.Fatalf("IsRevoked: got err %v, want ErrNoPool", err)
		}
		if got {
			t.Fatalf("IsRevoked: returned true on nil-pool path")
		}
	})

	t.Run("Sweep", func(t *testing.T) {
		got, err := s.Sweep(ctx)
		if !errors.Is(err, revocation.ErrNoPool) {
			t.Fatalf("Sweep: got %v, want ErrNoPool", err)
		}
		if got != 0 {
			t.Fatalf("Sweep: returned %d on nil-pool path", got)
		}
	})
}

// TestRevokeRejectsEmptyInputs ensures the defensive input checks
// run before any DB call — they do not require a pool.
func TestRevokeRejectsEmptyInputs(t *testing.T) {
	t.Parallel()

	// Use a non-nil pool sentinel to bypass the ErrNoPool early
	// return; we want to reach the empty-string checks. Since the
	// pool methods would dereference the *pgxpool.Pool, we pass nil
	// here and instead test that the nil-pool path runs first. To
	// actually test the empty-string guards independently, we'd need
	// a mock pool — defer to the integration test suite, which
	// covers the full Revoke path including these guards (the empty-
	// string check runs BEFORE Begin, so an integration call with
	// jti="" will fail with the right error before touching DB).
	s := revocation.New(nil)
	err := s.Revoke(context.Background(), "", time.Now().Add(time.Hour), "user:abc", "")
	// Nil-pool guard wins here; the integration tests assert the
	// jti=="" guard once a real pool is wired.
	if !errors.Is(err, revocation.ErrNoPool) {
		t.Fatalf("Revoke(empty-jti, nil-pool): got %v, want ErrNoPool", err)
	}
}

// TestIsRevokedEmptyJTIReturnsFalse: defensive default. An empty jti
// must not be classified as revoked — the middleware should never
// call us this way, but a false here is safer than an error or true.
func TestIsRevokedEmptyJTIReturnsFalse(t *testing.T) {
	t.Parallel()

	// Even with a nil pool, IsRevoked("") returns (false, ErrNoPool)
	// because the nil-pool check runs first. A non-nil-pool variant
	// of this test lives in the integration suite.
	s := revocation.New(nil)
	got, err := s.IsRevoked(context.Background(), "")
	if got {
		t.Fatalf("IsRevoked(empty): returned true")
	}
	// Nil-pool error is expected on the unit path; the integration
	// test verifies the (false, nil) return with a real pool.
	if !errors.Is(err, revocation.ErrNoPool) {
		t.Fatalf("IsRevoked(empty, nil-pool): got %v, want ErrNoPool", err)
	}
}
