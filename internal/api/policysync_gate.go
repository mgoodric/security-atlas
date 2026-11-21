package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
)

// refusePolicySyncKeys answers 403 to any request whose Authorization
// bearer is a policy-sync API key (#2849). A policy-sync key may only sync
// policies, and no route does that yet: the sync endpoint (#2850) is the
// one path this gate will let through.
//
// It runs ahead of the JWT middleware, so a policy-sync key is refused
// even when the request also carries a session cookie. Anything else
// passes through untouched: a JWT, no bearer, an ordinary API key (which
// /v1 then refuses with 401, as before), or a key that does not resolve.
// The lookup does not bump the key's last_used_at.
//
// It fails closed: a lookup error other than an unknown key (the database
// is unreachable) answers 503, so a policy-sync key is never passed on to
// a later middleware that might authenticate it another way.
func refusePolicySyncKeys(apikeys *apikeystore.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := extractBearerFromHTTP(r)
			if !ok || !strings.HasPrefix(tok, bearer.PrefixProd) {
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
