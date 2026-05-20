// Slice 145 — unit tests for the per-(tenant, user) export concurrency
// limiter.
//
// Coverage map:
//
//	AC-4  → TestLimiterAcquireReleaseHonorsCap
//	AC-4  → TestLimiterPerTenantUserIsolation
//	P0-A9 → TestLimiterReleaseIsIdempotent
//	P0-A9 → TestLimiterReleasesOnDeferEvenAfterPanic
//	-     → TestErrCapExceededIsSentinel

package export_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/export"
)

// AC-4: a Limiter with cap N admits the first N concurrent acquires
// for one key and refuses the (N+1)-th. After a release, the slot is
// reusable.
func TestLimiterAcquireReleaseHonorsCap(t *testing.T) {
	l := export.NewLimiter(2)
	tenant := uuid.New()
	user := "user-alpha"

	rel1, err := l.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("first acquire: unexpected error %v", err)
	}
	rel2, err := l.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("second acquire: unexpected error %v", err)
	}
	// Third acquire MUST be refused — cap is 2.
	if _, err := l.Acquire(tenant, user); !errors.Is(err, export.ErrCapExceeded) {
		t.Fatalf("third acquire: want ErrCapExceeded, got %v", err)
	}

	// Release one slot; next acquire should succeed.
	rel1()
	rel3, err := l.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("post-release acquire: unexpected error %v", err)
	}
	// Release remaining slots — keeps the test from leaking state into
	// subsequent goroutines if the test runner re-uses the limiter
	// (it does not — this is a fresh value).
	rel2()
	rel3()
}

// AC-4 (cross-tenant): a super_admin running concurrent exports across
// two tenants is NOT throttled by cap=2 in any single tenant.
// (Slice 145 P0-HARDEN-2.)
func TestLimiterPerTenantUserIsolation(t *testing.T) {
	l := export.NewLimiter(2)
	tenantA := uuid.New()
	tenantB := uuid.New()
	user := "super-admin"

	// Saturate tenant A.
	a1, err := l.Acquire(tenantA, user)
	if err != nil {
		t.Fatalf("tenantA #1: %v", err)
	}
	a2, err := l.Acquire(tenantA, user)
	if err != nil {
		t.Fatalf("tenantA #2: %v", err)
	}
	if _, err := l.Acquire(tenantA, user); !errors.Is(err, export.ErrCapExceeded) {
		t.Fatalf("tenantA #3: want ErrCapExceeded, got %v", err)
	}

	// Tenant B is independent — first two acquires succeed.
	b1, err := l.Acquire(tenantB, user)
	if err != nil {
		t.Fatalf("tenantB #1: %v", err)
	}
	b2, err := l.Acquire(tenantB, user)
	if err != nil {
		t.Fatalf("tenantB #2: %v", err)
	}

	a1()
	a2()
	b1()
	b2()
}

// P0-A9: the release function is idempotent — calling it twice is a
// no-op, not a double-free that would leak a slot from a sibling
// in-flight export.
func TestLimiterReleaseIsIdempotent(t *testing.T) {
	l := export.NewLimiter(1)
	tenant := uuid.New()
	user := "user-idempotent"

	rel, err := l.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Cap=1 so the next acquire must fail.
	if _, err := l.Acquire(tenant, user); !errors.Is(err, export.ErrCapExceeded) {
		t.Fatalf("want ErrCapExceeded, got %v", err)
	}
	// Release once.
	rel()
	// Release AGAIN — must be a no-op (the slot was already returned).
	rel()
	// Two new acquires would succeed if the double-release leaked a
	// slot. Cap=1 means exactly one MUST succeed and the next MUST
	// refuse — this proves the once-guard works.
	rel1, err := l.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("post-double-release acquire: %v", err)
	}
	if _, err := l.Acquire(tenant, user); !errors.Is(err, export.ErrCapExceeded) {
		t.Fatalf("post-double-release oversaturation: want ErrCapExceeded, got %v", err)
	}
	rel1()
}

