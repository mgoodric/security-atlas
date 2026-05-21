// Package oauth implements the public-surface scaffolding of the
// atlas OAuth 2.0 Authorization Server.
//
// Slice 187 lands ONLY the two standards-mandated unauthenticated
// endpoints — JWKS (RFC 7517) and OIDC discovery (RFC 8414) — plus
// 501-stub responses for the four future endpoints the discovery
// document advertises (`/oauth/token`, `/oauth/authorize`,
// `/oauth/revoke`, `/oauth/introspect`). Real handlers for those
// endpoints land in slices 188-192.
//
// The package is mounted into the main HTTP server via Mount(router)
// using direct chi `Get`/`Post` calls — the chi router rejects a
// second top-level `Mount("/")`, so the established parallel-batch
// convention is to register routes directly on the root.
//
// JWKS + discovery MUST be reachable WITHOUT an auth context (RFC 8414
// §3: "The configuration information is intended to be retrieved
// without authentication"). The slice-190 R2 middleware that will gate
// `/v1/*` MUST allowlist these two paths.
package oauth

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// PathJWKS is the JWKS endpoint path (RFC 7517).
const PathJWKS = "/.well-known/jwks.json"

// PathDiscovery is the OIDC discovery endpoint path (OIDC Discovery 1.0).
const PathDiscovery = "/.well-known/openid-configuration"

// PathToken / PathAuthorize / PathRevoke / PathIntrospect are the
// future OAuth endpoint paths. Slice 187 returns 501 on each; the
// discovery document advertises them so clients can plan against the
// stable URL shape.
const (
	PathToken      = "/oauth/token"
	PathAuthorize  = "/oauth/authorize"
	PathRevoke     = "/oauth/revoke"
	PathIntrospect = "/oauth/introspect"
	cacheMaxAge    = 3600
)

// Config holds the values the discovery document needs at request
// time. Issuer is the externally-reachable URL clients see — it MUST
// match the `iss` claim of every JWT the platform mints.
type Config struct {
	Issuer string
}

// Handler bundles the keystore + the discovery config and exposes the
// HTTP entrypoints via Mount. The discovery JSON is pre-marshaled at
// construction time so the GET hot path is a single Write call.
type Handler struct {
	store        keystore.KeyStore
	discoveryDoc []byte
}

// New constructs a Handler. The store provides verification keys for
// JWKS; the cfg.Issuer drives every absolute URL in the discovery doc.
// The discovery JSON is pre-marshaled once here — issuer is fixed at
// process startup, so building the map per request is pure waste.
func New(store keystore.KeyStore, cfg Config) *Handler {
	doc, err := json.Marshal(discoveryDocument(cfg.Issuer))
	if err != nil {
		// json.Marshal of a fixed-shape map[string]any with string +
		// []string + bool values cannot fail in practice; if it does,
		// the process is broken and refusing to construct is the
		// right behavior.
		panic("oauth: marshal discovery document: " + err.Error())
	}
	return &Handler{store: store, discoveryDoc: doc}
}

// Mount registers the slice-187 OAuth endpoints on the supplied chi
// router. Callers MUST pass the root router — not a Mount("/") submount
// — so the two `/.well-known/*` paths sit at the absolute root per
// the standards.
func (h *Handler) Mount(r chi.Router) {
	r.Get(PathJWKS, h.serveJWKS)
	r.Get(PathDiscovery, h.serveDiscovery)
	r.Post(PathToken, stubHandler("188"))
	r.Get(PathAuthorize, stubHandler("189"))
	r.Post(PathRevoke, stubHandler("190"))
	r.Post(PathIntrospect, stubHandler("190"))
}

// serveJWKS returns the verification-key set as a JSON Web Key Set
// (RFC 7517). Only public-key halves appear; private material is never
// serialised by this path.
func (h *Handler) serveJWKS(w http.ResponseWriter, r *http.Request) {
	_, vks, err := h.store.Get(r.Context())
	if err != nil {
		http.Error(w, "keystore unavailable", http.StatusInternalServerError)
		return
	}
	keys := make([]jose.JSONWebKey, 0, len(vks))
	for _, vk := range vks {
		keys = append(keys, jose.JSONWebKey{
			Key:       vk.Key,
			KeyID:     vk.KeyID,
			Algorithm: tokensign.SignatureAlgorithmString,
			Use:       "sig",
		})
	}
	set := jose.JSONWebKeySet{Keys: keys}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cacheControlMaxAge(cacheMaxAge))
	_ = json.NewEncoder(w).Encode(set)
}

// serveDiscovery returns the pre-marshaled OIDC discovery document
// (RFC 8414 + OIDC Discovery 1.0). The shape is the load-bearing
// public contract of slice 187.
func (h *Handler) serveDiscovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cacheControlMaxAge(cacheMaxAge))
	_, _ = w.Write(h.discoveryDoc)
}

// discoveryDocument is split out so tests can call it directly and
// other slices in the spine can extend it (188 will append
// `client_credentials` to grant_types_supported, etc.).
func discoveryDocument(issuer string) map[string]any {
	return map[string]any{
		"issuer":                                issuer,
		"jwks_uri":                              issuer + PathJWKS,
		"token_endpoint":                        issuer + PathToken,
		"authorization_endpoint":                issuer + PathAuthorize,
		"revocation_endpoint":                   issuer + PathRevoke,
		"introspection_endpoint":                issuer + PathIntrospect,
		"grant_types_supported":                 []string{},
		"id_token_signing_alg_values_supported": []string{tokensign.SignatureAlgorithmString},
		"subject_types_supported":               []string{"public"},
		"scopes_supported":                      []string{"openid"},
		"claims_supported": []string{
			"iss", "sub", "aud", "exp", "iat", "jti",
			"atlas:idp_issuer",
			"atlas:current_tenant_id",
			"atlas:available_tenants",
			"atlas:roles",
			"atlas:super_admin",
		},
		"response_types_supported":              []string{"code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	}
}

// stubHandler returns a `slice_pending` 501 response pointing at the
// future slice that will land the real handler. P0-187-9 requires the
// discovery document to be honest about what's stubbed; the body
// surfaces the same honesty to direct callers.
func stubHandler(slice string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "slice_pending",
			"slice": slice,
		})
	}
}

func cacheControlMaxAge(seconds int) string {
	// public so caching intermediaries (CDNs, browsers) can cache; no
	// auth context required to fetch.
	return "public, max-age=" + strconv.Itoa(seconds)
}
