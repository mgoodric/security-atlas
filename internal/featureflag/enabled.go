package featureflag

import (
	"context"
	"encoding/json"
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
// indicates a typo'd key in a Gate(key) call -- a code bug, not a
// runtime degradation).
//
// Anti-criterion P0: a DB-down Looker MUST NOT fail closed. The
// returned bool is the Seed default; the returned error is nil. Only an
// unknown-key lookup surfaces a non-nil error.
func Enabled(ctx context.Context, looker Looker, key string) (bool, error) {
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
		if errors.Is(err, ErrNotFound) {
			// Unknown key -- this is a code bug (caller typo), surface
			// it. The boolean default is false so a misconfigured gate
			// fails closed UI-wise, but the error caller is expected
			// to surface a 500 rather than silently 404.
			return false, err
		}
		// Other errors -- already logged inside Store.Get. Fall back to
		// the Seed default for the key (Get does this internally and
		// returns nil; reaching here means a different error class --
		// surface it but don't disable the feature).
		if def, ok := DefaultByKey(key); ok {
			return def.Enabled, nil
		}
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
// stack BEFORE any Gate(key) middleware so handlers downstream see the
// same cache instance.
func CacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithCache(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Gate is the chi middleware factory. Wrap a route with Gate(looker, key)
// to 404 the request when the flag is disabled.
//
// Response shape:
//
//	HTTP/1.1 404 Not Found
//	X-Feature-Disabled: <key>
//	Content-Type: application/json
//
//	{"error":"feature disabled"}
//
// The 404 (instead of 403) is deliberate -- a disabled capability
// should be indistinguishable from a non-existent route to an
// unauthorized caller. The X-Feature-Disabled header is OBSERVABLE
// (operators can see which gate fired in logs) without leaking which
// keys exist to an unauthenticated caller.
//
// On unknown key (ErrNotFound from Looker), Gate returns 500 -- this
// is a code bug, not a runtime condition. The middleware should never
// be wired with a key that isn't in Seed.
func Gate(looker Looker, key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			on, err := Enabled(r.Context(), looker, key)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					http.Error(w, "feature flag misconfigured: unknown key", http.StatusInternalServerError)
					return
				}
				// Any other error from Enabled -- log already happened
				// in Store.Get. Fall open with Seed default.
				if def, ok := DefaultByKey(key); ok {
					on = def.Enabled
				}
			}
			if !on {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Feature-Disabled", key)
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "feature disabled"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
