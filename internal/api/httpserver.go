package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/requestidmw"
	scimapi "github.com/mgoodric/security-atlas/internal/api/scim"
	"github.com/mgoodric/security-atlas/internal/api/securityheaders"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	walkthroughsapi "github.com/mgoodric/security-atlas/internal/api/walkthroughs"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/risk"
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
//
// It delegates router assembly to buildRouter() and wraps the result with
// otelhttp at the OUTERMOST layer (slice 121). The split between assembly
// and the otel wrap exists so the route-walk test (slice 436) can chi.Walk
// the raw *chi.Mux without unwrapping otelhttp.
func (s *Server) httpHandler() http.Handler {
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
	return otelhttp.NewHandler(s.buildRouter(), "atlas-http",
		otelhttp.WithFilter(spanFilter),
	)
}

// buildRouter assembles the platform's chi router: the shared middleware
// chain (security headers, request ID, CORS, JWT, credential gate, tenancy,
// authz, feature-flag cache) followed by every per-domain route
// registration. Slice 436 extracted the per-domain route registration into
// the register_*.go files in this same package; buildRouter is the single
// composition point that wires the shared middleware once and then calls
// each registrar with the SAME middleware-bearing root router (the AC-6
// auth-preservation guarantee: a route registered from register_admin.go
// carries the identical middleware chain it carried inline). The router is
// returned un-wrapped so the slice-436 route-walk test can chi.Walk it.
func (s *Server) buildRouter() *chi.Mux {
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

	// Slice 436: shared stores reused across more than one per-domain
	// registrar are constructed once here and threaded down, so a single
	// resource backs the routes that share it (one risk.Store backs both the
	// risk routes and the dashboard-export panel source; one vendor.Store
	// backs both the vendor routes and the board-pack burndown adapter; the
	// freshness+drift read-model stores back the freshness/drift, dashboard,
	// and board reads). Identical to the former inline construction.
	risksStore := risk.NewStore(s.dbPool)
	vendorStore := vendor.NewStore(s.dbPool)
	freshnessStore := freshness.NewStore(s.dbPool)
	driftStore := drift.NewStore(s.dbPool)

	s.registerPlatform(root, queries)
	s.registerGraph(root)
	s.registerRisk(root, risksStore)
	s.registerVendor(root, vendorStore)
	s.registerFrameworkScope(root)
	s.registerControls(root)
	s.registerAudit(root)
	s.registerMe(root)
	s.registerGovernance(root)
	s.registerControlState(root)
	s.registerOSCAL(root, featureFlagStore)
	s.registerDashboard(root, risksStore, freshnessStore, driftStore)
	s.registerCalendar(root)
	s.registerBoard(root, featureFlagStore, freshnessStore, driftStore, vendorStore)
	s.registerQuestionnaire(root)
	s.registerAdmin(root, featureFlagStore)
	s.registerMetrics(root)
	return root
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

// scimGroupDeriver adapts the slice-509 grouprole.Resolver to the
// scimapi.RoleDeriver surface the SCIM /Groups handler calls on a membership
// change (slice 733 AC-3). It is a thin mapping shim: it translates the
// handler's package-local DeriveRequest into a grouprole.DeriveInput with
// Source=SCIM and a NIL idp_config_id (the SCIM push channel matches the
// NULL-source mappings, per slice 509). It carries NO mapping/derivation logic
// — that lives entirely in the resolver (P0-733-1). The resolver's last-admin
// guard, fail-closed (unmapped group → no role), no-auto-create, and
// manual-role preservation all hold unchanged on this path (P0-733-3 / AC-4).
type scimGroupDeriver struct {
	resolver *grouprole.Resolver
}

// Derive maps the SCIM membership change to roles via the slice-509 resolver.
// ctx carries the tenant RLS context set by the SCIM auth middleware, so the
// resolver reads the tenant's mappings + reconciles under RLS (P0-733-4).
func (d scimGroupDeriver) Derive(ctx context.Context, in scimapi.DeriveRequest) error {
	_, err := d.resolver.Derive(ctx, grouprole.DeriveInput{
		UserID:      in.UserID,
		IDPConfigID: uuid.Nil, // SCIM source → NULL-source mappings (slice 509)
		Groups:      in.Groups,
		Source:      grouprole.SourceSCIM,
	})
	return err
}
