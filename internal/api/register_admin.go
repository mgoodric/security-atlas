package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/adminauditperiods"
	"github.com/mgoodric/security-atlas/internal/api/adminauthzbundle"
	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/admincrosswalktier"
	"github.com/mgoodric/security-atlas/internal/api/admindemo"
	"github.com/mgoodric/security-atlas/internal/api/admingroupmappings"
	"github.com/mgoodric/security-atlas/internal/api/adminscim"
	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	"github.com/mgoodric/security-atlas/internal/api/adminsuperadmins"
	"github.com/mgoodric/security-atlas/internal/api/admintenants"
	"github.com/mgoodric/security-atlas/internal/api/adminusers"
	"github.com/mgoodric/security-atlas/internal/api/adminvendors"
	auditapi "github.com/mgoodric/security-atlas/internal/api/audit"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	exceptionsapi "github.com/mgoodric/security-atlas/internal/api/exceptions"
	featuresapi "github.com/mgoodric/security-atlas/internal/api/features"
	policiesapi "github.com/mgoodric/security-atlas/internal/api/policies"
	scimapi "github.com/mgoodric/security-atlas/internal/api/scim"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/scim"
)

// registerAdmin registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerAdmin(root *chi.Mux, featureFlagStore *featureflag.Store) {
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
		// Slice 733: the SCIM /Groups resource. Membership changes drive a
		// re-derivation through the slice-509 grouprole resolver — the SOLE path
		// to a role (P0-733-1 / P0-733-3). The resolver is REUSED via the
		// scimGroupDeriver adapter, never re-implemented. Same router, same
		// per-tenant SCIM credential, same RLS as /Users (P0-733-4).
		scimGroupH := scimapi.NewGroupHandler(
			scim.NewGroupStore(s.dbPool),
			scimGroupDeriver{resolver: grouprole.NewResolver(s.dbPool)},
		)
		root.Group(func(scimR chi.Router) {
			scimR.Use(scimapi.Middleware(s.scimCredStore))
			scimUserH.Mount(scimR)
			scimGroupH.MountGroups(scimR)
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

	// Slice 509: IdP group-to-role mapping CRUD (AC-8). Admin-gated (the
	// handler enforces cred.IsAdmin defense-in-depth alongside the slice 035
	// OPA middleware); editing a mapping is a privilege-granting action so a
	// non-admin caller gets 403. The store runs every op under the caller's
	// tenant RLS context (invariant #6). The derivation surface (the resolver
	// the OIDC + SCIM sources call) is wired separately at those call sites.
	groupMapH := admingroupmappings.New(grouprole.NewStore(s.dbPool))
	root.Post("/v1/admin/group-role-mappings", groupMapH.Create)
	root.Get("/v1/admin/group-role-mappings", groupMapH.List)
	root.Delete("/v1/admin/group-role-mappings/{id}", groupMapH.Delete)

	// Slice 483: crosswalk-mapping verified-tier governance (ADR 0018).
	// Admin-gated (cred.IsAdmin; a non-admin transition is 403 — threat-model
	// E) transition of a fw_to_scf_edges row's trust tier. The store flips the
	// tier and appends an immutable audit row in the same transaction. The
	// target table is a CATALOG table (no tenant RLS); the gate is this
	// admin-role authz + the append-only audit trail. Route appended per the
	// parallel-batch convention (chi.Mux rejects two Mounts at "/").
	crosswalkTierH := admincrosswalktier.New(crosswalktier.NewStore(s.dbPool))
	root.Post("/v1/admin/crosswalk-edges/{id}/tier", crosswalkTierH.Transition)

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
}
