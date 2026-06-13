package api

import (
	"github.com/go-chi/chi/v5"

	aggregationrulesapi "github.com/mgoodric/security-atlas/internal/api/aggregationrules"
	orgunitsapi "github.com/mgoodric/security-atlas/internal/api/orgunits"
	risksapi "github.com/mgoodric/security-atlas/internal/api/risks"
	themesapi "github.com/mgoodric/security-atlas/internal/api/themes"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// registerRisk registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerRisk(root *chi.Mux, risksStore *risk.Store) {
	// Slice 019: risk register CRUD + 5x5 heatmap. Routes appended per the
	// parallel-batch convention — chi.Mux rejects two Mounts at "/", and other
	// batches are also appending here, so order-of-append must not matter.
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
}
