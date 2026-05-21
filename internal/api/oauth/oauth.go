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
//
// Slice 188 added an optional token endpoint: when AttachTokenEndpoint
// is called with a non-nil TokenEndpoint, the `/oauth/token` route
// dispatches to client-credentials + token-exchange handlers instead
// of returning a 501 stub, and the discovery document advertises the
// supported grant types.
//
// Slice 189 added an optional authorize endpoint: when
// AttachAuthorizeEndpoint is called with a non-nil AuthorizeEndpoint,
// the `/oauth/authorize` route serves the real handler (vs the 501
// stub) and the discovery document gains `authorization_code` in
// grant_types_supported.
type Handler struct {
	store          keystore.KeyStore
	cfg            Config
	tokenEP        *TokenEndpoint
	authorizeEP    *AuthorizeEndpoint
	revokeEP       *RevocationEndpoint
	introspectEP   *IntrospectionEndpoint
	deviceAuthEP   *DeviceAuthorizationEndpoint
	deviceApproval *DeviceApprovalEndpoint
	discoveryDoc   []byte
}

// New constructs a Handler. The store provides verification keys for
// JWKS; the cfg.Issuer drives every absolute URL in the discovery doc.
// The discovery JSON is pre-marshaled once here — issuer is fixed at
// process startup, so building the map per request is pure waste.
//
// To enable the real `/oauth/token` handler (slice 188), call
// AttachTokenEndpoint AFTER New but BEFORE Mount. Mount snapshots the
// then-current grant-types set into the discovery document.
func New(store keystore.KeyStore, cfg Config) *Handler {
	h := &Handler{store: store, cfg: cfg}
	h.rebuildDiscovery()
	return h
}

// AttachTokenEndpoint wires the slice-188 grant handlers and
// regenerates the discovery document with the supported grant types
// list. Optional — when nil, the `/oauth/token` route stays a 501
// stub and discovery advertises an empty grant_types_supported.
func (h *Handler) AttachTokenEndpoint(ep *TokenEndpoint) {
	h.tokenEP = ep
	h.rebuildDiscovery()
}

// AttachAuthorizeEndpoint wires the slice-189 authorize handler and
// regenerates the discovery document so `authorization_code` joins
// grant_types_supported. Optional — when nil, the `/oauth/authorize`
// route stays a 501 stub.
func (h *Handler) AttachAuthorizeEndpoint(ep *AuthorizeEndpoint) {
	h.authorizeEP = ep
	h.rebuildDiscovery()
}

// AttachRevocationEndpoint wires the slice-190 `/oauth/revoke` handler
// per RFC 7009. When attached, the discovery document is regenerated
// to advertise the revocation_endpoint_auth_methods_supported list.
// Optional — when nil, the `/oauth/revoke` route stays a 501 stub.
func (h *Handler) AttachRevocationEndpoint(ep *RevocationEndpoint) {
	h.revokeEP = ep
	h.rebuildDiscovery()
}

// AttachIntrospectionEndpoint wires the slice-190 `/oauth/introspect`
// handler per RFC 7662. When attached, the discovery document is
// regenerated to advertise the introspection_endpoint_auth_methods_supported
// list. Optional — when nil, the `/oauth/introspect` route stays a
// 501 stub.
func (h *Handler) AttachIntrospectionEndpoint(ep *IntrospectionEndpoint) {
	h.introspectEP = ep
	h.rebuildDiscovery()
}

// AttachDeviceAuthorizationEndpoint wires the slice-191 RFC 8628
// device-authorization handler. When attached, the discovery
// document advertises `device_authorization_endpoint` and adds the
// device-code grant URN to `grant_types_supported`.
func (h *Handler) AttachDeviceAuthorizationEndpoint(ep *DeviceAuthorizationEndpoint) {
	h.deviceAuthEP = ep
	h.rebuildDiscovery()
}

// AttachDeviceApprovalEndpoint wires the slice-191 internal approve
// + deny handlers. These endpoints are NOT in RFC 8628 — they are
// the atlas-internal hooks the device approval UI posts to. No
// discovery surface change because the endpoints are not part of
// the public OAuth contract.
func (h *Handler) AttachDeviceApprovalEndpoint(ep *DeviceApprovalEndpoint) {
	h.deviceApproval = ep
}

