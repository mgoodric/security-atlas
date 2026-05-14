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
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	controlstateapi "github.com/mgoodric/security-atlas/internal/api/controlstate"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	exceptionsapi "github.com/mgoodric/security-atlas/internal/api/exceptions"
	featuresapi "github.com/mgoodric/security-atlas/internal/api/features"
	fwscopesapi "github.com/mgoodric/security-atlas/internal/api/frameworkscopes"
	meapi "github.com/mgoodric/security-atlas/internal/api/me"
	orgunitsapi "github.com/mgoodric/security-atlas/internal/api/orgunits"
	policiesapi "github.com/mgoodric/security-atlas/internal/api/policies"
	policyacksapi "github.com/mgoodric/security-atlas/internal/api/policyacks"
	risksapi "github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/scopes"
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
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
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
	root.Use(corsMiddleware)
	// Slice 034: /auth/* (login/callback/logout) is bearer-exempt — users
	// don't have a bearer yet at the moment they sign in. The middleware
	// must skip the prefix. Note: /v1/admin/credentials* DOES go through
	// bearer auth (admin endpoints require an admin credential).
	root.Use(httpAuthMiddlewareWithExemptions(s.credStore, s.apikeyStore, "/auth/"))
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
		root.Use(authzmw.Middleware(s.authzEngine, s.authzAudit, "/auth/", "/health"))
	}
	// Slice 059: per-request feature-flag cache. Attached AFTER auth /
	// tenancy / authz so the cache lives inside the same request-context
	// every downstream handler sees. Anti-criterion P0: no cross-request
	// cache -- the cache is created fresh per request and dies when the
	// request ends.
	root.Use(featureflag.CacheMiddleware)

	queries := dbx.New(s.dbPool)
	root.Mount("/", anchors.New(queries).Routes())
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
	risksH := risksapi.New(risksStore)
	root.Post("/v1/risks", risksH.CreateRisk)
	root.Get("/v1/risks", risksH.ListRisks)
	root.Get("/v1/risks/heatmap", risksH.Heatmap)
	// Slice 053: manual aggregation + live recompute. Literal-segment
	// routes (/aggregate, /{id}/aggregation) declared before the generic
	// /v1/risks/{id} so chi's declaration-order match keeps them ahead.
	root.Post("/v1/risks/aggregate", risksH.Aggregate)
	root.Get("/v1/risks/{id}/aggregation", risksH.LiveAggregation)
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
	// Slice 022: policy library. Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). Sub-resource transitions
	// (submit/approve/publish) are declared before /{id} so chi's
	// declaration-order match keeps the literal-segment routes first
	// within the same method. Approve + Publish enforce IsApprover at the
	// handler (slice 034 credential flag).
	policiesH := policiesapi.New(policy.NewStore(s.dbPool))
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
