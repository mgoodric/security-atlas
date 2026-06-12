package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/adminauditperiods"
	"github.com/mgoodric/security-atlas/internal/api/adminauthzbundle"
	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/admindemo"
	"github.com/mgoodric/security-atlas/internal/api/adminscim"
	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	"github.com/mgoodric/security-atlas/internal/api/adminsuperadmins"
	"github.com/mgoodric/security-atlas/internal/api/admintenants"
	"github.com/mgoodric/security-atlas/internal/api/adminusers"
	"github.com/mgoodric/security-atlas/internal/api/adminvendors"
	aggregationrulesapi "github.com/mgoodric/security-atlas/internal/api/aggregationrules"
	"github.com/mgoodric/security-atlas/internal/api/anchors"
	artifactsapi "github.com/mgoodric/security-atlas/internal/api/artifacts"
	auditapi "github.com/mgoodric/security-atlas/internal/api/audit"
	auditnotesapi "github.com/mgoodric/security-atlas/internal/api/auditnotes"
	auditperiodsapi "github.com/mgoodric/security-atlas/internal/api/auditperiods"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	boardapi "github.com/mgoodric/security-atlas/internal/api/board"
	calendarapi "github.com/mgoodric/security-atlas/internal/api/calendar"
	checklistapi "github.com/mgoodric/security-atlas/internal/api/checklist"
	controldetailapi "github.com/mgoodric/security-atlas/internal/api/controldetail"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	controlstateapi "github.com/mgoodric/security-atlas/internal/api/controlstate"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	csfassessmentapi "github.com/mgoodric/security-atlas/internal/api/csfassessment"
	dashboardapi "github.com/mgoodric/security-atlas/internal/api/dashboard"
	dashboardexportapi "github.com/mgoodric/security-atlas/internal/api/dashboardexport"
	decisionsapi "github.com/mgoodric/security-atlas/internal/api/decisions"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	exceptionsapi "github.com/mgoodric/security-atlas/internal/api/exceptions"
	featuresapi "github.com/mgoodric/security-atlas/internal/api/features"
	fwscopesapi "github.com/mgoodric/security-atlas/internal/api/frameworkscopes"
	freshnessdriftapi "github.com/mgoodric/security-atlas/internal/api/freshnessdrift"
	gapexplainapi "github.com/mgoodric/security-atlas/internal/api/gapexplain"
	mcpwriteproposalsapi "github.com/mgoodric/security-atlas/internal/api/mcpwriteproposals"
	meapi "github.com/mgoodric/security-atlas/internal/api/me"
	metricsapi "github.com/mgoodric/security-atlas/internal/api/metrics"
	orgunitsapi "github.com/mgoodric/security-atlas/internal/api/orgunits"
	oscalcomponentsapi "github.com/mgoodric/security-atlas/internal/api/oscalcomponents"
	oscalexportapi "github.com/mgoodric/security-atlas/internal/api/oscalexport"
	oscalprovenanceapi "github.com/mgoodric/security-atlas/internal/api/oscalprovenance"
	policiesapi "github.com/mgoodric/security-atlas/internal/api/policies"
	policyacksapi "github.com/mgoodric/security-atlas/internal/api/policyacks"
	questionnairesapi "github.com/mgoodric/security-atlas/internal/api/questionnaires"
	"github.com/mgoodric/security-atlas/internal/api/requestidmw"
	risksapi "github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	scimapi "github.com/mgoodric/security-atlas/internal/api/scim"
	"github.com/mgoodric/security-atlas/internal/api/scopes"
	searchapi "github.com/mgoodric/security-atlas/internal/api/search"
	"github.com/mgoodric/security-atlas/internal/api/securityheaders"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	tenantsapi "github.com/mgoodric/security-atlas/internal/api/tenants"
	themesapi "github.com/mgoodric/security-atlas/internal/api/themes"
	"github.com/mgoodric/security-atlas/internal/api/ucfcoverage"
	"github.com/mgoodric/security-atlas/internal/api/vendors"
	walkthroughsapi "github.com/mgoodric/security-atlas/internal/api/walkthroughs"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	auditperiod "github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/userprefs"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/checklist"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/csfassessment"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/gapexplain"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/notify/email"
	"github.com/mgoodric/security-atlas/internal/notify/slack"
	"github.com/mgoodric/security-atlas/internal/notify/webhook"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/qaisuggest"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
	"github.com/mgoodric/security-atlas/internal/scim"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/vendor"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// AttachDB wires a pgx pool into the server. The HTTP handlers under /v1/
// require it; absent a pool, RunHTTP returns an error. Set once at startup.
func (s *Server) AttachDB(pool *pgxpool.Pool) { s.dbPool = pool }

// HTTPHandlerForTests exposes the assembled HTTP handler so tests can
// drive it via httptest.NewServer. Production callers should use RunHTTP.
// Returns nil when no DB pool has been attached (handlers need one).
func (s *Server) HTTPHandlerForTests() http.Handler {
	if s.dbPool == nil {
		return nil
	}
	return s.httpHandler()
}

