package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/anchors"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// registerPlatform registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerPlatform(root *chi.Mux, queries *dbx.Queries) {
	root.Mount("/", anchors.NewWithPool(queries, s.dbPool).Routes())
	// Slice 174: UCF anchor catalog data export (CSV / JSON / XLSX).
	// Reuses the slice 135 export library's CSV encoder + filename
	// builder; the nested JSON + two-sheet XLSX projections live
	// inside the anchors package (slice 174 D6). The literal-segment
	// /v1/anchors/export route sits on the trie alongside the
	// /v1/anchors/{id} pattern from anchors.Routes(); chi resolves the
	// static segment ahead of the param segment.
	anchorsExportH := anchors.NewExportHandler(s.dbPool)
	root.Get("/v1/anchors/export", anchorsExportH.ExportAnchors)

	// Slice 037: /health liveness probe. Registered after the root Mount
	// and alongside the other direct routes below — chi panics if a route
	// is added before all .Use() middleware, and registering it directly
	// on root (not via a second Mount("/")) avoids the double-mount
	// panic. It is bearer- and authz-exempt via the exemption lists
	// passed to the middleware above, so it answers with no credential.
	root.Get("/health", s.handleHealth)
	// Slice 121 (AC-15/16): opt-in Prometheus `/metrics` fallback.
	// Mounted only when cmd/atlas has wired the handler in via
	// AttachMetricsHandler (driven by ATLAS_METRICS_FALLBACK_ENABLE=true).
	// Default off (P0-A3): without the env-var the route is absent and
	// GET /metrics returns 404. When mounted, the route is bearer-exempt
	// + authz-exempt above so Prometheus can scrape it without a
	// credential — operators MUST gate this endpoint at the network layer
	// (firewall / reverse-proxy ACL / private subnet) when enabled.
	if s.metricsHandler != nil {
		root.Method(http.MethodGet, "/metrics", s.metricsHandler)
	}
	// Slice 072: GET /v1/version. Public metadata endpoint — bearer- and
	// authz-exempt above. Registered directly on the root chi router (not
	// via a second Mount("/")) to avoid the double-mount panic. Only
	// mounted when cmd/atlas has wired in Config.VersionFieldsFn; unit
	// servers that leave it nil simply don't get the route, which is fine
	// for slice-013-style fallback paths that don't need the endpoint.
	if s.versionFieldsFn != nil {
		root.Method(http.MethodGet, "/v1/version", NewVersionHandler(s.versionFieldsFn))
	}
	// Slice 073: public install-state metadata + elevated mark-first-signin
	// write. GET /v1/install-state is intentionally bearer-exempt — same
	// precedent as /health and /v1/version (P0-A4). POST
	// /v1/install/mark-first-signin requires a bearer (the user who just
	// signed in proxies through the BFF route); the bearer-auth middleware
	// above gates the path because /v1/install/* is NOT in the exempt list.
	root.Get("/v1/install-state", s.handleInstallState)
	root.Post("/v1/install/mark-first-signin", s.handleMarkFirstSignin)
	// Slice 201: POST /v1/test/issue-jwt — env-gated test-only JWT
	// issuance for the Playwright e2e harness. Mounted ONLY when
	// `ATLAS_TEST_MODE=1` at boot time. The handler ALSO re-checks the
	// env var per request (P0-201-2 defense in depth). Production
	// binaries leave the env var unset and the route is absent.
	// See `internal/api/testissuejwt.go` for the full design rationale.
	if testModeEnabled() {
		root.Post("/v1/test/issue-jwt", s.handleIssueTestJWT)
	}
	// Slice 187: OAuth Authorization Server scaffolding. The handler
	// owns six routes — JWKS, OIDC discovery, and four 501-stubs for
	// /oauth/token (188), /oauth/authorize (189), /oauth/revoke (190),
	// /oauth/introspect (190). Mounted directly on the root router
	// (NOT via a second Mount("/")) per the established
	// parallel-batch convention. Routes are bearer- and authz-exempt
	// via the exemption lists above. Only mounted when cmd/atlas has
	// wired the handler via AttachOAuthHandler — unit servers that
	// don't need the AS surface leave the routes absent.
	if s.oauthHandler != nil {
		s.oauthHandler.Mount(root)
	}
}
