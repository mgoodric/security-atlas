package api

import (
	"github.com/go-chi/chi/v5"

	artifactsapi "github.com/mgoodric/security-atlas/internal/api/artifacts"
	controlimportsapi "github.com/mgoodric/security-atlas/internal/api/controlimports"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/scope"
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
	// #1775: the SCF remap queue — one proposal per control left on an
	// older SCF release's anchor, filterable by class and status. Literal
	// segment, ahead of the /v1/controls/{id}/... patterns.
	root.Get("/v1/controls/remap-proposals", controlsapi.NewRemapProposalsHandler(controlsStore).List)
	// #1776: a person approves (re-anchoring the control as a superseding
	// revision), rejects or keeps legacy a proposal, or bulk-approves the
	// open unchanged ones.
	controlsapi.NewRemapDecisionsHandler(controlsStore).RegisterRoutes(root)
	// #2176: controls left on a legacy SCF release's anchor that the #1971
	// D3 rule flags for re-anchoring, with remap notes, for the dashboard
	// warning. Literal segment, ahead of the /v1/controls/{id}/... patterns.
	root.Get("/v1/controls/needs-reanchoring", controlsapi.NewReanchoringHandler(controlsStore).List)
	// #1779: bring-your-own-controls import — upload a CSV/XLSX control
	// sheet and get back a staged import batch, every row staged or
	// rejected with a reason. Literal /v1/controls/imports segment, so it
	// sits ahead of the /v1/controls/{id}/... patterns like /export.
	// #1781: candidates are LLM-reranked only for tenants whose cloud-LLM
	// opt-in is on, or (#2079) who opted in to reranking on local Ollama.
	importStore := controlimport.NewStore(s.dbPool)
	stager := controlimport.NewStager(importStore).WithReranker(controlimport.NewReranker(
		s.inferenceClient(),
		controlimport.AuditRecorder(llm.NewAuditWriter(s.dbPool)),
		s.importRerankAllowed(importStore),
	))
	// #1782: approve (creating a control through the bundle store) and
	// reject staged rows; a person decides, never a machine credential.
	// #1784: download the approved rows' controls as control.yaml bundles.
	controlimportsapi.New(stager).WithSettings(importStore).WithDecisions(importStore).
		WithExport(importStore).RegisterRoutes(root)
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
		// #2111: the handler evaluates the control after each attestation
		// with the same engine Evaluate now uses.
		attestH := controlsapi.NewAttestHandler(s.dbPool, s.ingestService, attestUploader(s.artifactStore))
		if s.dbPool != nil {
			attestH.WithEvaluator(eval.NewEngine(eval.NewStore(s.dbPool), scope.NewStore(s.dbPool)))
		}
		root.Get("/v1/controls/{id}/attest-form", attestH.AttestForm)
		root.Post("/v1/controls/{id}/attestations", attestH.Submit)
	}

	// Slice 468: server-backed control owner-assignment (single-item +
	// bulk) + per-user saved filter-views. The single-item path is the
	// prerequisite the bulk path reuses as its per-item check (AC-467-1);
	// the bulk path is the authz-amplifier (AC-11). Both go through the
	// shared assignOwnerInTx so they cannot drift (P0-467-1). The
	// owner-assign routes resolve to action="write", resource="controls"
	// at the authz middleware — admitted for admin / grc_engineer /
	// control_owner, denied for viewer / auditor — identically to every
	// other control write. The ":bulk-assign-owner" colon-suffix is NOT a
	// transition verb, so the bulk route gets the same write-on-controls
	// decision the single route does (the role gate is byte-identical).
	ownerAssignH := controlsapi.NewOwnerAssignHandler(s.dbPool)
	root.Post("/v1/controls/{id}/owner", ownerAssignH.AssignOwner)
	// #2110: Download bundle — the control's control.yaml as a tarball
	// POST /v1/controls:upload-bundle reads back (a read on controls).
	root.Get("/v1/controls/{id}/bundle", controlsapi.NewBundleDownloadHandler(controlsStore).Download)
	root.Post("/v1/controls:bulk-assign-owner", ownerAssignH.BulkAssignOwner)

	// Saved filter-views — a per-user self-service surface (resource type
	// "saved-views" at the authz layer, admitted for any authenticated
	// role via defaults.rego; per-user isolation enforced at the query
	// layer, tenant isolation via RLS — P0-448-5).
	savedViewsH := controlsapi.NewSavedViewsHandler(s.dbPool)
	root.Get("/v1/saved-views", savedViewsH.List)
	root.Post("/v1/saved-views", savedViewsH.Create)
	root.Delete("/v1/saved-views/{id}", savedViewsH.Delete)
}
