// Package risks serves the slice-019 HTTP API for the risk register. Routes
// (registered onto the platform root router by internal/api/httpserver.go):
//
//	POST   /v1/risks           create
//	GET    /v1/risks           list (optional ?treatment=&category=&methodology= filters)
//	GET    /v1/risks/{id}      get one
//	PATCH  /v1/risks/{id}      move between levels and org units (#1749)
//	DELETE /v1/risks/{id}      delete
//	GET    /v1/risks/heatmap   5x5 grid for nist_800_30 + qualitative_5x5
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). The store opens its own transaction per call and
// applies the tenant GUC.
package risks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// riskLister is the LIST-ONLY read seam the ListRisks (GET /v1/risks) path
// reads through (slice 410). It carries JUST the one List method that
// endpoint needs — deliberately narrow (slice 409 D1: a full read interface
// over the wide risk.Store surface — Create/List/Get/Delete/Heatmap/
// ThemeOrgUnitHeatmap/… — would be a ~7-method refactor a single golden does
// not justify). The contract-tier recorder (handler_contract_test.go) injects
// a fixed-row stub satisfying this seam so the GET /v1/risks wire shape
// records on the plain `go test ./...` unit surface with no Postgres pool
// (ADR-0007 / P0-409-1). The production *risk.Store satisfies it verbatim;
// the seam is unexported and New(*risk.Store) is unchanged (P0-409-2). The
// rest of the handler keeps using the concrete *risk.Store directly.
type riskLister interface {
	List(ctx context.Context, filter risk.ListFilter) ([]risk.Risk, error)
}

// Handler bundles the slice-019 routes over a single risk.Store. Slice 020
// adds an optional ResidualDeriver — when set, GET /v1/risks/{id} returns the
// derived residual + effectiveness breakdown and POST /v1/risks/{id}/controls
// is served. When nil (a deployment without NATS/eval wired), the risk routes
// still work and residual_score is whatever was last persisted.
//
// lister is the slice-410 list-only read seam the ListRisks path reads
// through; New points it at store, so production behavior is identical. Every
// other handler keeps using store directly.
type Handler struct {
	store   *risk.Store
	deriver *risk.ResidualDeriver
	lister  riskLister
}

// New constructs a Handler. The ResidualDeriver is attached separately via
// WithDeriver so the slice-019/053 callers that pass only a Store keep
// working unchanged. The slice-410 list-only seam (lister) is wired to the
// same store — the public signature is unchanged (P0-409-2).
func New(store *risk.Store) *Handler { return &Handler{store: store, lister: store} }

// newHandlerWithLister constructs a Handler whose ListRisks path reads through
// an arbitrary list-only seam. It exists ONLY for the slice-410 contract
// recorder, which injects a fixed-row stub so the GET /v1/risks wire shape
// records with no Postgres pool. Unexported — not part of the public surface.
func newHandlerWithLister(lister riskLister) *Handler {
	return &Handler{lister: lister}
}

// WithDeriver attaches the slice-020 ResidualDeriver and returns the handler
// for chaining. httpserver.go calls this when the eval engine is available.
func (h *Handler) WithDeriver(d *risk.ResidualDeriver) *Handler {
	h.deriver = d
	return h
}

// ----- wire shapes -----

type createReq struct {
	Title               string          `json:"title"`
	Description         string          `json:"description"`
	Category            string          `json:"category"`
	Methodology         string          `json:"methodology"`
	InherentScore       json.RawMessage `json:"inherent_score"`
	Treatment           string          `json:"treatment"`
	AcceptanceStatus    string          `json:"acceptance_status"`
	TreatmentOwner      string          `json:"treatment_owner"`
	ResidualScore       json.RawMessage `json:"residual_score"`
	ReviewDueAt         *time.Time      `json:"review_due_at,omitempty"`
	AcceptedUntil       *string         `json:"accepted_until,omitempty"` // YYYY-MM-DD
	Accepter            string          `json:"accepter"`
	InstrumentReference string          `json:"instrument_reference"`
	LinkedControlIDs    []string        `json:"linked_control_ids"`
	// Level and OrgUnitID place the risk in the hierarchy (#1749). Both
	// are optional: with an org unit and no level the risk takes the
	// unit's level; with neither it is a team risk with no unit.
	Level     string  `json:"level"`
	OrgUnitID *string `json:"org_unit_id"`
}

