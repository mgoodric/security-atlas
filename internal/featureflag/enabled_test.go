package featureflag

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fakeLooker counts calls and returns canned answers. Used to assert
// memoization (single call per context) without a DB.
type fakeLooker struct {
	calls   atomic.Int64
	answers map[string]Flag
	err     error
}

func (f *fakeLooker) Get(_ context.Context, key string) (Flag, error) {
	f.calls.Add(1)
	if f.err != nil {
		return Flag{}, f.err
	}
	if flag, ok := f.answers[key]; ok {
		return flag, nil
	}
	return Flag{}, ErrNotFound
}

// #2548: EnabledStrict hands a flag-store error back instead of the seed
// default Enabled answers with. risk.enabled defaults on, so a fallback
// would read as "switched on".
func TestEnabledStrictReturnsStoreError(t *testing.T) {
	t.Parallel()
	looker := &fakeLooker{err: ErrStoreUnavailable}
	if on, err := Enabled(context.Background(), looker, "risk.enabled"); err != nil || !on {
		t.Fatalf("Enabled = %v, %v; want the seed default (true), nil", on, err)
	}
	before := StrictReadErrors()
	on, err := EnabledStrict(context.Background(), looker, "risk.enabled")
	if !errors.Is(err, ErrStoreUnavailable) || on {
		t.Fatalf("EnabledStrict = %v, %v; want false, ErrStoreUnavailable", on, err)
	}
	// #2575: the failed read is counted.
	if StrictReadErrors() <= before {
		t.Fatal("StrictReadErrors did not count the failed read")
	}
}

// TestEnabledMemoizesInContext asserts the in-request cache returns the
// first lookup's value on subsequent calls. AC-4 + anti-criterion (no
// N+1 lookups per request).
func TestEnabledMemoizesInContext(t *testing.T) {
	looker := &fakeLooker{
		answers: map[string]Flag{
			"risk.enabled": {Key: "risk.enabled", Enabled: true},
		},
	}
	ctx := WithCache(context.Background())

	for i := 0; i < 3; i++ {
		on, err := Enabled(ctx, looker, "risk.enabled")
		if err != nil {
			t.Fatalf("call %d: Enabled returned error: %v", i+1, err)
		}
		if !on {
			t.Fatalf("call %d: Enabled = false; want true", i+1)
		}
	}
	if got := looker.calls.Load(); got != 1 {
		t.Errorf("Looker.Get called %d times; want 1 (in-request memoization broken)", got)
	}
}

// TestEnabledNoCacheStillWorks asserts the helper still returns the
// correct value without a request cache (each call hits the Looker).
func TestEnabledNoCacheStillWorks(t *testing.T) {
	looker := &fakeLooker{
		answers: map[string]Flag{
			"vendor.enabled": {Key: "vendor.enabled", Enabled: false},
		},
	}
	ctx := context.Background() // no WithCache

	for i := 0; i < 2; i++ {
		on, err := Enabled(ctx, looker, "vendor.enabled")
		if err != nil {
			t.Fatalf("Enabled returned error: %v", err)
		}
		if on {
			t.Fatalf("Enabled = true; want false")
		}
	}
	if got := looker.calls.Load(); got != 2 {
		t.Errorf("Looker.Get called %d times; want 2 (no cache means no memoization)", got)
	}
}

// TestEnabledUnknownKeyReturnsErrNotFound asserts a typo'd key surfaces
// as an error (not a silent false). Code-bug surface.
func TestEnabledUnknownKeyReturnsErrNotFound(t *testing.T) {
	looker := &fakeLooker{answers: map[string]Flag{}}
	_, err := Enabled(context.Background(), looker, "nonexistent.key")
	if err == nil {
		t.Fatalf("Enabled returned nil error for unknown key; want ErrNotFound")
	}
}

// TestEnabledFallsBackToSeedDefaultOnDBError simulates a DB error and
// asserts the helper returns the Seed default without an error. AC-A2.
func TestEnabledFallsBackToSeedDefaultOnDBError(t *testing.T) {
	// Looker returns a non-ErrNotFound error (simulating a DB failure
	// that Store.Get could not classify as "missing row").
	looker := &fakeLooker{err: errAny{}}

	// Known seed key -- Enabled should return the Seed default and nil.
	on, err := Enabled(context.Background(), looker, "risk.enabled")
	if err != nil {
		t.Fatalf("Enabled returned error on DB failure: %v; want nil + seed default", err)
	}
	def, _ := DefaultByKey("risk.enabled")
	if on != def.Enabled {
		t.Errorf("Enabled fell back to %v; want seed default %v", on, def.Enabled)
	}
}

type errAny struct{}

func (errAny) Error() string { return "simulated DB failure" }

// TestCacheMiddlewareAttachesCache asserts the middleware wires the
// per-request cache so downstream handlers see memoization.
func TestCacheMiddlewareAttachesCache(t *testing.T) {
	var sawCache bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cacheFrom(r.Context()) != nil {
			sawCache = true
		}
	})
	handler := CacheMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !sawCache {
		t.Errorf("CacheMiddleware did not attach a request cache")
	}
}