// httpHandler builds the platform's HTTP router: anchors + frameworks API
// under /v1/, auth middleware shared with the gRPC server, CORS for the
// local dev frontend.
func (s *Server) httpHandler() http.Handler {
	root := chi.NewRouter()
	// Slice 087: hardening HTTP headers (HSTS, X-Content-Type-Options,
	// X-Frame-Options, Referrer-Policy, CSP-Report-Only) must be the FIRST
	// middleware in the chain so they apply to EVERY response — including
	// the bearer-auth 401s, the /auth/* sign-in flow, /health, /v1/version,
	// and /v1/install-state. Surfaced by the 2026-Q2 security audit
	// (MEDIUM-HIGH finding); see docs/audits/2026-Q2-security-audit.md and
	// internal/api/securityheaders/middleware.go.
	root.Use(securityheaders.Middleware)
	// Slice 367: request-ID middleware sits AFTER securityheaders (so
	// security headers still apply to every response) but BEFORE
	// corsMiddleware and the JWT chain so every downstream handler
	// (including the auth-failure paths) sees a stable request ID.
	// The helper in internal/api/httperr consumes this ID to correlate
	// the client's generic-5xx response with the slog log line that
	// carries the full err.Error(). See docs/audits/327-... finding M-2
	// and docs/audit-log/367-error-detail-leakage-audit-decisions.md.
	root.Use(requestidmw.Middleware)
	root.Use(corsMiddleware)
	// Slice 190 + 197 + 326: JWT validation middleware is the SOLE
	// auth middleware on `/v1/*`. The slice 191 `legacyBearerDeprecation`
	// 410-Gone responder for `atlas_`-prefixed legacy bearers was
	// retired in slice 326 after the slice 191 → 197 → 201 cutover
	// closed. Any caller still presenting a legacy `atlas_`-prefixed
	// bearer now traverses the JWT path: `jwtmw.extractJWT` rejects
	// the shape (only `eyJ`-prefixed tokens are JWT candidates), the
	// middleware passes through, and the `requireCredential` gate
	// below terminates the request with 401. The legacy migration URL
	// remains documented in the v1.X release notes for any holder
	// still on the legacy path.
	//
	// Gated on `s.jwtSigner != nil`. Integration tests wire the signer
	// via `Server.IssueTestJWT` and pass nil for the revocation store
	// (the middleware short-circuits revocation lookup on a nil store
	// per `jwtmw.Middleware`).
	if s.jwtSigner != nil {
		// Slice 191 narrowing: the JWT bypass list previously included
		// the entire `/oauth/` prefix. Slice 191 adds two routes —
		// `/oauth/device_authorization/approve` and
		// `/oauth/device_authorization/deny` — that MUST run the JWT
		// middleware so the approving user's identity reaches the
		// handler via `authctx.CredentialFromContext`. We list the
		// public OAuth paths individually so the approval endpoints
		// fall through to JWT validation; this respects the
		// P0-191-10 invariant (no JWT enforcement on /oauth/token
		// or /.well-known) while letting the approval endpoints
		// authenticate the operator.
		root.Use(jwtBypass(jwtmw.Middleware(s.jwtSigner, s.jwtRevoked, jwtmw.Options{
			ExpectedIssuer:   s.jwtIssuer,
			ExpectedAudience: s.jwtAudience,
			CookieName:       jwtmw.DefaultCookieName,
		}), "/auth/", "/health", "/metrics", "/v1/version", "/v1/install-state",
			"/v1/calendar.ics", "/.well-known/", "/oauth/token",
			"/oauth/authorize", "/oauth/revoke", "/oauth/introspect",
			"/oauth/device_authorization", "/v1/test/issue-jwt", "/scim/"))
		// Slice 197: fail-closed credential requirement. The slice 190
		// JWT middleware passes through requests with NO JWT shape
		// (e.g. malformed bearers or no Authorization header); without
		// this gate they would reach handlers unauthenticated. This
		// middleware fires AFTER jwtmw and returns 401 on any
		// non-exempt path that has no credential in context. The
		// exempt set mirrors the JWT middleware bypass list.
		//
		// Slice 326: this gate is the post-retirement terminus for
		// legacy `atlas_`-prefixed bearers. `jwtmw.extractJWT` rejects
		// them as non-JWT shape and passes through; this middleware
		// then 401s. The slice 191 410-Gone responder is retired.
		//
		// P0-191-1 invariant restored at the platform level: there is
		// no auth-bypass window for requests with no token.
		//
		// Slice 201: `/v1/test/issue-jwt` joins the exempt set ONLY when
		// `ATLAS_TEST_MODE=1` (checked at mount time below). The route
		// MUST be bearer-exempt because its purpose is to issue the
		// first JWT — a circular dependency would prevent the Playwright
		// global-setup from ever obtaining a token.
		root.Use(requireCredential("/auth/", "/health", "/metrics", "/v1/version", "/v1/install-state",
			"/v1/calendar.ics", "/.well-known/", "/oauth/token",
			"/oauth/authorize", "/oauth/revoke", "/oauth/introspect",
			"/oauth/device_authorization", "/v1/test/issue-jwt", "/scim/"))
	}
	// Slice 033: lift the authenticated credential's tenant id onto the
	// request context so every downstream handler — and every database
	// transaction it opens — runs under the right `app.current_tenant`
	// GUC. Constitutional invariant 6 enforcement. The middleware is a
	// no-op when no credential is in context (bearer-exempt paths like
	// /auth/* keep their own request-supplied tenant resolution).
	root.Use(tenancymw.Middleware)
	// Slice 035: RBAC + ABAC via embedded OPA. Every non-exempt
	// request reaches authz.Decide; every decision (allow or deny)
	// writes one row to decision_audit_log. Attaches AFTER tenancymw
	// so the audit-log INSERT runs under the right tenant GUC.
	// Exempt prefixes mirror the bearer-auth exempt set; /health is
	// added because a liveness probe shouldn't require credentials.
	if s.authzEngine != nil {
		// Slice 072: /v1/version is added to the authz-exempt set for the
		// same reason as /health — a metadata probe shouldn't reach OPA.
		// Slice 073: /v1/install-state is added too — public metadata, same
		// reasoning as the bearer-exempt above.
		// Slice 201: `/v1/test/issue-jwt` joins the authz-exempt set
		// (mounted only when ATLAS_TEST_MODE=1) — same rationale as the
		// bearer-exempt above: the endpoint mints the first JWT, so it
		// cannot be gated by OPA on a credential it has not yet produced.
		root.Use(authzmw.Middleware(s.authzEngine, s.authzAudit, "/auth/", "/health", "/metrics", "/v1/version", "/v1/install-state", "/v1/calendar.ics", "/.well-known/", "/oauth/", "/v1/test/issue-jwt", "/scim/"))
	}
	// Slice 059: per-request feature-flag cache. Attached AFTER auth /
	// tenancy / authz so the cache lives inside the same request-context
	// every downstream handler sees. Anti-criterion P0: no cross-request
	// cache -- the cache is created fresh per request and dies when the
	// request ends.
	root.Use(featureflag.CacheMiddleware)

	// Slice 660: one shared feature-flag Store, reused by the route-gate
	// middleware (OSCAL + board), the admin features handler, and the
	// non-admin enabled-modules read. Tenant scope comes from RLS on each
	// per-request transaction (invariant #6).
	featureFlagStore := featureflag.NewStore(s.dbPool)

	queries := dbx.New(s.dbPool)
	// Slice 104: the anchors handler needs the pool (not just the
	// pre-bound queries) to open per-request tenant-GUC transactions
	// when `?include=state` is set. The non-state paths continue to use
	// the pre-bound `queries`.
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
	// Slice 008: UCF graph traversal HTTP API. Three read endpoints
	// query the requirement-anchor-control graph through the SCF spine
	// (canvas §3 / Plans/UCF_GRAPH_MODEL.md). Routes are appended
	// directly to the root chi router — adding a second
	// chi.NewRouter().Mount("/", ...) would panic. The
	// /v1/anchors/{id}/requirements route under this handler supersedes
	// the slice-006 in-memory placeholder (which was removed from
	// anchors.Routes()); the response shape is a superset of the
	// in-memory one so slice-007's TestRequirementsForAnchor still
	// passes.
	// Slice 256 — wire the eval engine, scope store, and frameworkscope
	// store so /v1/controls/{id}/coverage can carry the per-row weighted
	// `coverage` field (strength × 30-day effectiveness, intersected
	// with the framework's scope predicate). Unit tests that don't need
	// these dependencies leave the field omitted by skipping
	// AttachCoverage — the slice-008 shape stays backwards-compatible.
	ucfH := ucfcoverage.New(s.dbPool).AttachCoverage(
		eval.NewEngine(eval.NewStore(s.dbPool), scope.NewStore(s.dbPool)),
		scope.NewStore(s.dbPool),
		frameworkscope.NewStore(s.dbPool),
	)
	ucfH.RegisterRoutes(root)
	if dbSvc, ok := s.registry.(*schemaregistry.Service); ok && dbSvc != nil {
		// chi forbids two Mounts on the same path. Attach each schema
		// route directly to the root router so they live alongside the
		// anchors handlers.
		h := schemaregistry.NewHTTPHandler(dbSvc)
		root.Get("/v1/schemas", h.ListHTTP)
		root.Get("/v1/schemas/{kind}/{semver}", h.GetHTTP)
		root.Post("/v1/schemas", h.RegisterHTTP)
	}
	// Slice 017: scope dimensions, scope cells, and per-control applicability.
	// chi.Mux rejects mounting two routers on the same prefix, so the slice's
	// individual routes are wired with Method() one-by-one onto the root.
	scopesH := scopes.New(scope.NewStore(s.dbPool))
	root.Post("/v1/scopes/cells", scopesH.CreateCell)
	root.Get("/v1/scopes/cells", scopesH.ListCells)
	root.Get("/v1/scopes/dimensions", scopesH.ListDimensions)
	root.Get("/v1/controls/{id}/applicability", scopesH.ControlApplicability)
	// Slice 013: evidence ledger write API. Only mounted when an ingest
	// service has been wired in (DB-backed). Unit-only servers leave it
	// nil and exclusively use the slice-003 gRPC fallback path.
	//
	// Slice 015: when the JetStream Publisher is wired
	// (s.evidencePublisher), the handler routes pushes through the
	// stream and acks at stream-commit time (AC-2). When nil, the
	// handler falls back to direct Service.Process — the slice-013
	// path — for unit tests and dev mode without NATS.
	if s.ingestService != nil {
		evidenceH := apievidence.NewHTTPHandler(s.ingestService, s.evidencePushRate)
		if s.evidencePublisher != nil {
			evidenceH = evidenceH.WithPublisher(s.evidencePublisher)
		}
		root.Post("/v1/evidence:push", evidenceH.PushHTTP)
	}
	// Slice 019: risk register CRUD + 5x5 heatmap. Routes appended per the
	// parallel-batch convention — chi.Mux rejects two Mounts at "/", and other
	// batches are also appending here, so order-of-append must not matter.
	risksStore := risk.NewStore(s.dbPool)
	// Slice 020: the residual deriver ties risk-control links to slice 012's
	// evaluation engine. GET /v1/risks/{id} returns the derived residual +
	// effectiveness breakdown when this is attached; POST
	// /v1/risks/{id}/controls links a control and recomputes residual.
	risksDeriver := risk.NewResidualDeriver(
		risksStore,
		eval.NewEngine(eval.NewStore(s.dbPool), scope.NewStore(s.dbPool)),
	)
	risksH := risksapi.New(risksStore).WithDeriver(risksDeriver)
	root.Post("/v1/risks", risksH.CreateRisk)
	root.Get("/v1/risks", risksH.ListRisks)
	root.Get("/v1/risks/heatmap", risksH.Heatmap)
	// Slice 067: themes × org_units aggregation — slice 056's heatmap
	// panel's central data source. A literal-segment route declared
	// alongside /v1/risks/heatmap, before the generic /v1/risks/{id}, so
	// chi's declaration-order match keeps it ahead of the UUID-id route.
	root.Get("/v1/risks/theme-heatmap", risksH.ThemeHeatmap)
	// Slice 136: risk register data export (CSV / JSON / XLSX). Reuses
	// the slice 135 data-export library + slice 145 concurrency cap.
	// Literal-segment route declared before the generic /v1/risks/{id}
	// so chi's declaration-order match keeps it ahead.
	risksExportH := risksapi.NewExportHandler(s.dbPool)
	root.Get("/v1/risks/export", risksExportH.ExportRisks)
	// Slice 053: manual aggregation + live recompute. Literal-segment
	// routes (/aggregate, /{id}/aggregation) declared before the generic
	// /v1/risks/{id} so chi's declaration-order match keeps them ahead.
	root.Post("/v1/risks/aggregate", risksH.Aggregate)
	root.Get("/v1/risks/{id}/aggregation", risksH.LiveAggregation)
	// Slice 020: POST /v1/risks/{id}/controls — link a control to a risk.
	// Literal-suffix route declared before the generic /v1/risks/{id} so
	// chi's declaration-order match keeps it ahead.
	root.Post("/v1/risks/{id}/controls", risksH.LinkControl)
	root.Get("/v1/risks/{id}", risksH.GetRisk)
	root.Delete("/v1/risks/{id}", risksH.DeleteRisk)
	// Slice 053: theme catalog + per-risk theme tagging.
	themesH := themesapi.New(risksStore)
	root.Get("/v1/themes", themesH.ListVisible)
	root.Post("/v1/risks/{id}/themes", themesH.AssignThemes)
	root.Delete("/v1/risks/{id}/themes/{theme}", themesH.RemoveTheme)
	// Slice 054: declarative aggregation rules engine. Routes appended per
	// the parallel-batch convention -- chi.Mux rejects two Mounts at "/",
	// so individual routes register onto the root. The literal-segment
	// transition routes (/activate, /deactivate) are declared BEFORE the
	// bare /{id} so chi's declaration-order match keeps them ahead of the
	// generic UUID-id route. POST accepts application/json AND
	// application/yaml; rules are created `staged` and only go live via
	// the HITL PATCH .../activate.
	aggrulesH := aggregationrulesapi.New(aggrule.NewStore(s.dbPool))
	root.Post("/v1/aggregation-rules", aggrulesH.Create)
	root.Get("/v1/aggregation-rules", aggrulesH.List)
	root.Patch("/v1/aggregation-rules/{id}/activate", aggrulesH.Activate)
	root.Patch("/v1/aggregation-rules/{id}/deactivate", aggrulesH.Deactivate)
	root.Get("/v1/aggregation-rules/{id}", aggrulesH.Get)
	// Slice 053: org_unit CRUD (canvas §6.4 hierarchy).
	orgunitsH := orgunitsapi.New(risksStore)
	root.Post("/v1/org_units", orgunitsH.Create)
	root.Get("/v1/org_units", orgunitsH.List)
	root.Get("/v1/org_units/{id}", orgunitsH.Get)
	root.Patch("/v1/org_units/{id}", orgunitsH.Patch)
	root.Delete("/v1/org_units/{id}", orgunitsH.Delete)
	// Slice 024: vendor lite module — CRUD + burndown. The burndown route
	// is registered before /v1/vendors/{id} so chi's router matches the
	// literal segment first (chi resolves routes in declaration order
	// inside the same method).
	//
	// Slice 273: the same vendor.Store also backs the board-pack
	// vendor_burndown section adapter wired below. Single store = single
	// resource; the adapter reuses vendor.Store.Burndown.
	vendorStore := vendor.NewStore(s.dbPool)
	vendorsH := vendors.New(vendorStore)
	root.Post("/v1/vendors", vendorsH.CreateVendor)
	root.Get("/v1/vendors", vendorsH.ListVendors)
	root.Get("/v1/vendors/burndown", vendorsH.Burndown)
	root.Get("/v1/vendors/{id}", vendorsH.GetVendor)
	root.Patch("/v1/vendors/{id}", vendorsH.UpdateVendor)
	root.Delete("/v1/vendors/{id}", vendorsH.DeleteVendor)
	// Slice 688: per-review history ledger — list + record a completed review.
	root.Get("/v1/vendors/{id}/reviews", vendorsH.ListReviews)
	root.Post("/v1/vendors/{id}/reviews", vendorsH.RecordReview)
	// Slice 018: FrameworkScope predicate + four-state workflow + intersection
	// compute. Routes appended per the parallel-batch convention (chi rejects
	// two Mounts at "/"). The /effective-scope route lives under
	// /v1/controls/{id}/ alongside the slice-017 /applicability route.
	fwH := fwscopesapi.New(
		frameworkscope.NewStore(s.dbPool),
		scope.NewStore(s.dbPool),
	)
	root.Post("/v1/framework-scopes", fwH.Create)
	root.Get("/v1/framework-scopes", fwH.List)
	// Sub-resource transitions are PATCH, distinct from the generic PATCH
	// on /v1/framework-scopes/{id} (which edits predicate). chi resolves
	// the literal-segment routes first within the same method.
	root.Patch("/v1/framework-scopes/{id}/submit", fwH.Submit)
	root.Patch("/v1/framework-scopes/{id}/approve", fwH.Approve)
	root.Patch("/v1/framework-scopes/{id}/activate", fwH.Activate)
	root.Get("/v1/framework-scopes/{id}", fwH.Get)
	root.Patch("/v1/framework-scopes/{id}", fwH.Patch)
	root.Get("/v1/controls/{id}/effective-scope", fwH.EffectiveScope)
	// Slice 515: NIST CSF 2.0 Tier / Profile assessment workflow. Tenant-
	// confidential assessment state over the shared CSF crosswalk; edit routes
	// are grc_engineer/admin-gated inside the handler, read routes are any
	// tenant credential. The gap view reads the existing crosswalk traversal
	// (invariant #1) rather than re-storing it. Literal-segment routes
	// ({kind}/selections) are registered before the {requirement_id} variant
	// per chi's same-method resolution order.
	csfH := csfassessmentapi.New(csfassessment.NewStore(s.dbPool))
	root.Put("/v1/csf/tier", csfH.PutTier)
	root.Get("/v1/csf/tier", csfH.GetTier)
	root.Get("/v1/csf/gap", csfH.Gap)
	root.Put("/v1/csf/profiles/{kind}/selections", csfH.PutSelection)
	root.Delete("/v1/csf/profiles/{kind}/selections/{requirement_id}", csfH.DeleteSelection)
	root.Put("/v1/csf/profiles/{kind}", csfH.PutProfile)
	root.Get("/v1/csf/profiles/{kind}", csfH.GetProfile)
	// Slice 036: S3 artifact store — large-payload upload + short-TTL
	// signed download. Routes only mount when an artifact store has been
	// wired in via Server.AttachArtifactStore (or Config.ArtifactStore).
	// Unit-only servers leave it nil.
	if s.artifactStore != nil {
		artifactsH := artifactsapi.New(s.artifactStore)
		root.Post("/v1/artifacts:upload", artifactsH.Upload)
		root.Get("/v1/artifacts/{id}", artifactsH.Get)
	}
	// Slice 009: control-as-code bundle upload. Admin-only — same auth gate
	// as the schema registry's POST /v1/schemas. The handler reads either
	// multipart (a .tar.gz bundle) or JSON (inline manifest YAML) per
	// docs/spec/control-bundle.md §4.
	var controlsRegistry control.SchemaRegistry
	if dbSvc, ok := s.registry.(*schemaregistry.Service); ok && dbSvc != nil {
		controlsRegistry = dbSvc
	}
	controlsStore := control.NewStore(s.dbPool)
	controlsH := controlsapi.New(controlsStore, controlsRegistry)
	root.Post("/v1/controls:upload-bundle", controlsH.UploadBundle)
	// Slice 151: GET /v1/controls — bare tenant-control list endpoint. The
	// slice-151 risk-create form's control-link multi-select consumes this
	// to render the picker. Distinct from /v1/anchors (slice 098) which
	// returns the SCF catalog, and distinct from /v1/controls/drift /
	// /v1/controls/{id}/... — the chi router treats bare /v1/controls and
	// the /v1/controls/{id}/... patterns as separate routes.
	controlsListH := controlsapi.NewListHandler(controlsStore)
	root.Get("/v1/controls", controlsListH.List)
	// Slice 137: controls UCF graph data export (CSV / JSON / XLSX).
	// Reuses the slice 135 data-export library + slice 145 concurrency
	// cap. Literal-segment route declared before any /v1/controls/{id}/
	// patterns so chi's declaration-order match keeps it ahead. Row cap
	// is 500K (vs slice 136's 50K) per slice 137 D3 — UCF graphs at
	// multi-product orgs are large.
	controlsExportH := controlsapi.NewExportHandler(s.dbPool)
	root.Get("/v1/controls/export", controlsExportH.ExportControls)
	// Slice 175: controls history export (lineage incl. superseded
	// versions). Sibling endpoint to /v1/controls/export — same shape,
	// 17-column projection (slice 137's 15 + superseded_by +
	// superseded_at), distinct meta-audit action
	// (`controls_history_export`). Literal-segment route under
	// /v1/controls/history/... declared alongside /v1/controls/export
	// — chi matches static segments before the {id} wildcard so no
	// shadowing risk with /v1/controls/{id}/history (slice 064).
	controlsHistoryExportH := controlsapi.NewHistoryExportHandler(s.dbPool)
	root.Get("/v1/controls/history/export", controlsHistoryExportH.ExportControlsHistory)
	// Slice 011: manual control attestation endpoint. Wired only when
	// the slice-013 ingest service is wired in (this slice writes
	// evidence records via that service). When the artifact store is
	// also wired, attestations may cite an uploaded artifact_id.
	if s.ingestService != nil {
		attestH := controlsapi.NewAttestHandler(s.dbPool, s.ingestService, attestUploader(s.artifactStore))
		root.Get("/v1/controls/{id}/attest-form", attestH.AttestForm)
		root.Post("/v1/controls/{id}/attestations", attestH.Submit)
	}
	// Slice 026: sample-pull primitives. Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). The annotation sub-resource
	// route is declared after the literal /v1/samples/{id} so chi's
	// declaration-order match keeps the literal-segment first.
	auditH := auditapi.New(audit.NewStore(s.dbPool))
	root.Post("/v1/populations", auditH.CreatePopulation)
	root.Get("/v1/populations/{id}", auditH.GetPopulation)
	root.Post("/v1/samples", auditH.DrawSample)
	root.Get("/v1/samples/{id}", auditH.GetSample)
	root.Post("/v1/samples/{id}/annotations", auditH.Annotate)
	root.Get("/v1/samples/{id}/annotations", auditH.ListAnnotations)
	// Slice 028: AuditPeriod + freezing primitive. Routes appended per the
	// parallel-batch convention (chi rejects two Mounts at "/"). The
	// literal-segment routes (/freeze, /control-state, /populations/{popID})
	// are declared BEFORE the bare /{id} so chi's declaration-order match
	// keeps them ahead of the generic UUID-id route.
	periodsH := auditperiodsapi.New(auditperiod.NewStore(s.dbPool))
	root.Post("/v1/audit-periods", periodsH.Create)
	root.Get("/v1/audit-periods", periodsH.List)
	root.Post("/v1/audit-periods/{id}/freeze", periodsH.Freeze)
	root.Get("/v1/audit-periods/{id}/control-state", periodsH.ControlState)
	root.Post("/v1/audit-periods/{id}/populations/{popID}", periodsH.AttachPopulation)
	// Slice 030: OSCAL SSP + POA&M export. The literal-segment
	// /oscal-export sub-resource is declared BEFORE the bare /{id} so
	// chi's declaration-order match keeps it ahead of periodsH.Get. It
	// only mounts when the production binary has wired the Exporter via
	// AttachOscalExporter (the export needs a running Python
	// oscal-bridge); unit servers leave it nil and the route is absent.
	if s.oscalExporter != nil {
		oscalExportH := oscalexportapi.New(s.oscalExporter)
		root.Post("/v1/audit-periods/{id}/oscal-export", oscalExportH.Export)
		// Slice 457: browser download surface. Same tenant-scoped export,
		// served with a Content-Disposition: attachment header so the UI
		// can drop the signed bundle as a downloadable .json artifact. The
		// literal :download verb is declared alongside the bare
		// /oscal-export so chi's declaration-order match keeps both ahead
		// of periodsH.Get below.
		root.Post("/v1/audit-periods/{id}/oscal-export:download", oscalExportH.Download)
	}
	root.Get("/v1/audit-periods/{id}", periodsH.Get)
	// Slice 027: walkthrough recording primitive. Routes appended per the
	// parallel-batch convention (chi rejects two Mounts at "/"). The
	// attachment + finalize + export sub-resource routes are declared
	// BEFORE the bare /{id} so chi's declaration-order match keeps them
	// ahead of the generic UUID-id route. The handler 503s on
	// attachments when the artifact store isn't wired; the route still
	// mounts so OpenAPI / discovery surfaces it.
	walkthroughStore := walkthrough.NewStore(walkthrough.Config{Pool: s.dbPool})
	walkthroughsH := walkthroughsapi.New(walkthroughStore, walkthroughUploaderFor(s.artifactStore))
	root.Post("/v1/walkthroughs", walkthroughsH.Create)
	root.Get("/v1/walkthroughs", walkthroughsH.List)
	root.Post("/v1/walkthroughs/{id}/attachments", walkthroughsH.AddAttachment)
	root.Post("/v1/walkthroughs/{id}:finalize", walkthroughsH.Finalize)
	root.Get("/v1/walkthroughs/{id}/export", walkthroughsH.Export)
	root.Get("/v1/walkthroughs/{id}", walkthroughsH.Get)
	// Slice 025: auditor role + scoped read-only access.
	//
	//   POST /v1/audit-notes              auditor-only write (period assignment gated)
	//   GET  /v1/audit-notes              caller's own notes within a period
	//   GET  /v1/me/audit-period          AC-5 -- single active assignment
	//   GET  /v1/me/audit-periods         AC-6 -- all assignments
	//
	// Routes appended per the parallel-batch convention -- chi.Mux rejects
	// two Mounts at "/", so individual routes register onto the root. The
	// upstream OPA middleware (slice 035) is the primary gate; the handler
	// performs defense-in-depth on UserID / scope_type / body shape.
	// Slice 029: Audit Hub threaded comments + in-app notifications.
	// auditNotesH gets the notifications.Store so successful POSTs
	// dispatch notifications to the distinct prior-thread authors on
	// 'shared' notes. The notifications.Store is also wired into the
	// /v1/me/notifications endpoints below.
	notificationsStore := notifications.NewStore(s.dbPool)
	auditNotesH := auditnotesapi.New(notes.NewStore(s.dbPool), notificationsStore, nil)
	root.Post("/v1/audit-notes", auditNotesH.Create)
	root.Get("/v1/audit-notes", auditNotesH.List)
	// Thread retrieval is a literal sub-route declared BEFORE any
	// potential /v1/audit-notes/{id} so chi's declaration-order match
	// keeps it ahead of a bare-id route.
	root.Get("/v1/audit-notes/thread", auditNotesH.Thread)
	meAuditorStore := auditor.NewStore(s.dbPool)
	meH := meapi.New(meAuditorStore)
	root.Get("/v1/me/audit-period", meH.AuditPeriod)
	root.Get("/v1/me/audit-periods", meH.AuditPeriods)
	// Slice 029: /v1/me/notifications. Authenticated caller-scoped read
	// + mark-read surface. Routes append per the parallel-batch
	// convention (chi.Mux rejects two Mounts at "/", so individual
	// routes register onto the root). PATCH path params are resolved
	// via chi.URLParam in the handler.
	meNotificationsH := meapi.NewNotifications(notificationsStore)
	root.Get("/v1/me/notifications", meNotificationsH.List)
	root.Patch("/v1/me/notifications/{id}/read", meNotificationsH.MarkRead)
	// Slice 108: /v1/me + /v1/me/preferences + /v1/me/sessions. Each handler
	// gets its own dependency object (users store, userprefs store, sessions
	// store + dbPool for me_audit_log writes). Routes appended directly to the
	// root chi router per the parallel-batch convention. Static suffix routes
	// (/preferences, /sessions, /sessions/{id}) are declared after the bare
	// /v1/me but use distinct path shapes so there is no shadowing.
	usersStore := users.NewStore(s.dbPool)
	sessionsStore := sessions.NewStore(s.dbPool, 0)
	userprefsStore := userprefs.NewStore(s.dbPool)
	// Slice 130: share the same DBRolesResolver the slice-035 OPA engine
	// uses for `Input.UserRoles`. Building a fresh resolver here is cheap
	// (resolver is a pool wrapper, no state) and avoids threading the
	// authz.Engine's private resolver out through AttachAuthz. The shared
	// SELECT semantics + the shared `tenancy.ApplyTenant` posture are the
	// load-bearing properties; instance identity is not.
	meProfileH := meapi.NewProfile(usersStore, s.dbPool, authz.NewDBRolesResolver(s.dbPool))
	mePrefsH := meapi.NewPreferences(userprefsStore, s.dbPool)
	meSessionsH := meapi.NewSessions(sessionsStore, s.dbPool)
	root.Get("/v1/me", meProfileH.GetMe)
	root.Patch("/v1/me", meProfileH.PatchMe)
	root.Get("/v1/me/preferences", mePrefsH.GetPreferences)
	root.Patch("/v1/me/preferences", mePrefsH.PatchPreferences)
	// Slice 445: GET/PUT /v1/me/email-channel — the per-user master
	// opt-in toggle for the email delivery channel (AC-9). Default
	// opted-OUT (P0-445-7). The Channel is constructed with the SMTP
	// provider from env (inert when no SMTP host configured) + the
	// public base URL for the digest deep-link.
	emailCfg := email.ConfigFromEnv()
	emailCh := email.NewChannel(s.dbPool, email.NewSMTPProvider(emailCfg), emailCfg.BaseURL)
	// Slice 585: surface whether the OPERATOR has configured the SMTP target
	// (env present) so the settings toggle can render disabled + "not
	// configured by your administrator". Config.Enabled() is a presence check
	// only — the SMTP host/credentials never reach the wire (P0-585).
	meEmailH := meapi.NewEmailChannel(emailCh, emailCfg.Enabled())
	root.Get("/v1/me/email-channel", meEmailH.Get)
	root.Put("/v1/me/email-channel", meEmailH.Put)
	// Slice 543: GET/PUT /v1/me/slack-channel + /v1/me/webhook-channel —
	// per-user master opt-in toggles for the additional delivery channels.
	// Default opted-OUT (P0-543-3). Each channel's target + credentials are
	// OPERATOR-configured via env (inert when unset); the webhook URL is
	// SSRF-validated at construction (P0-543-2). These are SINKS only
	// (P0-543-4) — the toggle just records the per-user opt-in.
	slackCfg := slack.ConfigFromEnv()
	slackCh := slack.NewChannel(s.dbPool, slack.NewHTTPTransport(slackCfg), slackCfg.BaseURL)
	// Slice 585: slackCfg.Enabled() reports whether the operator-configured
	// Slack webhook URL env is present (presence only — the URL/token never
	// reaches the wire, P0-585 / P0-543-2). Drives the disabled toggle state.
	meSlackH := meapi.NewChannelOptIn("slack", slackCfg.Enabled(), slackCh.GetOptIn, slackCh.SetOptIn)
	root.Get("/v1/me/slack-channel", meSlackH.Get)
	root.Put("/v1/me/slack-channel", meSlackH.Put)
	webhookCfg := webhook.ConfigFromEnv()
	webhookTransport, webhookErr := webhook.NewHTTPTransport(webhookCfg, webhook.SSRFPolicy())
	if webhookErr != nil {
		// A misconfigured (internal-target) webhook URL fails fast and
		// visibly at startup rather than silently delivering to an internal
		// service (P0-543-2). The opt-in toggle still needs a channel, so
		// fall back to an inert transport and surface the config error.
		slog.Default().Error("webhook channel disabled: invalid target",
			slog.String("error", webhookErr.Error()))
		webhookTransport, _ = webhook.NewHTTPTransport(webhook.Config{}, webhook.SSRFPolicy())
	}
	webhookCh := webhook.NewChannel(s.dbPool, webhookTransport, webhookCfg.BaseURL)
	// Slice 585: the webhook is "configured" only when the env URL is present
	// AND it passed SSRF validation at construction. A present-but-invalid
	// (internal-target) URL fell back to an inert transport above, so it is
	// NOT usable — report configured=false so the toggle stays disabled.
	// Enabled() is a presence check only; the URL never reaches the wire
	// (P0-585 / P0-543-2).
	webhookConfigured := webhookCfg.Enabled() && webhookErr == nil
	meWebhookH := meapi.NewChannelOptIn("webhook", webhookConfigured, webhookCh.GetOptIn, webhookCh.SetOptIn)
	root.Get("/v1/me/webhook-channel", meWebhookH.Get)
	root.Put("/v1/me/webhook-channel", meWebhookH.Put)
	root.Get("/v1/me/sessions", meSessionsH.ListSessions)
	root.Delete("/v1/me/sessions", meSessionsH.RevokeOtherSessions)
	root.Delete("/v1/me/sessions/{id}", meSessionsH.RevokeSession)
	// Slice 192: GET /v1/me/tenants — multi-tenant directory.
	// Reads the caller's verified JWT claim
	// `atlas:available_tenants[]` and enriches with tenant names
	// from the BYPASSRLS authPool (PK-bounded query — P0-192-2).
	// When authPool is nil (test harness), the handler still
	// renders honest tenant IDs without name enrichment.
	meTenantsH := meapi.NewTenants(s.authPool)
	root.Get("/v1/me/tenants", meTenantsH.ListTenants)
	// Slice 144: PATCH /v1/tenants/{id} — rename a tenant.
	// Gated on per-tenant admin (slice-034 cred.IsAdmin OR
	// slice-187 JWT roles[CURRENT][admin]) OR super_admin
	// (slice-187 JWT atlas:super_admin claim). RLS on the
	// tenants table is the load-bearing second leg —
	// cross-tenant rename is impossible at the DB layer
	// regardless of the role gate.
	tenantsH := tenantsapi.New(s.dbPool)
	root.Patch("/v1/tenants/{id}", tenantsH.PatchTenant)
	// Slice 021: exception / waiver workflow. Routes appended per the
	// parallel-batch convention -- chi.Mux rejects two Mounts at "/", so
	// individual routes are registered onto the root. Literal-segment
	// routes (/expiring) are declared before /{id} so chi's
	// declaration-order match keeps the calendar route ahead of the
	// generic UUID-id route.
	exceptionsH := exceptionsapi.New(exception.NewStore(s.dbPool))
	root.Post("/v1/exceptions", exceptionsH.CreateException)
	root.Get("/v1/exceptions", exceptionsH.ListExceptions)
	root.Get("/v1/exceptions/expiring", exceptionsH.Expiring)
	root.Get("/v1/exceptions/{id}", exceptionsH.GetException)
	root.Get("/v1/exceptions/{id}/audit-log", exceptionsH.AuditLog)
	root.Patch("/v1/exceptions/{id}/approve", exceptionsH.Approve)
	root.Patch("/v1/exceptions/{id}/deny", exceptionsH.Deny)
	root.Patch("/v1/exceptions/{id}/activate", exceptionsH.Activate)
	// Slice 173: MCP write tools + HITL approval flow. Routes appended per
	// the parallel-batch convention (chi rejects two Mounts at "/"). The
	// MCP write tools (running in the cmd/atlas-mcp binary) call POST
	// /v1/mcp/write-proposals to file a draft; operators confirm or reject
	// via the same surface. The Store ships with the four canonical
	// Appliers (create_risk, update_control_state, push_evidence,
	// update_risk_treatment) registered; on confirm, the Applier executes
	// inside the same transaction as the state flip so a downstream-write
	// failure rolls the proposal back to state=ai_proposed.
	mcpWriteStore := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(s.dbPool))
	mcpWriteH := mcpwriteproposalsapi.New(mcpWriteStore)
	root.Post("/v1/mcp/write-proposals", mcpWriteH.CreateProposal)
	root.Get("/v1/mcp/write-proposals", mcpWriteH.ListProposals)
	root.Get("/v1/mcp/write-proposals/{id}", mcpWriteH.GetProposal)
	root.Post("/v1/mcp/write-proposals/{id}/confirm", mcpWriteH.ConfirmProposal)
	root.Post("/v1/mcp/write-proposals/{id}/reject", mcpWriteH.RejectProposal)
	// Slice 055: Decision Log CRUD + linkage (canvas Â§6.7). Routes appended
	// per the parallel-batch convention -- chi.Mux rejects two Mounts at
	// "/", so individual routes register onto the root. The literal-segment
	// route /v1/decisions/overdue is declared BEFORE the bare
	// /v1/decisions/{id} so chi's declaration-order match keeps the calendar
	// route ahead of the generic UUID-id route. The link sub-resource
	// routes are declared after the bare /{id} routes but use distinct path
	// shapes (/{id}/links/{kind}[/{targetID}]) so there is no shadowing.
	decisionsH := decisionsapi.New(decision.NewStore(s.dbPool))
	root.Post("/v1/decisions", decisionsH.CreateDecision)
	root.Get("/v1/decisions", decisionsH.ListDecisions)
	root.Get("/v1/decisions/overdue", decisionsH.Overdue)
	root.Get("/v1/decisions/{id}", decisionsH.GetDecision)
	root.Get("/v1/decisions/{id}/audit-log", decisionsH.AuditLog)
	root.Patch("/v1/decisions/{id}", decisionsH.UpdateDecision)
	root.Post("/v1/decisions/{id}/supersede", decisionsH.Supersede)
	root.Post("/v1/decisions/{id}/links/{kind}", decisionsH.AddLink)
	root.Delete("/v1/decisions/{id}/links/{kind}/{targetID}", decisionsH.RemoveLink)
	// Slice 022: policy library. Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). Sub-resource transitions
	// (submit/approve/publish) are declared before /{id} so chi's
	// declaration-order match keeps the literal-segment routes first
	// within the same method. Approve + Publish enforce IsApprover at the
	// handler (slice 034 credential flag).
	// Slice 107: NewWithPool wires the *pgxpool.Pool the
	// `?include=ack_rate` path uses (it opens a tenant-GUC-bearing tx
	// for the joined query). Backwards-compatible: callers without the
	// extension continue through the store as before.
	policiesH := policiesapi.NewWithPool(policy.NewStore(s.dbPool), s.dbPool)
	root.Post("/v1/policies", policiesH.CreatePolicy)
	root.Get("/v1/policies", policiesH.ListPolicies)
	root.Patch("/v1/policies/{id}/submit", policiesH.Submit)
	root.Patch("/v1/policies/{id}/approve", policiesH.Approve)
	root.Post("/v1/policies/{id}/publish", policiesH.Publish)
	root.Get("/v1/policies/{id}/pdf", policiesH.PDF)
	root.Get("/v1/policies/{id}", policiesH.GetPolicy)
	// Slice 023: policy acknowledgment workflow. Three routes appended
	// per the parallel-batch convention (chi rejects two Mounts at "/").
	// The literal-segment route /v1/policies/{id}/acknowledgment-rate is
	// declared before /v1/policies/{id} above would have shadowed it --
	// but slice 022 only added literal sub-resources (/submit, /approve,
	// /publish, /pdf) and the bare /{id}, so there is no shadowing risk
	// because chi resolves declaration order within the same method.
	// POST /v1/policies/{id}/acknowledge mounts only when the ingest
	// service is wired (the ack writes an evidence record); without it
	// the handler 503s. GET routes always mount because they only read.
	policyAcksH := policyacksapi.New(policy.NewAckStore(s.dbPool), s.ingestService)
	root.Get("/v1/me/acknowledgments", policyAcksH.MyAcknowledgments)
	root.Get("/v1/policies/{id}/acknowledgment-rate", policyAcksH.AcknowledgmentRate)
	if s.ingestService != nil {
		root.Post("/v1/policies/{id}/acknowledge", policyAcksH.Acknowledge)
	}
	// Slice 012: control state evaluation engine. Two read-only endpoints
	// over the control_evaluations ledger. Routes appended per the
	// parallel-batch convention (chi rejects two Mounts at "/"). Both are
	// literal-segment sub-resources under /v1/controls/{id}/ alongside slice
	// 017's /applicability and slice 018's /effective-scope -- chi resolves
	// declaration order within the same method, so no shadowing. The engine
	// is a pure read+append surface (it never writes evidence_records --
	// constitutional invariant #2), so it needs only the DB pool; the NATS
	// consumer + scheduler that drive evaluation are wired in cmd/atlas.
	controlStateEngine := eval.NewEngine(eval.NewStore(s.dbPool), scope.NewStore(s.dbPool))
	controlStateH := controlstateapi.New(controlStateEngine)
	root.Get("/v1/controls/{id}/state", controlStateH.State)
	root.Get("/v1/controls/{id}/effectiveness", controlStateH.Effectiveness)
	// Slice 064: control-detail backend read endpoints. Four pure reads that
	// fill the four binding placeholders slice 041's control-detail view
	// shipped (evidence stream, linked policies, linked risks, control
	// history). Routes appended per the parallel-batch convention (chi
	// rejects two Mounts at "/"). The three /v1/controls/{id}/ sub-resources
	// sit alongside slice 012's /state + /effectiveness -- chi resolves
	// declaration order within the same method, so no shadowing. The Store
	// is a pure read surface over existing tables (evidence_records,
	// policies, risk_control_links, control_evaluations) -- this slice adds
	// no migration and no write path (constitutional invariant #2).
	controlDetailH := controldetailapi.New(controldetailapi.NewStore(s.dbPool))
	root.Get("/v1/evidence", controlDetailH.Evidence)
	root.Get("/v1/controls/{id}/policies", controlDetailH.Policies)
	root.Get("/v1/controls/{id}/risks", controlDetailH.Risks)
	root.Get("/v1/controls/{id}/history", controlDetailH.History)
	// Slice 444: AI gap-explanation v0 — the first AI-assist surface, the
	// lowest-risk one. GET /v1/controls/{id}/gap-explanation returns the
	// DETERMINISTIC freshness/evidence rollup ALWAYS, plus a plain-language,
	// cited, NON-BINDING local-Ollama explanation of that rollup when
	// available AND every citation resolves to a tenant-owned row (AC-4). The
	// explanation is a comprehension aid in the control-detail view — never an
	// audit artifact, never persisted (P0-444-4), no approve/publish/export
	// path (P0-444-3). Route appended per the parallel-batch convention (chi
	// rejects two Mounts at "/"); the /v1/controls/{id}/ sub-resource sits
	// alongside slice 064's /policies,/risks,/history — chi resolves
	// declaration order within the same method, so no shadowing. The inference
	// client is local Ollama (slice 498), built from the all-defaults local
	// config; when Ollama is unreachable the surface degrades gracefully to
	// the deterministic rollup (AC-7). The gapexplain.Store is a pure read
	// surface over evidence_freshness + controls + evidence_records (invariant
	// #2) and runs under app.current_tenant RLS (invariant #6).
	gapExplainStore := gapexplain.NewStore(s.dbPool)
	gapExplainSvc := gapexplain.NewService(
		gapExplainStore,
		llm.NewOllamaClient(llm.ConfigFromEnv()),
		gapExplainStore,
	)
	gapExplainH := gapexplainapi.New(gapExplainSvc)
	root.Get("/v1/controls/{id}/gap-explanation", gapExplainH.GapExplanation)
	// Slice 599: OSCAL resolved-chain provenance read. A single pure read
	// over the slice-578 provenance persisted into the append-only
	// imported_catalog_audit_log.detail JSON of the `profile_imported`
	// success row. It surfaces, for one imported profile baseline, the
	// ordered chain of resolved documents + their roles + sha256 hashes (the
	// "diligence the diligence tool" provenance story for chained imports).
	// Routes appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). The path is a fresh top-level segment -- no shadowing.
	// The Store is a pure read surface (it never writes the audit-log ledger
	// -- constitutional invariant #2) and never touches the oscal-bridge: the
	// provenance is already in Postgres, so the read is bridge-free.
	oscalProvenanceH := oscalprovenanceapi.New(oscalprovenanceapi.NewStore(s.dbPool))
	root.Get("/v1/oscal/imported-profiles/{id}/provenance", oscalProvenanceH.Provenance)
	// Slice 589: OSCAL vendor-claim read API + operator disposition. Pure
	// reads over the slice-512 imported component-definitions + the
	// accept/reject/needs-info disposition write. A vendor claim is an
	// ASSERTION, not platform-verified evidence: the disposition records the
	// human decision and NEVER auto-satisfies a control (canvas invariant #2,
	// P0-512-1). The Store writes ONLY the claim's disposition metadata + an
	// append-only audit row -- it never touches control_evaluations or the
	// evidence ledger, and never the oscal-bridge (the read path is over
	// persisted Postgres rows). Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). The {id}:verb shape mirrors
	// the slice-025 walkthroughs /{id}:finalize precedent.
	// Slice 660: the OSCAL vendor-claims module gates on the `oscal.export`
	// feature flag. The whole module (list + get + accept/reject/needs-info
	// disposition + scf-anchor map) is wrapped in a featureflag.Gate group so
	// a flag-off tenant gets a clean 404 + {"error":"feature disabled"} (no
	// internal detail leak — slice 367) on EVERY route, not just nav-hidden.
	// `oscal.export` is OFF by default pending GA, which also removes the
	// user-facing exposure of the edge-broken OSCAL page (ATLAS-001/659/683)
	// regardless of 659's outcome. The Gate reads the caller's tenant flag
	// (RLS — invariant #6) and falls open to the Seed default on a DB error.
	oscalComponentsH := oscalcomponentsapi.New(oscalcomponentsapi.NewStore(s.dbPool))
	root.Group(func(r chi.Router) {
		r.Use(featureflag.Gate(featureFlagStore, "oscal.export"))
		r.Get("/v1/oscal/component-definitions", oscalComponentsH.ListDefinitions)
		r.Get("/v1/oscal/component-definitions/{id}", oscalComponentsH.GetDefinition)
		r.Post("/v1/oscal/component-claims/{id}:accept", oscalComponentsH.Accept)
		r.Post("/v1/oscal/component-claims/{id}:reject", oscalComponentsH.Reject)
		r.Post("/v1/oscal/component-claims/{id}:needs-info", oscalComponentsH.NeedsInfo)
		// Slice 620: operator maps an UNMAPPED vendor claim (slice-512
		// scf_anchor_id IS NULL) to a canonical SCF anchor. grc_engineer-gated;
		// validates the anchor exists in the bundled catalog; sets the crosswalk +
		// appends an append-only mapping-audit row. Requirement -> SCF anchor only
		// (invariant #7); the claim stays a claim -- mapping NEVER writes
		// control_evaluations (invariant #2 / P0-512-1).
		r.Patch("/v1/oscal/component-claims/{id}/scf-anchor", oscalComponentsH.MapScfAnchor)
	})
	// Slice 016: evidence freshness + control drift read model. Two
	// read-only endpoints over the slice-016 read-model tables
	// (evidence_freshness, control_drift_snapshots). Routes appended per the
	// parallel-batch convention (chi rejects two Mounts at "/").
	// /v1/controls/drift is a static-segment sibling of /v1/controls/{id}/...
	// -- chi matches the static segment before the {id} wildcard, so no
	// shadowing. The Stores are pure read surfaces (they never write the
	// evidence_records or control_evaluations ledgers -- constitutional
	// invariant #2); the NATS refresh subscriber + daily scheduler that
	// drive the read-model refresh are wired in cmd/atlas.
	freshnessStore := freshness.NewStore(s.dbPool)
	driftStore := drift.NewStore(s.dbPool)
	freshnessDriftH := freshnessdriftapi.New(freshnessStore, driftStore)
	root.Get("/v1/evidence/freshness", freshnessDriftH.Freshness)
	root.Get("/v1/controls/drift", freshnessDriftH.Drift)
	// Slice 066: dashboard backend read endpoints. Three pure reads that
	// fill three of the four binding placeholders slice 040's program
	// dashboard view shipped (per-framework posture, the evidence-ingest
	// activity feed, the unified upcoming-items rollup; the fourth,
	// sort=residual,age on /v1/risks, extends the risks routes below).
	// Routes appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). /v1/frameworks/posture, /v1/activity, /v1/upcoming are
	// fresh top-level paths -- no shadowing of any existing route. The Store
	// is a pure read surface over existing tables + the slice-062
	// admin_audit_log_v view -- this slice adds no migration and no write
	// path (constitutional invariant #2).
	dashboardStoreForExport := dashboardapi.NewStore(s.dbPool)
	dashboardH := dashboardapi.New(dashboardStoreForExport)
	root.Get("/v1/frameworks/posture", dashboardH.FrameworkPosture)
	root.Get("/v1/activity", dashboardH.Activity)
	root.Get("/v1/upcoming", dashboardH.Upcoming)
	// Slice 269: dashboard snapshot export (`GET /v1/dashboard/export
	// ?format=json|csv|xlsx`). Composes the six per-panel reads
	// (framework posture + risks + freshness + drift + upcoming +
	// activity) into a single artifact in three formats — single
	// JSON document, ZIP of one CSV per panel, or XLSX workbook with
	// one sheet per panel. Reuses the already-wired
	// dashboardStoreForExport (shared with the per-panel reads
	// above), the risks store, and the freshness + drift stores
	// declared above. The handler runs the slice 035 OPA admit
	// (`dashboard_export` action — added by the slice 269 OPA admit
	// extension) PLUS a handler-level `hasDashboardExportAccess`
	// predicate; admin + approver only (slice 269 D3). Every
	// terminal outcome writes one `me_audit_log` row with
	// action='dashboard_export' (migration
	// 20260524000000_dashboard_export_meta_audit.sql extends the
	// CHECK to permit the value). Routes appended per the
	// parallel-batch convention. Unblocks slice 230's frontend
	// `Export` CTA wiring.
	dashboardExportSource := dashboardexportapi.NewLivePanelSource(
		dashboardStoreForExport,
		risksStore,
		freshnessStore,
		driftStore,
	)
	dashboardExportH := dashboardexportapi.NewHandler(s.dbPool, dashboardExportSource)
	root.Get("/v1/dashboard/export", dashboardExportH.ExportDashboard)
	// Slice 268: unified cross-domain search (`/v1/search`). Aggregates
	// lexical ILIKE matches across controls, risks, and evidence into a
	// single typed-union response. Per-type OPA admit (D3) is invoked
	// inside the handler — types the caller cannot read are dropped from
	// the merge and surface in `partial_types`. RLS keeps the per-type
	// reads tenant-scoped (constitutional invariant #6); no new schema
	// migration ships with this slice (P0-A1). Closes the slice-228
	// dashboard ⌘K bar's prerequisite gap.
	searchH := searchapi.New(s.dbPool, s.authzEngine)
	root.Get("/v1/search", searchH.Handle)
	// Slice 094: compliance calendar. Read-only aggregation across four
	// existing source tables (audit_periods + exceptions + policies +
	// controls + control_evaluations) plus an iCalendar (RFC 5545) export.
	// Tenant isolation is enforced at the DB layer via RLS (slice 033).
	// ICS auth is a per-user opaque URL token, hashed in api_keys and
	// scope-restricted to AllowedKinds=[calendar.read.v1] — a leaked
	// calendar URL token cannot be used as a general bearer. Routes
	// appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). See docs/audit-log/094-compliance-calendar-decisions.md
	// decisions D1-D8 for the design calls the implementing agent made.
	calendarH := calendarapi.New(
		calendarapi.NewStore(s.dbPool),
		s.credStore,
	)
	calendarH.RegisterRoutes(root)
	// Slice 031: monthly board brief. Generates a single-page, board-ready
	// posture snapshot (per-framework posture + 30-day drift + top-3 risks
	// aging) and persists it as a PINNED, IMMUTABLE snapshot (canvas §7.5).
	// The Generator is a pure reader of the slice-016 freshness + drift read
	// models (reused via the freshnessStore + driftStore constructed above)
	// plus the frameworks + risks tables; its only write target is the
	// append-only board_briefs table. The narrative is TEMPLATED — no LLM
	// (AC-6, P0 anti-criterion). Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"); the literal-suffix routes
	// (/{id}.md, /{id}/pdf) are declared before the bare /{id} so chi's
	// declaration-order match keeps them ahead of the generic id route.
	boardStore := board.NewStore(s.dbPool)
	boardGen := board.NewGenerator(boardStore, freshnessStore, driftStore)
	boardH := boardapi.New(boardGen, boardStore)
	// Slice 660: the board-reporting module (briefs + packs) gates on the
	// `board.reporting` feature flag. Both RegisterRoutes calls receive a
	// featureflag.Gate-wrapped router so a flag-off tenant gets a clean 404
	// + {"error":"feature disabled"} on EVERY board route (briefs + packs),
	// matching the OSCAL gate above (consistent 404 shape; no internal
	// leak). `board.reporting` is OFF by default pending GA. The Gate reads
	// the caller's tenant flag under RLS (invariant #6).
	root.Group(func(r chi.Router) {
		r.Use(featureflag.Gate(featureFlagStore, "board.reporting"))
		boardH.RegisterRoutes(r)
		// Slice 032: quarterly board pack. Extends the slice-031 monthly brief
		// into the full board-meeting artifact — a multi-section report
		// (posture, top risks, coverage trend, open findings, operational
		// metrics, investment-vs-coverage, asks of the board) with a
		// draft -> publish lifecycle. The PackGenerator reuses the same
		// slice-016 freshness + drift read models plus the board-pack-owned
		// failing-evaluations read (control_evaluations as of period_end —
		// decision D4). The narrative is TEMPLATED — no LLM (P0 anti-criterion).
		// Publish is gated on every section being human-approved (decision D6).
		// Routes appended per the parallel-batch convention; the literal-suffix
		// and deeper /sections/... routes are declared before the bare /{id}.
		boardPackStore := board.NewPackStore(s.dbPool)
		// Slice 273: the board-pack `vendor_burndown` section reads through
		// the existing slice-122 high-criticality vendor burndown surface
		// (vendor.Store.Burndown) via a tiny in-process adapter. The adapter
		// lives at this wiring layer so internal/board stays free of an
		// internal/vendor import (board.VendorBurndownReader is the contract).
		// Pinned to criticality=high per slice 273 D2 — the board concern is
		// overdue reviews on the vendors that matter.
		boardPackGen := board.NewPackGenerator(
			boardPackStore, freshnessStore, driftStore,
			vendorBurndownAdapter{store: vendorStore},
		)
		boardPackH := boardapi.NewPack(boardPackGen, boardPackStore)
		boardPackH.RegisterRoutes(r)
	})
	// Slice 155: questionnaire tracer-bullet — Excel import + manual
	// authoring + AnswerLibrary skeleton (SCF-anchor keyed) + PDF export.
	// Routes appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). Tenant scoping enforced by RLS via the Store; the
	// PDF render reuses the chromedp pattern established by
	// internal/board/pdf.go (slice 022/027/137 precedent — zero new
	// go.mod dependency for PDF). NO AI-assist at v1 — the AnswerLibrary
	// suggestion path is a deterministic SCF-anchor lookup, not inference.
	questionnaireStore := questionnaire.NewStore(s.dbPool)
	// Slice 441: AI-answer suggestion v0 (cited drafts, one-click approve) —
	// the FIRST AI-write surface. The qaisuggest.Store does keyword-first-pass
	// retrieval + citation resolution + draft persistence under RLS (invariant
	// #6); the inference rides local Ollama (slice 498, P0-441-6 local-only).
	// Constitutional invariants (no fabricated coverage, no cross-tenant bleed,
	// one-click human approval) are enforced by the service + the DB CHECK on
	// questionnaire_answers (the slice-498 shared ai_assist guard, adopted by
	// this slice's migration).
	qaiStore := qaisuggest.NewStore(s.dbPool)
	qaiSvc := qaisuggest.NewService(
		qaiStore,
		llm.NewOllamaClient(llm.ConfigFromEnv()),
		qaiStore,
		qaiStore,
	)
	questionnairesH := questionnairesapi.NewWithSuggest(questionnaireStore, qaiSvc)
	questionnairesH.RegisterRoutes(root)
	// Slice 471: role-scoped control-implementation checklist generator v0
	// (cited, non-binding). The which-control -> which-role split is
	// DETERMINISTIC (owner_role + applicability_expr normalization, never
	// LLM-guessed); the local-Ollama task-breakdown turns each in-scope
	// control's text into 1..N cited task statements. Every item is cited to a
	// real tenant-owned control/scf-anchor/policy id (validated before the
	// operator sees it); the checklist is a DRAFT approved one section (role) at
	// a time. Constitutional invariants (no fabricated coverage, no cross-tenant
	// bleed, one-click approval, local-only) enforced by the service + the DB
	// CHECK on checklist_sections (the slice-498 shared ai_assist guard). Routes
	// append per the parallel-batch convention.
	checklistStore := checklist.NewStore(s.dbPool)
	checklistSvc := checklist.NewService(
		checklistStore,
		llm.NewOllamaClient(llm.ConfigFromEnv()),
		checklistStore,
		checklistStore,
		llm.NewAuditWriter(s.dbPool),
	)
	checklistH := checklistapi.New(checklistSvc, checklistStore)
	checklistH.RegisterRoutes(root)
	// Slice 034: admin credentials HTTP API + auth routes. Routes append
	// per the parallel-batch convention. Admin-credential routes require
	// the bearer auth middleware (admin gate inside the handler). The
	// /auth/* routes are exempted from bearer auth in the middleware
	// (httpAuthMiddlewareWithExemptions above).
	if s.apikeyStore != nil {
		admincredsH := admincreds.New(s.apikeyStore)
		root.Post("/v1/admin/credentials", admincredsH.Issue)
		root.Get("/v1/admin/credentials", admincredsH.List)
		root.Post("/v1/admin/credentials/{id}/rotate", admincredsH.Rotate)
		root.Post("/v1/admin/credentials/{id}/revoke", admincredsH.Revoke)
	}
	// Slice 508: SCIM 2.0 user-lifecycle provisioning. Two surfaces:
	//
	//   (1) Admin control plane on /v1 (admin-gated): issue / list / revoke
	//       the per-tenant SCIM bearer credential (AC-3).
	//   (2) The inbound SCIM endpoints on /scim/v2/* — mounted in a chi
	//       SUBROUTER wrapped by the SCIM auth middleware (the distinct
	//       credential scope, P0-508-2). The /scim/ prefix is bypassed by the
	//       /v1 JWT + requireCredential + authz chain above, so a SCIM token
	//       can NEVER reach a /v1 handler and an atlas JWT is irrelevant here.
	//
	// Both mount only when the SCIM credential store is wired (cmd/atlas wires
	// it with a BEARER_HASH_KEY hasher + BYPASSRLS authPool). The provisioning
	// store runs every query under the credential's tenant RLS (P0-508-4).
	if s.scimCredStore != nil {
		adminScimH := adminscim.New(s.scimCredStore)
		root.Post("/v1/admin/scim-credentials", adminScimH.Issue)
		root.Get("/v1/admin/scim-credentials", adminScimH.List)
		root.Delete("/v1/admin/scim-credentials/{id}", adminScimH.Revoke)

		scimUserH := scimapi.NewHandler(scim.NewStore(s.dbPool))
		root.Group(func(scimR chi.Router) {
			scimR.Use(scimapi.Middleware(s.scimCredStore))
			scimUserH.Mount(scimR)
		})
	}
	// Slice 073: admin-only bootstrap-token reset endpoint. Used by
	// `atlas-cli credentials issue --reset-bootstrap`. Mounts only when
	// the platform_status resetter is attached.
	if s.platformResetter != nil {
		root.Post("/v1/admin/install/reset-bootstrap", s.handleResetBootstrap)
	}
	if s.authHandler != nil {
		root.Get("/auth/oidc/login", s.authHandler.OIDCLogin)
		root.Get("/auth/oidc/callback", s.authHandler.OIDCCallback)
		root.Post("/auth/local/login", s.authHandler.LocalLogin)
		root.Post("/auth/logout", s.authHandler.Logout)
	}
	// Slice 059: per-tenant feature flags admin API. Two admin-only
	// routes; the handler enforces cred.IsAdmin defense-in-depth so
	// non-admin callers see 403 even without the slice-035 OPA
	// middleware wired. Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/").
	featuresH := featuresapi.New(featureFlagStore)
	root.Get("/v1/admin/features", featuresH.List)
	root.Patch("/v1/admin/features/{key}", featuresH.Patch)
	// Slice 660: NON-admin, tenant-scoped enabled-modules read. The web
	// shell calls this for EVERY authed user to gate nav (hide Vendor
	// Claims / Board Packs when their flag is off) — the admin
	// GET /v1/admin/features above stays admin-only (it carries the full
	// inventory + toggle/audit surface). This route exposes ONLY the
	// slice 660 gating booleans (featureflag.GatingKeys) for the caller's
	// own tenant (RLS — invariant #6); no write path, no audit metadata.
	root.Get("/v1/features/enabled", featuresH.Enabled)
	// Slice 062: admin BFF backend endpoints — SSO config, users + roles,
	// and unified audit-log read across the seven per-domain audit log
	// tables (via the admin_audit_log_v view from migration _022). Each
	// handler enforces cred.IsAdmin defense-in-depth alongside the slice
	// 035 OPA middleware. Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). Unblocks slice 060's
	// SSO / Users / Audit-log UI pages so they can flip from PARTIAL to
	// PASS.
	ssoH := adminsso.New(s.dbPool)
	root.Get("/v1/admin/sso", ssoH.Get)
	root.Patch("/v1/admin/sso", ssoH.Patch)
	root.Post("/v1/admin/sso/preflight", ssoH.Preflight)
	// Slice 478: extend the slice-062 users surface with the cross-tenant
	// super-admin list + the assign/revoke verbs (incl. super-admin
	// self-assign). SetAuthPool wires the BYPASSRLS pool used for the
	// cross-tenant writes (the target tenant != the actor's session tenant,
	// so atlas_app RLS would block the INSERT — slice 478 D2, mirroring
	// admintenants.Create). When authPool is nil (unit-server harness),
	// cross-tenant ops return 503 and within-tenant ops keep working.
	// ListDispatch routes super-admins to the cross-tenant view and
	// everyone else to the slice-062 within-tenant List (P0-478-3: a
	// tenant-admin's view is never widened).
	usersH := adminusers.New(s.dbPool).SetAuthPool(s.authPool)
	root.Get("/v1/admin/users", usersH.ListDispatch)
	root.Get("/v1/admin/users/{id}", usersH.Get)
	root.Patch("/v1/admin/users/{id}/roles", usersH.PatchRoles)
	root.Post("/v1/admin/users/assign", usersH.Assign)
	root.Post("/v1/admin/users/revoke", usersH.Revoke)

	// Slice 142: super_admin management surface. super_admin-gated
	// (the handler reads jwtmw.FromContext().SuperAdmin); the OPA
	// policy in policies/authz/super_admin.rego is the second leg.
	superAdminsH := adminsuperadmins.New(s.dbPool)
	root.Get("/v1/admin/super-admins", superAdminsH.List)
	root.Post("/v1/admin/super-admins", superAdminsH.Grant)
	root.Delete("/v1/admin/super-admins/{user_id}", superAdminsH.Demote)

	// Slice 378: authz bundle hot-reload. super_admin-gated; reloads
	// the embedded policies/authz/*.rego bundle without a process
	// restart. The atomic.Pointer-backed Engine swap (slice 378 AC-1
	// + AC-2) means in-flight Decide calls during a reload see
	// either the old query or the new one — never a partial state.
	// The matrix validator runs against the CANDIDATE query BEFORE
	// the swap (slice 378 AC-3); matrix failure leaves the engine
	// serving the prior bundle. Closes slice 332 F-OPA-2 (High).
	// When the engine is not yet attached (unit-server harness), the
	// handler returns 503 to every request.
	if s.authzEngine != nil {
		authzBundleH := adminauthzbundle.New(s.dbPool, s.authzEngine)
		root.Post("/v1/admin/authz-bundle/reload", authzBundleH.Reload)
	}

	// Slice 143: create-tenant flow. super_admin-gated; the handler
	// reads jwtmw.FromContext().SuperAdmin; the OPA super_admin.rego
	// admits the `tenants` resource segment as the second leg. The
	// POST handler uses the BYPASSRLS authPool for the cross-tenant
	// transaction because the new tenant_id ≠ the actor's session
	// tenant and the slice-002 four-policy RLS on `tenants` (slice
	// 144) would block an atlas_app INSERT keyed on a row whose `id`
	// does not match the GUC. When authPool is nil (test harness
	// without DATABASE_URL), the handler returns 503.
	adminTenantsH := admintenants.New(s.dbPool, s.authPool)
	root.Get("/v1/admin/tenants", adminTenantsH.List)
	root.Post("/v1/admin/tenants", adminTenantsH.Create)
	// Slice 278: demo-seed UI button (edge-deployment only).
	// Triple gate: ATLAS_ENABLE_DEMO_SEED env var (returns 503 when
	// unset), admin role via slice-035 OPA admin.rego (returns 403
	// for non-admins), and a me_audit_log row written BEFORE the
	// seeder runs (fail closed). Per-IP rate limit of 1 invocation
	// per 60 seconds on Seed + Teardown; Status is unlimited.
	// authPool is the BYPASSRLS pool required by demoseed.Seeder
	// (slice 205 LOAD-BEARING design). dbPool is the RLS-enforced
	// app-role pool slice 671 uses to drive the post-seed evaluator
	// (eval.EvaluateAll + freshness.Refresh) under RLS so the seeded
	// tenant shows real control state instead of "—".
	adminDemoH := admindemo.New(s.authPool, admindemo.DefaultEnabledFunc).SetAppPool(s.dbPool)
	root.Get("/v1/admin/demo/status", adminDemoH.Status)
	root.Post("/v1/admin/demo/seed", adminDemoH.Seed)
	root.Post("/v1/admin/demo/teardown", adminDemoH.Teardown)
	auditLogH := adminauditlog.New(s.dbPool)
	root.Get("/v1/admin/audit-log", auditLogH.List)
	// Slice 124: unified audit-log aggregation read across all nine
	// per-domain audit-log tables (decision/evidence/exception/sample/
	// audit_period/aggregation_rule/feature_flag/me/walkthrough). The
	// upstream slice 035 OPA middleware is the canonical role gate; the
	// handler does defense-in-depth (admin OR auditor OR grc_engineer).
	// Route appended per the parallel-batch convention (chi.Mux rejects
	// two Mounts at "/", so individual routes register onto the root).
	root.Get("/v1/admin/audit-log/unified", auditLogH.UnifiedList)
	// Slice 135: bulk data-export variant of the unified read.
	// Same admit set (admin OR auditor OR grc_engineer — slice 124 D5;
	// slice 135 P0-A9 admit-set parity test pins it at the OPA layer),
	// same 90-day window cap, same tenancy + RLS path. Reuses the
	// slice-124 aggregator (internal/audit/unifiedlog.Query) with a
	// format-encoder swap on the response body (CSV / JSON / XLSX).
	// Meta-audit row written on EVERY outcome (slice 135 P0-A4).
	root.Get("/v1/admin/audit-log/export", auditLogH.ExportUnified)
	// Slice 270: non-admin activity-ledger surface. Same aggregator as
	// the slice 124 admin endpoint (`internal/audit/unifiedlog.Query`),
	// shared SQL with one extra row-visibility WHERE predicate gated on
	// `caller_is_privileged`. The OPA admit-set widens to {admin,
	// auditor, grc_engineer, viewer, control_owner} via the new
	// `activity-unified` resource type — non-privileged callers (viewer
	// / control_owner) see tenant-public kinds plus their own me-rows;
	// feature_flag rows are hidden + cross-actor me-rows are hidden
	// (slice 270 D1 + D2). Reuses the slice 124 meta-audit pattern with
	// `surface="activity"` tagging (slice 270 D7) — no new action value,
	// no migration.
	root.Get("/v1/activity/unified", auditLogH.ActivityList)
	// Slice 139: per-entity data exports for audit_periods + vendors.
	// Both reuse the slice-135 export library; both go through the
	// slice-145 per-(tenant, user) concurrency cap; both emit a
	// meta-audit row on every outcome (`audit_periods_export` +
	// `vendors_export` — migration 20260519000000). Route mounts
	// registered onto the root per the parallel-batch convention
	// (chi.Mux rejects two Mounts at "/"). Same admit set as the
	// slice-135 audit-log export (admin OR auditor OR grc_engineer).
	auditPeriodsExportH := adminauditperiods.New(s.dbPool)
	root.Get("/v1/admin/audit-periods/export", auditPeriodsExportH.ExportAuditPeriods)
	vendorsExportH := adminvendors.New(s.dbPool)
	root.Get("/v1/admin/vendors/export", vendorsExportH.ExportVendors)
	// Slice 138: per-entity data exports for the ledger entities —
	// evidence + policies + exceptions + samples. Each closes the
	// per-entity export cluster with the slice 135 library + slice
	// 145 concurrency cap. Meta-audit actions:
	//   `evidence_export` (payload column EXCLUDED at v1 per
	//    slice 138 P0-A-Ledger-1 — operational-metadata leak vector)
	//   `policies_export`
	//   `exceptions_export`
	//   `samples_export` (row cap 250K per slice doc; samples can
	//    be voluminous at multi-product orgs)
	// Migration: 20260520000010_ledger_entities_export_meta_audit.sql.
	// Same admit set as the slice 137 controls-export route.
	evidenceExportH := apievidence.NewExportHandler(s.dbPool)
	root.Get("/v1/admin/evidence/export", evidenceExportH.ExportEvidence)
	policiesExportH := policiesapi.NewExportHandler(s.dbPool)
	root.Get("/v1/admin/policies/export", policiesExportH.ExportPolicies)
	exceptionsExportH := exceptionsapi.NewExportHandler(s.dbPool)
	root.Get("/v1/admin/exceptions/export", exceptionsExportH.ExportExceptions)
	samplesExportH := auditapi.NewSamplesExportHandler(s.dbPool)
	root.Get("/v1/admin/samples/export", samplesExportH.ExportSamples)
	// Slice 076: metrics catalog + cascade + observation store. Routes
	// appended per the parallel-batch convention (chi.Mux rejects two
	// Mounts at "/"). The literal-segment route /v1/metrics/cascade is
	// declared BEFORE /v1/metrics/{id} so chi's declaration-order match
	// keeps the cascade route ahead of the generic /{id} route. The
	// /{id}/observations + /{id}/inputs + /{id}/target sub-routes are
	// distinct path shapes, no shadowing.
	metricsH := metricsapi.New(s.dbPool)
	root.Get("/v1/metrics", metricsH.ListCatalog)
	root.Get("/v1/metrics/cascade", metricsH.GetCascade)
	root.Get("/v1/metrics/{id}", metricsH.GetCatalog)
	root.Get("/v1/metrics/{id}/observations", metricsH.ListObservations)
	root.Post("/v1/metrics/{id}/inputs", metricsH.CreateInput)
	root.Get("/v1/metrics/{id}/target", metricsH.GetTarget)
	root.Put("/v1/metrics/{id}/target", metricsH.UpsertTarget)
	// Slice 121 (AC-5/6/7): wrap the assembled chi router with otelhttp
	// at the OUTERMOST layer so every request including 401s gets a
	// span. AC-6: otelhttp's default attribute set is
	// {http.method, http.route, http.status_code, http.target, net.peer.ip}
	// — it does NOT capture Authorization / Cookie headers, request
	// body, or response body (P0-A7 / P0-A8). AC-7: high-frequency
	// probes (/health, /metrics, /v1/version, /v1/install-state) are
	// excluded via WithFilter so they don't drown out useful spans.
	//
	// AC-2: when OTel is in no-op mode (OTEL_EXPORTER_OTLP_ENDPOINT
	// unset), otelhttp still runs but its tracer is the no-op — every
	// span is recorded against a discarded backend. Cheap. No
	// behavioural change for callers that haven't configured OTel.
	return otelhttp.NewHandler(root, "atlas-http",
		otelhttp.WithFilter(spanFilter),
	)
}

