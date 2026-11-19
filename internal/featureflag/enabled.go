package featureflag

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// Looker is the minimum surface Enabled() needs. *Store satisfies it.
// The interface keeps the helper testable without a DB -- unit tests can
// pass a fake Looker that returns canned Flags + counts call frequency
// for the memoization assertion.
type Looker interface {
	Get(ctx context.Context, key string) (Flag, error)
}

// requestCache is the per-request memoization map. It lives on a context
// attached via WithCache; outside that, Enabled() degrades to one DB
// lookup per call (still correct, just less efficient).
//
// The cache is intentionally per-request: storing flag values at the
// process level would leak stale state across requests when an admin
// toggles a flag mid-flight. Anti-criterion P0: no cross-request cache.
type requestCache struct {
	mu sync.Mutex
	v  map[string]bool
}

type cacheKey struct{}

// WithCache attaches a fresh in-request memoization cache to ctx. The
// httpserver wires this in a middleware (one cache per request, dies
// when the request ends). Tests that want to assert memoization wrap
// their context with WithCache before calling Enabled twice.
func WithCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheKey{}, &requestCache{v: map[string]bool{}})
}

func cacheFrom(ctx context.Context) *requestCache {
	v, _ := ctx.Value(cacheKey{}).(*requestCache)
	return v
}

// Enabled returns the effective boolean for (ctx tenant, key).
//
//   - If ctx carries a requestCache (via WithCache) and the key is
//     present, returns the cached value (no DB hit).
//   - Otherwise hits Looker.Get and caches the result on the way out.
//
// On Looker error, Enabled returns the Seed default and SWALLOWS the
// error (logged inside Store.Get). The boolean returned is best-effort
// available; the error return is reserved for ErrNotFound (which
// indicates a registry key missing from Seed -- a code bug, not a
// runtime degradation).
//
// Anti-criterion P0: a DB-down Looker MUST NOT fail closed here. The
// returned bool is the Seed default; the returned error is nil. Only an
// unknown-key lookup surfaces a non-nil error. ModuleGate is the one
// exception: it reads through lookup and answers 503 rather than guess a
// tenant's module switch from the default (#2339).
func Enabled(ctx context.Context, looker Looker, key string) (bool, error) {
	on, err := lookup(ctx, looker, key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		// Already logged inside Store.Get. Fall back to the Seed default
		// for the key rather than disable the feature.
		if def, ok := DefaultByKey(key); ok {
			return def.Enabled, nil
		}
	}
	return on, err
}

// EnabledStrict is Enabled without the Seed-default fallback: a
// flag-store error comes back as an error (wrapping ErrStoreUnavailable)
// rather than as the default. Work with no request behind it (the
// reminder notifiers, the digests, the board pack generator) reads
// through it and skips the tenant on an error, because a missed reminder
// costs less than one sent for a switched-off module (#2548). Request
// paths keep Enabled.
func EnabledStrict(ctx context.Context, looker Looker, key string) (bool, error) {
	return lookup(ctx, looker, key)
}

// lookup is Enabled without the Seed-default fallback: a flag-store error
// comes back as an error. ErrNotFound (a key missing from Seed) is a code
// bug; any other error means the tenant's stored value is unknown.
func lookup(ctx context.Context, looker Looker, key string) (bool, error) {
	if cache := cacheFrom(ctx); cache != nil {
		cache.mu.Lock()
		if v, ok := cache.v[key]; ok {
			cache.mu.Unlock()
			return v, nil
		}
		cache.mu.Unlock()
	}
	flag, err := looker.Get(ctx, key)
	if err != nil {
		return false, err
	}
	if cache := cacheFrom(ctx); cache != nil {
		cache.mu.Lock()
		cache.v[key] = flag.Enabled
		cache.mu.Unlock()
	}
	return flag.Enabled, nil
}

// CacheMiddleware attaches a fresh per-request memoization cache to
// every incoming request. Wire it in the HTTP server's middleware
// stack BEFORE ModuleGate so the gate and the handlers downstream see the
// same cache instance.
func CacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithCache(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
