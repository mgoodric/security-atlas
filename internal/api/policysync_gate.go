package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
)

// policySyncPath is the one route a policy-sync key may call (#2850).
const policySyncPath = "/v1/policies:sync"

// refusePolicySyncKeys confines policy-sync API keys (#2849) to the
// policy sync endpoint, and that endpoint to policy-sync keys (#2850).
//
// On every path but policySyncPath it answers 403 to a request whose
// Authorization bearer is a policy-sync key. Anything else passes through
// untouched: a JWT, no bearer, an ordinary API key (which /v1 then refuses
// with 401, as before), or a key that does not resolve. That lookup does
// not bump the key's last_used_at.
//
// On policySyncPath it is the endpoint's authentication. A policy-sync key
// is authenticated (bumping last_used_at) and put on the request context
// as the credential, for tenancymw and the handler. Every other caller is
// refused: an unknown key with 401, and a session, a JWT or any other key,
// admin keys included, with 403. The JWT middleware skips this path, so a
// session cookie riding along cannot replace the key's credential.
//
// It runs ahead of the JWT middleware, so a policy-sync key is refused
// even when the request also carries a session cookie. It fails closed: a
// lookup error other than an unknown key (the database is unreachable)
// answers 503, so a policy-sync key is never passed on to a later
// middleware that might authenticate it another way.
func refusePolicySyncKeys(apikeys *apikeystore.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := extractBearerFromHTTP(r)
			isKey := ok && strings.HasPrefix(tok, bearer.PrefixProd)
			if r.URL.Path == policySyncPath {
				admitPolicySyncKey(apikeys, next, w, r, tok, isKey)
				return
			}
			if !isKey {
				next.ServeHTTP(w, r)
				return
			}
			cred, err := apikeys.Identify(r.Context(), tok)
			if err != nil && !errors.Is(err, apikeystore.ErrUnknownKey) {
				httperr.WriteStatus(w, r, http.StatusServiceUnavailable, "policy-sync gate: identify API key", err)
				return
			}
			if err != nil || !cred.PolicySyncOnly() {
				next.ServeHTTP(w, r)
				return
			}
			httpresp.WriteError(w, http.StatusForbidden, "a policy-sync key may only sync policies")
		})
	}
}

// admitPolicySyncKey serves policySyncPath: next runs only for a
// policy-sync key, with its credential on the context.
func admitPolicySyncKey(apikeys *apikeystore.Store, next http.Handler, w http.ResponseWriter, r *http.Request, tok string, isKey bool) {
	if !isKey {
		httpresp.WriteError(w, http.StatusForbidden, "policy sync requires a policy-sync API key")
		return
	}
	cred, err := apikeys.Authenticate(r.Context(), tok)
	if errors.Is(err, apikeystore.ErrUnknownKey) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="atlas", error="invalid_token"`)
		writeAuthError(w, "unknown, revoked or expired API key")
		return
	}
	if err != nil {
		httperr.WriteStatus(w, r, http.StatusServiceUnavailable, "policy-sync gate: authenticate API key", err)
		return
	}
	if !cred.PolicySyncOnly() {
		httpresp.WriteError(w, http.StatusForbidden, "policy sync requires a policy-sync API key")
		return
	}
	next.ServeHTTP(w, r.WithContext(authctx.WithCredential(r.Context(), cred)))
}