type riskWire struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Category      string          `json:"category"`
	Methodology   string          `json:"methodology"`
	InherentScore json.RawMessage `json:"inherent_score"`
	Treatment     string          `json:"treatment"`
	// AcceptanceStatus is proposed | approved for treatment=accept and
	// omitted otherwise (#1746).
	AcceptanceStatus    string          `json:"acceptance_status,omitempty"`
	TreatmentOwner      string          `json:"treatment_owner"`
	ResidualScore       json.RawMessage `json:"residual_score"`
	ReviewDueAt         *time.Time      `json:"review_due_at,omitempty"`
	AcceptedUntil       *string         `json:"accepted_until,omitempty"`
	Accepter            string          `json:"accepter"`
	InstrumentReference string          `json:"instrument_reference"`
	LinkedControlIDs    []string        `json:"linked_control_ids"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	// Slice 067 (AC-2): hierarchy + severity fields surfaced so the
	// slice-056 org tree and downstream panels can attribute a risk
	// without a second round-trip. Additive — existing fields and
	// filters are unchanged.
	//
	//	OrgUnitID — the slice-052 org_unit binding; omitempty (nil for an
	//	            unbound risk, which is valid — smaller orgs may not
	//	            configure org_units at all).
	//	Themes    — the slice-053 theme slugs; always an array (never
	//	            null) so the wire shape is stable.
	//	Severity  — the 5×5 severity scalar (likelihood × impact, or the
	//	            explicit aggregate `severity` for a slice-053 parent
	//	            risk). 0 for a non-5×5 / malformed inherent_score —
	//	            computed, not stored (risks has no severity column).
	OrgUnitID *string `json:"org_unit_id,omitempty"`
	// Level is the risk's hierarchy tier: team | org | company (#1749).
	Level    string   `json:"level"`
	Themes   []string `json:"themes"`
	Severity int      `json:"severity"`
	// RollUp is the severity computed from the risk's children (#1748),
	// beside its own assessed Severity. Set on GET /v1/risks/{id} for a
	// risk with at least one scored child; omitted otherwise.
	RollUp *rollUpWire `json:"rollup,omitempty"`
}

// CreateRisk handles POST /v1/risks (AC-1..AC-5).
func (h *Handler) CreateRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Title == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Category == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "category is required")
		return
	}
	methodology := dbx.RiskMethodology(req.Methodology)
	if methodology == "" {
		methodology = risk.DefaultMethodology // AC-1: default nist_800_30
	}
	// An omitted treatment is stored as untreated (the store applies the
	// default): no decision is recorded that the caller did not make.
	treatment := dbx.RiskTreatment(req.Treatment)
	linkedIDs, err := parseUUIDs(req.LinkedControlIDs)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "linked_control_ids: "+err.Error())
		return
	}
	acceptedUntil, err := parseDatePtr(req.AcceptedUntil)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "accepted_until must be YYYY-MM-DD")
		return
	}
	orgUnitID, err := parseUUIDPtr(req.OrgUnitID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "org_unit_id must be a UUID")
		return
	}

	in := risk.CreateInput{
		Title:               req.Title,
		Description:         req.Description,
		Category:            dbx.RiskCategory(req.Category),
		Methodology:         methodology,
		InherentScore:       []byte(req.InherentScore),
		Treatment:           treatment,
		AcceptanceStatus:    dbx.RiskAcceptanceStatus(req.AcceptanceStatus),
		TreatmentOwner:      req.TreatmentOwner,
		ResidualScore:       []byte(req.ResidualScore),
		ReviewDueAt:         req.ReviewDueAt,
		AcceptedUntil:       acceptedUntil,
		Accepter:            req.Accepter,
		InstrumentReference: req.InstrumentReference,
		LinkedControlIDs:    linkedIDs,
		Level:               dbx.RiskLevel(req.Level),
		OrgUnitID:           orgUnitID,
	}
	created, err := h.store.Create(ctx, in)
	if err != nil {
		switch {
		case risk.IsTreatmentValidation(err):
			// The MCP confirm surface returns the same code for the same
			// refusal (internal/api/mcpwriteproposals, #1844).
			httpresp.WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
				"code":  "treatment_validation_failed",
			})
		case errors.Is(err, risk.ErrInvalidMethodology),
			errors.Is(err, risk.ErrInherentScoreInvalid),
			risk.IsPlacementValidation(err):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			httperr.WriteInternal(w, r, "create risk", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"risk": riskWireFrom(created)})
}

// ListRisks handles GET /v1/risks (AC-6).
//
// Filters (slice 019): ?treatment= ?category= ?methodology=.
// Ordering (slice 066, AC-3): ?sort=residual,age ranks the result by
// residual-score magnitude descending then risk age ascending — the
// program dashboard's "top risks aging" panel.
// Filters (slice 067, AC-4): ?theme=<slug> ?org_unit=<uuid> narrow to the
// risks contributing to one heatmap cell — slice 056's heatmap-cell-click
// side panel.
//
// Every filter and the ?sort= ordering are additive and optional, and all
// compose: omitting them all keeps the slice-019 default newest-first
// unfiltered list.
func (h *Handler) ListRisks(w http.ResponseWriter, r *http.Request) {
	// Slice 067 (AC-6): handler-level program-read guard. Runs FIRST —
	// before tenant resolution — so an unauthorized role is a clean 403
	// regardless of tenant context.
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	sortOrder, err := risk.ParseListSort(r.URL.Query().Get("sort"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter := risk.ListFilter{
		Treatment:   dbx.RiskTreatment(r.URL.Query().Get("treatment")),
		Category:    dbx.RiskCategory(r.URL.Query().Get("category")),
		Methodology: dbx.RiskMethodology(r.URL.Query().Get("methodology")),
		Sort:        sortOrder,
		Theme:       r.URL.Query().Get("theme"),
	}
	// Slice 067 (AC-4): ?org_unit= is an optional UUID. A present-but-
	// malformed value is a 400 rather than a silently-ignored filter.
	if raw := r.URL.Query().Get("org_unit"); raw != "" {
		ouID, perr := uuid.Parse(raw)
		if perr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "org_unit must be a UUID")
			return
		}
		filter.OrgUnitID = &ouID
	}
	risks, err := h.lister.List(ctx, filter)
	if err != nil {
		httperr.WriteInternal(w, r, "list risks", err)
		return
	}
	out := make([]riskWire, len(risks))
	for i, rk := range risks {
		out[i] = riskWireFrom(rk)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"risks": out, "count": len(out)})
}

// ThemeHeatmap handles GET /v1/risks/theme-heatmap (slice 067, AC-3) —
// the themes × org_units aggregation that is slice 056's heatmap panel's
// central data source. Each populated cell is
// {theme, theme_builtin, org_unit_id, risk_count, aggregate_severity};
// cells with zero contributing risks are omitted (the frontend renders
// the full grid and treats an absent (theme, org_unit) pair as zero).
//
// Cells arrive ordered built-in-themes-first, then theme name, then
// org_unit — matching slice 056 AC-3's rendering contract (defaults
// ordered left of tenant-private themes). The `theme` field is the theme
// SLUG (the same identity GET /v1/themes returns and risks.themes stores)
// — not a UUID; see the slice-067 decisions log.
//
// No tenant_id is read from the query or body — the tenant comes solely
// from the slice-033 middleware (slice 033 D1).
func (h *Handler) ThemeHeatmap(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cells, err := h.store.ThemeOrgUnitHeatmap(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "theme heatmap", err)
		return
	}
	type cellWire struct {
		Theme             string `json:"theme"`
		ThemeBuiltin      bool   `json:"theme_builtin"`
		OrgUnitID         string `json:"org_unit_id"`
		RiskCount         int    `json:"risk_count"`
		AggregateSeverity int    `json:"aggregate_severity"`
	}
	out := make([]cellWire, len(cells))
	for i, c := range cells {
		out[i] = cellWire{
			Theme:             c.Theme,
			ThemeBuiltin:      c.ThemeBuiltin,
			OrgUnitID:         c.OrgUnitID.String(),
			RiskCount:         c.RiskCount,
			AggregateSeverity: c.AggregateSeverity,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"cells": out, "count": len(out)})
}

// GetRisk handles GET /v1/risks/{id}.
func (h *Handler) GetRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	rk, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
			return
		}
		httperr.WriteInternal(w, r, "get risk", err)
		return
	}
	wire := riskWireFrom(rk)
	_, rollUp, err := h.store.ListChildren(ctx, id)
	if err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			// Deleted between the two reads.
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
			return
		}
		httperr.WriteInternal(w, r, "risk roll-up", err)
		return
	}
	if rollUp != nil {
		ru := rollUpWireFrom(*rollUp)
		wire.RollUp = &ru
	}
	body := map[string]any{"risk": wire}
	// Slice 020 (AC-2): when the residual deriver is wired, return the
	// live-derived residual + the per-linked-control effectiveness breakdown.
	// Derive is the pure read path — recompute=false, so a GET never triggers
	// evaluation (the NATS subscriber + scheduler own that).
	if h.deriver != nil {
		res, derr := h.deriver.Derive(ctx, id, false)
		if derr != nil {
			httperr.WriteInternal(w, r, "derive residual", derr)
			return
		}
		body["residual"] = residualWireFrom(res)
	}
	httpresp.WriteJSON(w, http.StatusOK, body)
}

// patchReq is the PATCH /v1/risks/{id} body. OrgUnitID is raw so the
// handler can tell an omitted field (keep the unit) from an explicit null
// (unbind it).
type patchReq struct {
	Level     *string         `json:"level"`
	OrgUnitID json.RawMessage `json:"org_unit_id"`
}

// PatchRisk handles PATCH /v1/risks/{id} (#1749): move a risk between
// hierarchy levels and org units. Only `level` and `org_unit_id` are
// accepted; any other field is a 400, so a caller never mistakes this for a
// general update. Each change writes one risk_audit_log row naming the
// caller. A level that differs from the target unit's level is a 400.
func (h *Handler) PatchRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	in, msg := decodePatch(r)
	if msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	in.Actor = actorFrom(r)
	updated, err := h.store.UpdatePlacement(ctx, id, in)
	if err != nil {
		switch {
		case errors.Is(err, risk.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
		case risk.IsPlacementValidation(err):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			httperr.WriteInternal(w, r, "update risk placement", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"risk": riskWireFrom(updated)})
}

// decodePatch parses a PATCH /v1/risks/{id} body into a PlacementInput. A
// non-empty message is the 400 to return.
func decodePatch(r *http.Request) (risk.PlacementInput, string) {
	var req patchReq
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return risk.PlacementInput{}, "invalid JSON body: only level and org_unit_id can be changed"
	}
	var in risk.PlacementInput
	if req.Level != nil {
		if *req.Level == "" {
			return risk.PlacementInput{}, "level must be one of: team, org, company"
		}
		in.Level = dbx.RiskLevel(*req.Level)
	}
	if req.OrgUnitID != nil {
		in.OrgUnitSet = true
		if string(req.OrgUnitID) != "null" {
			var s string
			if err := json.Unmarshal(req.OrgUnitID, &s); err != nil {
				return risk.PlacementInput{}, "org_unit_id must be a UUID or null"
			}
			ouID, err := uuid.Parse(s)
			if err != nil {
				return risk.PlacementInput{}, "org_unit_id must be a UUID or null"
			}
			in.OrgUnitID = &ouID
		}
	}
	if req.Level == nil && !in.OrgUnitSet {
		return risk.PlacementInput{}, "body must set level and/or org_unit_id"
	}
	return in, ""
}

// actorFrom names the caller on the audit row: the credential's id, or a
// non-empty sentinel when no credential is in context (unit-test servers),
// so the audit log's actor CHECK never trips. Same rule as the aggregation
// rules handler.
func actorFrom(r *http.Request) string {
	if cred, ok := authctx.CredentialFromContext(r.Context()); ok && cred.ID != "" {
		return cred.ID
	}
	return "system"
}

// DeleteRisk handles DELETE /v1/risks/{id}.
func (h *Handler) DeleteRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if err := h.store.Delete(ctx, id); err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
			return
		}
		httperr.WriteInternal(w, r, "delete risk", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Heatmap handles GET /v1/risks/heatmap (AC-7). Returns a sparse list of
// occupied cells plus the full 5x5 matrix (zeroes inclusive) so the frontend
// can render without a second pass.
func (h *Handler) Heatmap(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cells, err := h.store.Heatmap(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "heatmap", err)
		return
	}
	// Dense 5x5 view — likelihood rows (1..5), impact cols (1..5).
	grid := [6][6]int{} // index 0 unused; 1..5 is the data range
	for _, c := range cells {
		if c.Likelihood < 1 || c.Likelihood > 5 || c.Impact < 1 || c.Impact > 5 {
			continue
		}
		grid[c.Likelihood][c.Impact] = c.Count
	}
	denseRows := make([][]int, 5)
	for i := 0; i < 5; i++ {
		row := make([]int, 5)
		for j := 0; j < 5; j++ {
			row[j] = grid[i+1][j+1]
		}
		denseRows[i] = row
	}
	type cellOut struct {
		Likelihood int `json:"likelihood"`
		Impact     int `json:"impact"`
		Count      int `json:"count"`
	}
	sparse := make([]cellOut, 0, len(cells))
	for _, c := range cells {
		if c.Likelihood < 1 || c.Likelihood > 5 || c.Impact < 1 || c.Impact > 5 {
			continue
		}
		sparse = append(sparse, cellOut{Likelihood: c.Likelihood, Impact: c.Impact, Count: c.Count})
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"cells": sparse,
		"grid":  denseRows,
		"shape": "5x5",
	})

}

// ----- helpers -----

func (h *Handler) tenantContext(r *http.Request) (context.Context, bool) {
	// Slice 033: the tenancy.Middleware (mounted in httpserver.go after
	// the bearer-auth middleware) already lifted cred.TenantID onto
	// r.Context() via tenancy.WithTenant. We confirm a tenant is set
	// (which doubles as a "credential is present" check on the
	// bearer-auth'd path: no credential → no tenant → 401-shaped path).
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

func riskWireFrom(r risk.Risk) riskWire {
	w := riskWire{
		ID:                  r.ID.String(),
		Title:               r.Title,
		Description:         r.Description,
		Category:            string(r.Category),
		Methodology:         string(r.Methodology),
		InherentScore:       jsonRaw(r.InherentScore),
		Treatment:           string(r.Treatment),
		AcceptanceStatus:    string(r.AcceptanceStatus),
		TreatmentOwner:      r.TreatmentOwner,
		ResidualScore:       jsonRaw(r.ResidualScore),
		Accepter:            r.Accepter,
		InstrumentReference: r.InstrumentReference,
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
	}
	if r.ReviewDueAt != nil {
		t := *r.ReviewDueAt
		w.ReviewDueAt = &t
	}
	if r.AcceptedUntil != nil {
		s := r.AcceptedUntil.Format("2006-01-02")
		w.AcceptedUntil = &s
	}
	w.LinkedControlIDs = make([]string, len(r.LinkedControlIDs))
	for i, id := range r.LinkedControlIDs {
		w.LinkedControlIDs[i] = id.String()
	}
	// Slice 067 (AC-2): hierarchy + severity fields.
	if r.OrgUnitID != nil {
		s := r.OrgUnitID.String()
		w.OrgUnitID = &s
	}
	w.Level = string(r.Level)
	w.Themes = append([]string{}, r.Themes...) // never null on the wire
	w.Severity = severityOf(r.InherentScore)
	return w
}

// severityOf computes the 5×5 severity scalar from a risk's inherent_score
// JSONB. Slice 067 (AC-2): severity is NOT a stored column — it is
// derived. An aggregated parent risk (slice 053) carries an explicit
// numeric `severity` field, which takes precedence; otherwise severity is
// likelihood × impact on the 5×5 grid (canvas §6.6). A score missing a
// numeric severity component yields 0 — the same graceful-degradation
// posture as the residual-magnitude sort (slice 066): a malformed score
// ranks as the least severe rather than erroring the response.
func severityOf(inherentScore []byte) int {
	return risk.AssessedSeverity(inherentScore)
}

func jsonRaw(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func parseUUIDs(in []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func parseUUIDPtr(s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func parseDatePtr(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
