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

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	"github.com/mgoodric/security-atlas/internal/api/adminusers"
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
	controldetailapi "github.com/mgoodric/security-atlas/internal/api/controldetail"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	controlstateapi "github.com/mgoodric/security-atlas/internal/api/controlstate"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	dashboardapi "github.com/mgoodric/security-atlas/internal/api/dashboard"
	decisionsapi "github.com/mgoodric/security-atlas/internal/api/decisions"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	exceptionsapi "github.com/mgoodric/security-atlas/internal/api/exceptions"
	featuresapi "github.com/mgoodric/security-atlas/internal/api/features"
	fwscopesapi "github.com/mgoodric/security-atlas/internal/api/frameworkscopes"
	freshnessdriftapi "github.com/mgoodric/security-atlas/internal/api/freshnessdrift"
	meapi "github.com/mgoodric/security-atlas/internal/api/me"
	metricsapi "github.com/mgoodric/security-atlas/internal/api/metrics"
	orgunitsapi "github.com/mgoodric/security-atlas/internal/api/orgunits"
	oscalexportapi "github.com/mgoodric/security-atlas/internal/api/oscalexport"
	policiesapi "github.com/mgoodric/security-atlas/internal/api/policies"
	policyacksapi "github.com/mgoodric/security-atlas/internal/api/policyacks"
	risksapi "github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/scopes"
	"github.com/mgoodric/security-atlas/internal/api/securityheaders"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
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
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/userprefs"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
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
	root.Use(corsMiddleware)
	// Slice 034: /auth/* (login/callback/logout) is bearer-exempt — users
	// don't have a bearer yet at the moment they sign in. The middleware
	// must skip the prefix. Note: /v1/admin/credentials* DOES go through
	// bearer auth (admin endpoints require an admin credential). Slice 037
	// adds /health to the same exempt set — the docker-compose self-host
	// bundle's healthcheck and the atlas-bootstrap readiness poll both hit
	// it with no credential. (The /health *route* is registered below,
	// after every .Use() — chi requires all middleware before any route.)
	// Slice 072: /v1/version is added to the bearer-exempt set. The
	// endpoint is intentionally public — it returns metadata about the
	// binary (Version/Commit/BuildTime/GoVersion), NOT tenant data. The
	// auth-bypass is documented at api.NewVersionHandler. Same precedent
	// as /health from slice 037.
	// Slice 073: /v1/install-state is added to the bearer-exempt set for
	// the same reason — public metadata about whether this is a fresh
	// install (P0-A4). The login page reads it SSR before any auth exists.
	// Slice 094: /v1/calendar.ics is exempted from the upstream Bearer-header
	// auth because calendar clients (Google / Apple / Outlook) cannot
	// carry an Authorization header — they fetch with no auth metadata
	// beyond what's in the URL. The calendar.ics handler authenticates
	// inline via the `?token=` URL parameter, scope-restricted to
	// AllowedKinds=[calendar.read.v1]. See decision D3 in
	// docs/audit-log/094-compliance-calendar-decisions.md.
	root.Use(httpAuthMiddlewareWithExemptions(s.credStore, s.apikeyStore, "/auth/", "/health", "/v1/version", "/v1/install-state", "/v1/calendar.ics"))
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
		root.Use(authzmw.Middleware(s.authzEngine, s.authzAudit, "/auth/", "/health", "/v1/version", "/v1/install-state", "/v1/calendar.ics"))
	}
	// Slice 059: per-request feature-flag cache. Attached AFTER auth /
	// tenancy / authz so the cache lives inside the same request-context
	// every downstream handler sees. Anti-criterion P0: no cross-request
	// cache -- the cache is created fresh per request and dies when the
	// request ends.
	root.Use(featureflag.CacheMiddleware)

	queries := dbx.New(s.dbPool)
	// Slice 104: the anchors handler needs the pool (not just the
	// pre-bound queries) to open per-request tenant-GUC transactions
	// when `?include=state` is set. The non-state paths continue to use
	// the pre-bound `queries`.
	root.Mount("/", anchors.NewWithPool(queries, s.dbPool).Routes())

	// Slice 037: /health liveness probe. Registered after the root Mount
	// and alongside the other direct routes below — chi panics if a route
	// is added before all .Use() middleware, and registering it directly
	// on root (not via a second Mount("/")) avoids the double-mount
	// panic. It is bearer- and authz-exempt via the exemption lists
	// passed to the middleware above, so it answers with no credential.
	root.Get("/health", s.handleHealth)
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
	ucfH := ucfcoverage.New(s.dbPool)
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
	vendorsH := vendors.New(vendor.NewStore(s.dbPool))
	root.Post("/v1/vendors", vendorsH.CreateVendor)
	root.Get("/v1/vendors", vendorsH.ListVendors)
	root.Get("/v1/vendors/burndown", vendorsH.Burndown)
	root.Get("/v1/vendors/{id}", vendorsH.GetVendor)
	root.Patch("/v1/vendors/{id}", vendorsH.UpdateVendor)
	root.Delete("/v1/vendors/{id}", vendorsH.DeleteVendor)
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
	controlsH := controlsapi.New(control.NewStore(s.dbPool), controlsRegistry)
	root.Post("/v1/controls:upload-bundle", controlsH.UploadBundle)
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
	meProfileH := meapi.NewProfile(usersStore, s.dbPool)
	mePrefsH := meapi.NewPreferences(userprefsStore, s.dbPool)
	meSessionsH := meapi.NewSessions(sessionsStore, s.dbPool)
	root.Get("/v1/me", meProfileH.GetMe)
	root.Patch("/v1/me", meProfileH.PatchMe)
	root.Get("/v1/me/preferences", mePrefsH.GetPreferences)
	root.Patch("/v1/me/preferences", mePrefsH.PatchPreferences)
	root.Get("/v1/me/sessions", meSessionsH.ListSessions)
	root.Delete("/v1/me/sessions", meSessionsH.RevokeOtherSessions)
	root.Delete("/v1/me/sessions/{id}", meSessionsH.RevokeSession)
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
	dashboardH := dashboardapi.New(dashboardapi.NewStore(s.dbPool))
	root.Get("/v1/frameworks/posture", dashboardH.FrameworkPosture)
	root.Get("/v1/activity", dashboardH.Activity)
	root.Get("/v1/upcoming", dashboardH.Upcoming)
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
	boardH.RegisterRoutes(root)
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
	boardPackGen := board.NewPackGenerator(boardPackStore, freshnessStore, driftStore)
	boardPackH := boardapi.NewPack(boardPackGen, boardPackStore)
	boardPackH.RegisterRoutes(root)
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
	featuresH := featuresapi.New(featureflag.NewStore(s.dbPool))
	root.Get("/v1/admin/features", featuresH.List)
	root.Patch("/v1/admin/features/{key}", featuresH.Patch)
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
	usersH := adminusers.New(s.dbPool)
	root.Get("/v1/admin/users", usersH.List)
	root.Get("/v1/admin/users/{id}", usersH.Get)
	root.Patch("/v1/admin/users/{id}/roles", usersH.PatchRoles)
	auditLogH := adminauditlog.New(s.dbPool)
	root.Get("/v1/admin/audit-log", auditLogH.List)
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
	return root
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

// httpAuthMiddlewareWithExemptions is the HTTP auth middleware that:
//  1. Skips bearer auth for request paths whose prefix matches any exempt.
//     The /auth/* routes need this because the user has no bearer yet at
//     the moment of sign-in.
//  2. Stacks a DB-backed apikeystore.Store as a fallback for tokens that
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