// spanFilter excludes high-frequency probes from span generation
// (AC-7). These endpoints are called every few seconds by the
// docker-compose healthcheck, Prometheus scraper, and frontend
// install-state SSR fetch; tracing them is noise without signal.
func spanFilter(r *http.Request) bool {
	switch r.URL.Path {
	case "/health", "/metrics", "/v1/version", "/v1/install-state":
		return false
	}
	return true
}

// attestUploader returns the slice-011 ArtifactUploader adapter over the
// slice-036 *artifact.Store, or nil when no artifact store has been
// wired. The slice-011 handler tolerates a nil uploader — it just
// rejects requests that cite an artifact_id with 503.
func attestUploader(store *artifact.Store) controlsapi.ArtifactUploader {
	if store == nil {
		return nil
	}
	return &storeArtifactAdapter{store: store}
}

// walkthroughUploaderFor returns the slice-027 ArtifactUploader adapter
// over the slice-036 *artifact.Store. The slice-027 handler 503s when
// the uploader is nil; this keeps unit-test servers (no artifact store
// wired) functional for the non-attachment endpoints.
func walkthroughUploaderFor(store *artifact.Store) walkthroughsapi.ArtifactUploader {
	if store == nil {
		return nil
	}
	return &walkthroughArtifactAdapter{store: store}
}