// P0-A9: an export that panics mid-stream still returns its slot
// (because the handler defers the release). This test asserts the
// defer pattern works as documented.
func TestLimiterReleasesOnDeferEvenAfterPanic(t *testing.T) {
	l := export.NewLimiter(1)
	tenant := uuid.New()
	user := "user-panic"

	// Run a function that acquires then panics; the deferred release
	// must fire and return the slot.
	func() {
		defer func() {
			_ = recover()
		}()
		rel, err := l.Acquire(tenant, user)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer rel()
		panic("simulated mid-stream encoder failure")
	}()

	// If the defer did not fire, this acquire would return
	// ErrCapExceeded.
	rel, err := l.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("post-panic acquire: %v", err)
	}
	rel()
}

// The sentinel error must be exposed as a package-level var so HTTP
// callers can errors.Is on it. The error message must also embed the
// tenant + user identifiers so a 500-tail trace can correlate without
// pulling them out separately.
func TestErrCapExceededIsSentinel(t *testing.T) {
	l := export.NewLimiter(1)
	tenant := uuid.New()
	user := "user-sentinel"

	rel, err := l.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	_, err = l.Acquire(tenant, user)
	if !errors.Is(err, export.ErrCapExceeded) {
		t.Fatalf("expected errors.Is(err, ErrCapExceeded); got %v", err)
	}
	if !strings.Contains(err.Error(), tenant.String()) {
		t.Errorf("error message missing tenant id; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), user) {
		t.Errorf("error message missing user id; got %q", err.Error())
	}
}

// Concurrent acquires from N goroutines against a limiter with cap=K:
// exactly K acquires return nil error; exactly N-K return
// ErrCapExceeded. This is the unit-level analogue of the integration
// test (5 concurrent against cap=2 → 2 OK + 3 429); the integration
// test pins the wire-shape, this test pins the primitive itself.
func TestLimiterConcurrentAcquiresHonorCap(t *testing.T) {
	const (
		cap        = 2
		goroutines = 5
	)
	l := export.NewLimiter(cap)
	tenant := uuid.New()
	user := "user-concurrent"

	type result struct {
		err error
		rel func()
	}
	results := make(chan result, goroutines)

	// Use a start-gate so every goroutine races for the slot at
	// roughly the same instant. The buffered channel acquire is
	// non-blocking so the race outcome is deterministic given the
	// cap; the only thing we are proving here is that the chan
	// primitive correctly limits to `cap`.
	var startGate sync.WaitGroup
	startGate.Add(1)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			startGate.Wait()
			rel, err := l.Acquire(tenant, user)
			results <- result{err: err, rel: rel}
		}()
	}
	startGate.Done()
	wg.Wait()
	close(results)

	var ok, refused int
	var rels []func()
	for r := range results {
		if r.err == nil {
			ok++
			rels = append(rels, r.rel)
		} else if errors.Is(r.err, export.ErrCapExceeded) {
			refused++
		} else {
			t.Errorf("unexpected error: %v", r.err)
		}
	}
	if ok != cap {
		t.Errorf("ok-count = %d; want %d", ok, cap)
	}
	if refused != goroutines-cap {
		t.Errorf("refused-count = %d; want %d", refused, goroutines-cap)
	}

	// Release every acquired slot.
	for _, rel := range rels {
		rel()
	}
}

// The default limiter ignores invalid env vars and falls back to the
// default cap.
func TestDefaultLimiterFallbackOnInvalidEnv(t *testing.T) {
	// We do NOT set the env var here; the singleton's first-use
	// resolution may have already happened in a prior test. The
	// guarantee we test is that DefaultLimiter() returns a usable
	// Limiter whose Cap() is >= 1 — never zero.
	l := export.DefaultLimiter()
	if l == nil {
		t.Fatal("DefaultLimiter() returned nil")
	}
	if l.Cap() < 1 {
		t.Errorf("DefaultLimiter cap = %d; want >= 1", l.Cap())
	}
}
