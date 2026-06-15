package api

import (
	"github.com/go-chi/chi/v5"

	auditapi "github.com/mgoodric/security-atlas/internal/api/audit"
	auditperiodsapi "github.com/mgoodric/security-atlas/internal/api/auditperiods"
	oscalexportapi "github.com/mgoodric/security-atlas/internal/api/oscalexport"
	walkthroughsapi "github.com/mgoodric/security-atlas/internal/api/walkthroughs"
	"github.com/mgoodric/security-atlas/internal/audit"
	auditperiod "github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
)

// registerAudit registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerAudit(root *chi.Mux) {
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
}
