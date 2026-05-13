package featureflag

import (
	"context"
	"encoding/json"
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

// TestGateDisabledReturns404 asserts the middleware short-circuits with
// 404 when the flag is off. AC-5 + AC-22 + AC-23.
func TestGateDisabledReturns404(t *testing.T) {
	looker := &fakeLooker{
		answers: map[string]Flag{
			"oscal.export": {Key: "oscal.export", Enabled: false},
		},
	}
	handler := Gate(looker, "oscal.export")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("inner handler reached on disabled flag")
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/oscal/export/ssp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
	if got := rec.Header().Get("X-Feature-Disabled"); got != "oscal.export" {
		t.Errorf("X-Feature-Disabled = %q; want %q", got, "oscal.export")
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "feature disabled" {
		t.Errorf("body.error = %q; want %q", body["error"], "feature disabled")
	}
}

// TestGateEnabledPassesThrough asserts the middleware does not interfere
// when the flag is on. AC-5 happy path.
func TestGateEnabledPassesThrough(t *testing.T) {
	looker := &fakeLooker{
		answers: map[string]Flag{
			"risk.enabled": {Key: "risk.enabled", Enabled: true},
		},
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	handler := Gate(looker, "risk.enabled")(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/risks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (gate should pass through)", rec.Code)
	}
	if rec.Header().Get("X-Feature-Disabled") != "" {
		t.Errorf("X-Feature-Disabled header set on enabled gate; should be absent")
	}
}

// TestGateUnknownKeyReturns500 asserts a misconfigured Gate(key) call
// surfaces as a server error rather than silently allowing or denying.
func TestGateUnknownKeyReturns500(t *testing.T) {
	looker := &fakeLooker{answers: map[string]Flag{}}
	handler := Gate(looker, "nonexistent.feature")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/v1/nope", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500 (unknown gate key is a code bug)", rec.Code)
	}
}

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
