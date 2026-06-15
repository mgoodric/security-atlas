package api

import (
	"github.com/go-chi/chi/v5"

	dashboardapi "github.com/mgoodric/security-atlas/internal/api/dashboard"
	dashboardexportapi "github.com/mgoodric/security-atlas/internal/api/dashboardexport"
	freshnessdriftapi "github.com/mgoodric/security-atlas/internal/api/freshnessdrift"
	searchapi "github.com/mgoodric/security-atlas/internal/api/search"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// registerDashboard registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerDashboard(root *chi.Mux, risksStore *risk.Store, freshnessStore *freshness.Store, driftStore *drift.Store) {
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
}
