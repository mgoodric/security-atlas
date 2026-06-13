package api

import (
	"github.com/go-chi/chi/v5"

	artifactsapi "github.com/mgoodric/security-atlas/internal/api/artifacts"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/control"
)

// registerControls registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerControls(root *chi.Mux) {
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
}