// rebuildDiscovery snapshots the current grant-type state into the
// pre-marshaled discovery JSON. Called from New, AttachTokenEndpoint,
// AttachAuthorizeEndpoint, AttachRevocationEndpoint, and
// AttachIntrospectionEndpoint.
func (h *Handler) rebuildDiscovery() {
	grantTypes := []string{}
	if h.tokenEP != nil {
		grantTypes = append(grantTypes, GrantTypeClientCredentials, GrantTypeTokenExchange)
		// Slice 189: the authorization_code grant lights up when BOTH
		// the token endpoint is wired AND the authorize endpoint has
		// been attached. Advertising the grant without the matching
		// authorize route would be dishonest to clients.
		if h.authorizeEP != nil {
			grantTypes = append(grantTypes, GrantTypeAuthorizationCode)
		}
		// Slice 191: the device-code grant lights up when BOTH the
		// token endpoint is wired AND the device-authorization
		// endpoint is attached — same honest-advertising discipline.
		if h.deviceAuthEP != nil {
			grantTypes = append(grantTypes, GrantTypeDeviceCode)
		}
	}
	doc, err := json.Marshal(discoveryDocument(h.cfg.Issuer, grantTypes,
		h.revokeEP != nil, h.introspectEP != nil, h.deviceAuthEP != nil))
	if err != nil {
		// json.Marshal of a fixed-shape map[string]any with string +
		// []string + bool values cannot fail in practice; if it does,
		// the process is broken and refusing to construct is the
		// right behavior.
		panic("oauth: marshal discovery document: " + err.Error())
	}
	h.discoveryDoc = doc
}

// Mount registers the OAuth endpoints on the supplied chi router.
// Callers MUST pass the root router — not a Mount("/") submount —
// so the two `/.well-known/*` paths sit at the absolute root per
// the standards.
func (h *Handler) Mount(r chi.Router) {
	r.Get(PathJWKS, h.serveJWKS)
	r.Get(PathDiscovery, h.serveDiscovery)
	if h.tokenEP != nil {
		r.Post(PathToken, h.tokenEP.ServeHTTP)
	} else {
		r.Post(PathToken, stubHandler("188"))
	}
	if h.authorizeEP != nil {
		r.Get(PathAuthorize, h.authorizeEP.ServeHTTP)
	} else {
		r.Get(PathAuthorize, stubHandler("189"))
	}
	if h.revokeEP != nil {
		r.Post(PathRevoke, h.revokeEP.ServeHTTP)
	} else {
		r.Post(PathRevoke, stubHandler("190"))
	}
	if h.introspectEP != nil {
		r.Post(PathIntrospect, h.introspectEP.ServeHTTP)
	} else {
		r.Post(PathIntrospect, stubHandler("190"))
	}
	if h.deviceAuthEP != nil {
		r.Post(PathDeviceAuthorization, h.deviceAuthEP.ServeHTTP)
	} else {
		r.Post(PathDeviceAuthorization, stubHandler("191"))
	}
	if h.deviceApproval != nil {
		r.Post(PathDeviceApprove, h.deviceApproval.ServeApprove)
		r.Post(PathDeviceDeny, h.deviceApproval.ServeDeny)
	} else {
		r.Post(PathDeviceApprove, stubHandler("191"))
		r.Post(PathDeviceDeny, stubHandler("191"))
	}
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
// other slices in the spine can extend it (188 appended
// `client_credentials` + token-exchange to grant_types_supported when
// the token endpoint is wired).
//
// Slice 188 also tightened token_endpoint_auth_methods_supported to
// `client_secret_post` only — the slice-188 handler accepts secrets
// in the form body but does NOT implement the HTTP Basic
// authentication scheme (`client_secret_basic`). Advertising what we
// don't accept would be dishonest to clients. RFC 6749 §2.3.1
// recommends accepting BOTH, but does not REQUIRE it; future-slice
// work can re-add basic auth if operator demand surfaces.
func discoveryDocument(issuer string, grantTypesSupported []string, revocationActive, introspectionActive, deviceAuthActive bool) map[string]any {
	doc := map[string]any{
		"issuer":                                issuer,
		"jwks_uri":                              issuer + PathJWKS,
		"token_endpoint":                        issuer + PathToken,
		"authorization_endpoint":                issuer + PathAuthorize,
		"revocation_endpoint":                   issuer + PathRevoke,
		"introspection_endpoint":                issuer + PathIntrospect,
		"grant_types_supported":                 grantTypesSupported,
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
		"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
	}
	// Slice 190: only advertise the revocation + introspection auth
	// method lists when the corresponding endpoints are wired. RFC
	// 8414 §2 allows omitting both fields when those endpoints are
	// not implemented; stubbed-only deployments stay quiet.
	if revocationActive {
		doc["revocation_endpoint_auth_methods_supported"] = []string{
			"client_secret_basic", "client_secret_post",
		}
	}
	if introspectionActive {
		doc["introspection_endpoint_auth_methods_supported"] = []string{
			"client_secret_basic", "client_secret_post",
		}
	}
	// Slice 191: advertise the device-authorization endpoint when
	// it's wired. RFC 8628 §4 mandates the
	// `device_authorization_endpoint` discovery field for AS that
	// implement the grant.
	if deviceAuthActive {
		doc["device_authorization_endpoint"] = issuer + PathDeviceAuthorization
	}
	return doc
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