// walkthroughArtifactAdapter is the narrow Put-only view of the
// slice-036 *artifact.Store the slice-027 handler needs.
type walkthroughArtifactAdapter struct {
	store *artifact.Store
}

func (a *walkthroughArtifactAdapter) Put(ctx context.Context, in artifact.PutInput) (artifact.Artifact, error) {
	return a.store.Put(ctx, in)
}

type storeArtifactAdapter struct {
	store *artifact.Store
}

// PayloadURIFor resolves artifactID through artifact.Store.Get (which
// enforces RLS via the tenant context) and returns the canonical s3://
// URI for the artifact. Cross-tenant lookups return ErrNotFound, which
// the handler surfaces as 404.
func (a *storeArtifactAdapter) PayloadURIFor(ctx context.Context, artifactID uuid.UUID) (string, error) {
	art, err := a.store.Get(ctx, artifactID)
	if err != nil {
		return "", err
	}
	return art.PayloadURI(a.store.Bucket()), nil
}

// RunHTTP starts the HTTP server on addr (e.g., ":8080") and blocks until
// ctx is canceled, at which point it shuts down within a 5-second grace.
// Returns an error if no DB pool has been attached.
func (s *Server) RunHTTP(ctx context.Context, addr string) error {
	if s.dbPool == nil {
		return errors.New("api: HTTP server requires a DB pool (call Server.AttachDB before RunHTTP)")
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.httpHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// corsMiddleware allows the Next.js dev origin to call the API with the
// bearer token. Production frontends served from the same origin don't
// trigger CORS.
func corsMiddleware(next http.Handler) http.Handler {
	const devOrigin = "http://localhost:3000"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == devOrigin {
			w.Header().Set("Access-Control-Allow-Origin", devOrigin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireCredential is the slice 197 fail-closed credential gate. It
// runs AFTER the slice 190 JWT validation middleware (which passes
// through requests with no JWT shape so the now-retired legacy bearer
// middleware could handle them). Without this gate, a request bearing
// no Authorization header at all would reach handlers in an
// unauthenticated state — handlers that do tenant-scoped queries would
// then either error on missing-tenant GUC or, worse, return rows
// without RLS filtering.
//
// The middleware returns RFC 6750 §3-shaped 401 + JSON body for any
// non-exempt path whose request context lacks
// `authctx.CredentialFromContext`. Exempt prefixes mirror the JWT
// middleware's bypass list: unauthenticated paths by design
// (`/oauth/token`, `/.well-known/*`, `/auth/*`, `/health`, `/metrics`,
// `/v1/version`, `/v1/install-state`, `/v1/calendar.ics`,
// `/oauth/device_authorization`).
//
// Exact-vs-prefix semantic mirrors jwtBypass: entries ending in `/`
// are prefix matches; entries without a trailing slash are
// exact-path matches.
//
// Slice 197 P0-191-1 invariant restoration: there is no auth-bypass
// window when the legacy bearer middleware is removed.
func requireCredential(exempt ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range exempt {
				if p == "" {
					continue
				}
				if strings.HasSuffix(p, "/") {
					if strings.HasPrefix(r.URL.Path, p) {
						next.ServeHTTP(w, r)
						return
					}
				} else if r.URL.Path == p {
					next.ServeHTTP(w, r)
					return
				}
			}
			if _, ok := authctx.CredentialFromContext(r.Context()); !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="atlas", error="invalid_token"`)
				writeAuthError(w, "authorization must be `Bearer <token>`")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// jwtBypass wraps the slice 190 JWT validation middleware so requests
// whose path matches any exempt prefix skip the middleware entirely.
// The exempt set mirrors the legacy bearer middleware's exemption
// list — `/oauth/*` (authenticates itself), `/.well-known/*` (RFC
// 8414 §3 mandates unauth access), `/health` (liveness), `/metrics`
// (opt-in scrape endpoint), and the public metadata routes
// (`/v1/version`, `/v1/install-state`, `/v1/calendar.ics`, `/auth/`).
// On a non-exempt path, the JWT middleware runs; on an exempt path,
// the chain proceeds directly to the next middleware.
//
// Slice 190 P0-190-9: the JWT middleware MUST operate only on /v1/*
// — by skipping every non-/v1 prefix above we satisfy that
// constraint without coupling jwtmw to chi route specifics.
func jwtBypass(mw func(http.Handler) http.Handler, exempt ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Slice 191: bypass uses prefix-match by default, with an
			// exact-match carve-out for paths that share a prefix
			// with an enforced sibling. `/oauth/device_authorization`
			// is unauthenticated (RFC 8628 §3.1) but its siblings
			// `/oauth/device_authorization/approve` and `/deny` are
			// authenticated — prefix-match would silently exempt both
			// children. We treat any exempt entry that does NOT end
			// in "/" as an exact-path match; entries that DO end in
			// "/" remain prefix-match (the same shape the slice 190
			// list used for "/.well-known/", "/auth/", "/oauth/").
			for _, p := range exempt {
				if p == "" {
					continue
				}
				if strings.HasSuffix(p, "/") {
					if strings.HasPrefix(r.URL.Path, p) {
						next.ServeHTTP(w, r)
						return
					}
				} else if r.URL.Path == p {
					next.ServeHTTP(w, r)
					return
				}
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

// httpAuthMiddlewareWithExemptions is the HTTP auth middleware that:
//  1. Skips bearer auth for request paths whose prefix matches any exempt.
//     The /auth/* routes need this because the user has no bearer yet at
//     the moment of sign-in.
//  2. Skips bearer auth when the slice 190 JWT middleware has already
//     authenticated the request (jwtmw.FromContext returns a non-nil
//     claims pointer). This is the coexistence contract: JWT first,
//     legacy as fall-through.
//  3. Stacks a DB-backed apikeystore.Store as a fallback for tokens that
//     the in-memory credstore does not know about. Connector pushes use
//     DB-backed keys; bootstrap admin credentials use in-memory.
func httpAuthMiddlewareWithExemptions(store *credstore.Store, apikeys *apikeystore.Store, exempt ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range exempt {
				if p != "" && strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			// Slice 190 coexistence: if the JWT middleware ran earlier
			// in the chain and set claims on the context, accept that
			// auth and pass through. Decision D3: JWT first, legacy
			// as fall-through.
			if claims := jwtmw.FromContext(r.Context()); claims != nil {
				next.ServeHTTP(w, r)
				return
			}
			token, ok := extractBearerFromHTTP(r)
			if !ok {
				writeAuthError(w, "authorization must be `Bearer <token>`")
				return
			}
			cred, err := store.Authenticate(token)
			if err != nil {
				// Fall through to the DB-backed apikeystore for slice-034
				// keys that were issued via /v1/admin/credentials.
				if errors.Is(err, credstore.ErrUnknownKey) && apikeys != nil {
					dbCred, dbErr := apikeys.Authenticate(r.Context(), token)
					if dbErr == nil {
						ctx := authctx.WithCredential(r.Context(), dbCred)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					writeAuthError(w, "invalid or revoked bearer token")
					return
				}
				if errors.Is(err, credstore.ErrUnknownKey) {
					writeAuthError(w, "invalid or revoked bearer token")
					return
				}
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			ctx := authctx.WithCredential(r.Context(), cred)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearerFromHTTP(r *http.Request) (string, bool) {
	auth := r.Header.Get(sdk.MetadataAuthorization)
	if auth == "" {
		return "", false
	}
	parts := strings.SplitN(strings.TrimSpace(auth), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	tok := strings.TrimSpace(parts[1])
	return tok, tok != ""
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

// handleHealth is the slice-037 liveness probe used by the docker-compose
// self-host bundle's healthcheck and the atlas-bootstrap readiness poll.
//
// It always returns 200 when the process is serving HTTP. If the DB pool
// is attached it runs a short-timeout ping; a failed ping reports
// `{"db":"degraded"}` but still 200 — `/health` is liveness ("is the
// process up?"), not readiness. Returning 503 on a transient DB blip
// would cause compose to mark atlas unhealthy and restart-loop it during
// Postgres warm-up. Bootstrap ordering already gates atlas on
// postgres-healthy, so the DB is reachable by the time atlas runs.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	db := "ok"
	if s.dbPool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.dbPool.Ping(ctx); err != nil {
			db = "degraded"
		}
	} else {
		db = "absent"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","db":"` + db + `"}`))
}

// vendorBurndownAdapter wires the slice-273 board.VendorBurndownReader
// contract onto the existing slice-122 vendor.Store.Burndown surface. It
// lives at the wiring layer (httpserver.go) — NOT in internal/board — so
// the board package stays free of an internal/vendor import. The adapter
// pins the criticality filter to `high` per slice 273 D2: the board
// concern is overdue reviews on the vendors that matter, not the entire
// vendor portfolio.
type vendorBurndownAdapter struct {
	store *vendor.Store
}

// ReadHighCriticalityBurndown reads the high-criticality vendor burndown
// at `asOf` through vendor.Store.Burndown (slice 122). It propagates the
// caller's ctx — which carries the tenant GUC — so RLS gates the read
// (constitutional invariant 6). Returns (0, 0, 0) when no high-criticality
// vendors are registered for the tenant; that case is the honest read.
func (a vendorBurndownAdapter) ReadHighCriticalityBurndown(ctx context.Context, asOf time.Time) (board.VendorBurndownReadout, error) {
	high := vendor.CriticalityHigh
	bd, err := a.store.Burndown(ctx, asOf, &high)
	if err != nil {
		return board.VendorBurndownReadout{}, err
	}
	// The Total band is the slice-122 aggregate; with criticality=high
	// pinned it equals the single Bands[0] row when present. Read the
	// aggregate so the contract stays correct even if vendor.Store ever
	// returns multiple bands under the same criticality filter.
	return board.VendorBurndownReadout{
		Total:   bd.Total.Total,
		OnTime:  bd.Total.Total - bd.Total.Overdue,
		PastDue: bd.Total.Overdue,
	}, nil
}
