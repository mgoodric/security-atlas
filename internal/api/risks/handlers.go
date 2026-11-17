// Package risks serves the slice-019 HTTP API for the risk register. Routes
// (registered onto the platform root router by internal/api/httpserver.go):
//
//	POST   /v1/risks           create
//	GET    /v1/risks           list (optional ?treatment=&category=&methodology= filters)
//	GET    /v1/risks/{id}      get one
//	PATCH  /v1/risks/{id}      edit record fields (#2104); move between levels and org units (#1749)
//	GET    /v1/risks/{id}/audit-log the risk's change history (#2104)
//	DELETE /v1/risks/{id}      delete
//	GET    /v1/risks/heatmap   5x5 grid for nist_800_30 + qualitative_5x5
//	GET    /v1/risks/categories the tenant's risk categories (#1809)
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). The store opens its own transaction per call and
// applies the tenant GUC.
package risks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
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
//
// #2132: the seam is ListPage, the filtered, sorted and paged read, which
// also returns the filtered total.
type riskLister interface {
	ListPage(ctx context.Context, filter risk.PageFilter) ([]risk.Risk, int64, error)
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
	views   *moduleaccess.Views
}

// WithModules wires the module views the owner_lacks_access flag reads
// (#2174). Without them (a unit-test server) no owner is flagged.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// flagOwners sets owner_lacks_access on each risk from its owner's live
// module access. An error fails the read rather than drop the flag.
func (h *Handler) flagOwners(r *http.Request, out []riskWire) error {
	check, err := h.views.Owners(r, "risk")
	if err != nil {
		return err
	}
	for i := range out {
		if out[i].OwnerUserID == nil {
			continue
		}
		if out[i].OwnerLacksAccess, err = check.Lacks(*out[i].OwnerUserID); err != nil {
			return err
		}
	}
	return nil
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
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Category         string          `json:"category"`
	Methodology      string          `json:"methodology"`
	InherentScore    json.RawMessage `json:"inherent_score"`
	Treatment        string          `json:"treatment"`
	AcceptanceStatus string          `json:"acceptance_status"`
	TreatmentOwner   string          `json:"treatment_owner"`
	// OwnerUserID is the owner, an active user of the tenant (#2129).
	OwnerUserID         *string         `json:"owner_user_id"`
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
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Category is the slug of the organisation category the risk
	// references (#1812); CategoryName is its display name. Seeded slugs
	// equal the old enum values, so existing clients read the same value.
	Category      string          `json:"category"`
	CategoryID    string          `json:"category_id,omitempty"`
	CategoryName  string          `json:"category_name,omitempty"`
	Methodology   string          `json:"methodology"`
	InherentScore json.RawMessage `json:"inherent_score"`
	Treatment     string          `json:"treatment"`
	// AcceptanceStatus is proposed | approved for treatment=accept and
	// omitted otherwise (#1746).
	AcceptanceStatus string `json:"acceptance_status,omitempty"`
	// OwnerUserID is the owner, a user of the tenant, or null (#2129).
	// OwnerUnlinked is the free-text owner no user matched, shown as
	// "Owner (unlinked)"; it is empty whenever OwnerUserID is set.
	// TreatmentOwner is the stored free text, kept for older clients.
	OwnerUserID    *string `json:"owner_user_id"`
	OwnerUnlinked  string  `json:"owner_unlinked"`
	TreatmentOwner string  `json:"treatment_owner"`
	// OwnerLacksAccess (#2174), computed at read time and present only
	// when true: the owner can no longer work in Risk. The owner is kept.
	OwnerLacksAccess    bool            `json:"owner_lacks_access,omitempty"`
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
	// ChildCount is how many child risks roll up into this one (#2060).
	// Set on GET /v1/risks; omitted for a risk that is not a roll-up.
	ChildCount int `json:"child_count,omitempty"`
	// Acceptance is the proposed or approved acceptance's record (#2105):
	// present when treatment=accept, omitted otherwise.
	Acceptance *acceptanceWire `json:"acceptance,omitempty"`
	// Status is open | closed (#2107). Closure is present when closed.
	Status  string       `json:"status"`
	Closure *closureWire `json:"closure,omitempty"`
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
	ownerUserID, err := parseUUIDPtr(req.OwnerUserID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "owner_user_id must be a UUID")
		return
	}

	in := risk.CreateInput{
		Title:               req.Title,
		Description:         req.Description,
		CategoryRef:         req.Category,
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
		OwnerUserID:         ownerUserID,
	}
	// A create proposing an acceptance records its creator as the proposer,
	// as the propose transition does, so they cannot approve it (#2371).
	if cred, ok := authctx.CredentialFromContext(r.Context()); ok {
		in.Proposer = credstore.PrincipalOf(cred)
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
		case errors.Is(err, risk.ErrScoreNotNumeric):
			httpresp.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, risk.ErrInvalidMethodology),
			errors.Is(err, risk.ErrInherentScoreInvalid),
			risk.IsPlacementValidation(err),
			risk.IsCategoryRefusal(err):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrOwnerNotActiveUser):
			httpresp.WriteError(w, http.StatusUnprocessableEntity, "owner_user_id: target owner is not an active user in this tenant")
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
// Roll-ups (#2060): a risk with at least one child linked is left out
// unless ?include_rollups=true, so the register's rows and count never show
// a parent beside the children it summarises. Included roll-ups carry
// child_count.
//
// Every filter and the ?sort= ordering are additive and optional, and all
// compose: omitting them all keeps the slice-019 default newest-first
// list.
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
	filter, msg := parsePageFilter(r.URL.Query())
	if msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	risks, total, err := h.lister.ListPage(ctx, filter)
	if err != nil {
		if risk.IsUnknownCategory(err) {
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httperr.WriteInternal(w, r, "list risks", err)
		return
	}
	out := make([]riskWire, len(risks))
	for i, rk := range risks {
		out[i] = riskWireFrom(rk)
	}
	if err := h.flagOwners(r, out); err != nil {
		httperr.WriteInternal(w, r, "risk owner access", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"risks":  out,
		"count":  len(out),
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

// Register paging (#2132). One page is at most 500 risks, the controls
// list's cap (#2131); with no ?limit= a caller gets a full page, so the
// pickers that read the whole list keep working at v1 tenant sizes.
const (
	listDefaultLimit int32 = 500
	listMaxLimit     int32 = 500
)

// ownerUnowned is the `?owner=` value that keeps the risks with no owner
// user (#2132).
const ownerUnowned = "none"

// parsePageFilter reads GET /v1/risks's filters, sort and paging (#2132).
// Every value is checked against an allow-list or parsed as a UUID or
// date; the returned string is the 400 message.
func parsePageFilter(q url.Values) (risk.PageFilter, string) {
	f := risk.PageFilter{
		Status:     dbx.RiskStatusOpen,
		Categories: categoryRefs(q["category"]),
		Theme:      q.Get("theme"),
		Limit:      listDefaultLimit,
	}
	sortOrder, err := risk.ParsePageSort(q.Get("sort"))
	if err != nil {
		return f, err.Error()
	}
	f.Sort = sortOrder

	// Status: open (the default) | closed | all. The older
	// include_closed=true still means all (#2107).
	if raw := q.Get("include_closed"); raw != "" {
		include, perr := strconv.ParseBool(raw)
		if perr != nil {
			return f, "include_closed must be true or false"
		}
		if include {
			f.Status = ""
		}
	}
	switch v := q.Get("status"); v {
	case "":
	case "all":
		f.Status = ""
	case string(dbx.RiskStatusOpen), string(dbx.RiskStatusClosed):
		f.Status = dbx.RiskStatus(v)
	default:
		return f, "status must be one of open|closed|all"
	}

	if v := dbx.RiskTreatment(q.Get("treatment")); v != "" {
		if !slices.Contains(dbx.AllRiskTreatmentValues(), v) {
			return f, "treatment must be one of accept|mitigate|transfer|avoid|untreated"
		}
		f.Treatment = v
	}
	if v := dbx.RiskMethodology(q.Get("methodology")); v != "" {
		if !slices.Contains(dbx.AllRiskMethodologyValues(), v) {
			return f, "methodology is not a known risk methodology"
		}
		f.Methodology = v
	}
	if v := dbx.RiskLevel(q.Get("level")); v != "" {
		if !slices.Contains(dbx.AllRiskLevelValues(), v) {
			return f, "level must be one of team|org|company"
		}
		f.Level = v
	}
	// #2490: ?severity_band=high|medium|low, the sidebar badge's count.
	if v := q.Get("severity_band"); v != "" {
		if _, _, ok := risk.SeverityBandRange(v); !ok {
			return f, risk.ErrInvalidSeverityBand.Error()
		}
		f.SeverityBand = v
	}
	// Slice 067 (AC-4): ?org_unit= is an optional UUID. A present-but-
	// malformed value is a 400 rather than a silently-ignored filter.
	if raw := q.Get("org_unit"); raw != "" {
		ouID, perr := uuid.Parse(raw)
		if perr != nil {
			return f, "org_unit must be a UUID"
		}
		f.OrgUnitID = &ouID
	}
	// #2132: ?owner=<user id> keeps that person's risks, ?owner=none the
	// risks with no owner user. RLS keeps another tenant's users out, so a
	// foreign id matches nothing.
	switch v := q.Get("owner"); v {
	case "":
	case ownerUnowned:
		f.Unowned = true
	default:
		id, perr := uuid.Parse(v)
		if perr != nil {
			return f, "owner must be a UUID or none"
		}
		f.Owner = &id
	}
	switch v := risk.ReviewDue(q.Get("review_due")); v {
	case risk.ReviewDueAny, risk.ReviewDueOverdue, risk.ReviewDueSoon:
		f.ReviewDue = v
	default:
		return f, "review_due must be one of overdue|due_soon"
	}
	if raw := q.Get("review_due_from"); raw != "" {
		d, perr := time.Parse(time.DateOnly, raw)
		if perr != nil {
			return f, "review_due_from must be a date (YYYY-MM-DD)"
		}
		f.DueFrom = &d
	}
	if raw := q.Get("review_due_to"); raw != "" {
		d, perr := time.Parse(time.DateOnly, raw)
		if perr != nil {
			return f, "review_due_to must be a date (YYYY-MM-DD)"
		}
		f.DueTo = &d
	}
	if raw := q.Get("include_rollups"); raw != "" {
		include, perr := strconv.ParseBool(raw)
		if perr != nil {
			return f, "include_rollups must be true or false"
		}
		f.IncludeRollUps = include
	}
	if raw := q.Get("limit"); raw != "" {
		n, perr := strconv.ParseInt(raw, 10, 32)
		if perr != nil || n <= 0 {
			return f, "limit must be a positive integer"
		}
		f.Limit = int32(min(n, int64(listMaxLimit)))
	}
	if raw := q.Get("offset"); raw != "" {
		n, perr := strconv.ParseInt(raw, 10, 32)
		if perr != nil || n < 0 {
			return f, "offset must be a non-negative integer"
		}
		f.Offset = int32(n)
	}
	return f, ""
}

// categoryRefs flattens ?category= values into category refs (#1812). The
// filter takes several categories as repeated parameters
// (?category=a&category=b), comma-separated (?category=a,b), or both.
// Empty items are dropped.
func categoryRefs(values []string) []string {
	var out []string
	for _, v := range values {
		for _, ref := range strings.Split(v, ",") {
			if ref = strings.TrimSpace(ref); ref != "" {
				out = append(out, ref)
			}
		}
	}
	return out
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
	flagged := []riskWire{wire}
	if err := h.flagOwners(r, flagged); err != nil {
		httperr.WriteInternal(w, r, "risk owner access", err)
		return
	}
	body := map[string]any{"risk": flagged[0]}
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
	// Category moves the risk onto another of the tenant's categories, by
	// slug, id or display name (#1812, #1815).
	Category *string `json:"category"`
	// The record fields (#2104). An omitted field keeps its value.
	Title          *string         `json:"title"`
	Description    *string         `json:"description"`
	Methodology    *string         `json:"methodology"`
	InherentScore  json.RawMessage `json:"inherent_score"`
	TreatmentOwner *string         `json:"treatment_owner"`
	// OwnerUserID sets the owner (a user id) or clears it (null) (#2129).
	OwnerUserID         json.RawMessage `json:"owner_user_id"`
	ReviewDueAt         json.RawMessage `json:"review_due_at"`
	InstrumentReference *string         `json:"instrument_reference"`
}

// patchRefusedFields are the risk fields PATCH does not change (#2104):
// treatment and acceptance are decisions with their own rules, and the
// residual score and control links are derived or linked elsewhere. Each
// is named in the 400 rather than reported as an unknown field.
var patchRefusedFields = []string{
	"treatment", "acceptance_status", "accepted_until", "accepter",
	"residual_score", "linked_control_ids",
}

// patchFieldList names the fields PATCH /v1/risks/{id} accepts.
const patchFieldList = "level, org_unit_id, category, title, description, methodology, " +
	"inherent_score, treatment_owner, owner_user_id, review_due_at, instrument_reference"

// PatchRisk handles PATCH /v1/risks/{id}: move a risk between hierarchy
// levels and org units (#1749), onto another category (#1812), and change
// its record fields (#2104): title, description, methodology,
// inherent_score, treatment_owner, owner_user_id, review_due_at,
// instrument_reference. An owner_user_id that is not an active user of the
// tenant is a 422 (#2129).
// A treatment or acceptance field is a 400 naming it, as is any other
// unknown field. Each change writes one risk_audit_log row naming the
// caller, with the before and after values; a PATCH that changes nothing
// writes none. A level that differs from the target unit's level is a
// 400, as is an unknown or archived category, an empty title, or an
// inherent score that does not fit the methodology; a risk already on an
// archived category can be moved off it.
func (h *Handler) PatchRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	actor, ok := actorFrom(w, r)
	if !ok {
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
	in.Actor = actor
	updated, err := h.store.UpdatePlacement(ctx, id, in)
	if err != nil {
		switch {
		case errors.Is(err, risk.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
		case errors.Is(err, risk.ErrScoreNotNumeric):
			httpresp.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		case risk.IsPlacementValidation(err), risk.IsCategoryRefusal(err),
			risk.IsRecordValidation(err):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrOwnerNotActiveUser):
			httpresp.WriteError(w, http.StatusUnprocessableEntity, "owner_user_id: target owner is not an active user in this tenant")
		default:
			httperr.WriteInternal(w, r, "update risk", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"risk": riskWireFrom(updated)})
}

// decodePatch parses a PATCH /v1/risks/{id} body into a PlacementInput. A
// non-empty message is the 400 to return.
func decodePatch(r *http.Request) (risk.PlacementInput, string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return risk.PlacementInput{}, "invalid JSON body"
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		return risk.PlacementInput{}, "invalid JSON body: want an object"
	}
	for _, f := range patchRefusedFields {
		if _, ok := keys[f]; ok {
			return risk.PlacementInput{}, f + " cannot be changed by PATCH /v1/risks/{id}: treatment and acceptance are not record fields"
		}
	}
	var req patchReq
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return risk.PlacementInput{}, "invalid JSON body: " + patchDecodeMessage(err)
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
	if req.Category != nil {
		if *req.Category == "" {
			return risk.PlacementInput{}, "category must be a category slug, id or display name"
		}
		in.CategoryRef = *req.Category
	}
	f := &in.Fields
	f.Title, f.Description = req.Title, req.Description
	f.TreatmentOwner, f.InstrumentReference = req.TreatmentOwner, req.InstrumentReference
	if req.Methodology != nil {
		m := dbx.RiskMethodology(*req.Methodology)
		f.Methodology = &m
	}
	if req.InherentScore != nil {
		if string(req.InherentScore) == "null" {
			return risk.PlacementInput{}, "inherent_score cannot be null"
		}
		f.InherentScore = req.InherentScore
	}
	if req.OwnerUserID != nil {
		f.OwnerUserSet = true
		if string(req.OwnerUserID) != "null" {
			var s string
			if err := json.Unmarshal(req.OwnerUserID, &s); err != nil {
				return risk.PlacementInput{}, "owner_user_id must be a UUID or null"
			}
			ownerID, err := uuid.Parse(s)
			if err != nil {
				return risk.PlacementInput{}, "owner_user_id must be a UUID or null"
			}
			f.OwnerUserID = &ownerID
		}
	}
	if req.ReviewDueAt != nil {
		var t time.Time
		if string(req.ReviewDueAt) == "null" || json.Unmarshal(req.ReviewDueAt, &t) != nil {
			return risk.PlacementInput{}, "review_due_at must be an RFC 3339 timestamp"
		}
		f.ReviewDueAt = &t
	}
	if req.Level == nil && !in.OrgUnitSet && req.Category == nil && f.Empty() {
		return risk.PlacementInput{}, "body must set at least one of: " + patchFieldList
	}
	return in, ""
}

// patchDecodeMessage turns a strict-decode error into the 400 text: an
// unknown field lists the fields PATCH accepts.
func patchDecodeMessage(err error) string {
	if msg := err.Error(); strings.HasPrefix(msg, "json: unknown field") {
		return strings.TrimPrefix(msg, "json: ") + "; PATCH accepts " + patchFieldList
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return typeErr.Field + " has the wrong type"
	}
	return "malformed body"
}

// actorFrom names the caller on the audit row: the credential's id. With
// no credential (or one without an id) in context it writes a 401 and
// returns false, so a write is never recorded without an actor (#1954).
func actorFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	if cred, ok := authctx.CredentialFromContext(r.Context()); ok && cred.ID != "" {
		return cred.ID, true
	}
	httpresp.WriteError(w, http.StatusUnauthorized, "credential missing")
	return "", false
}

// DeleteRisk handles DELETE /v1/risks/{id}. Hard delete is admin-only
// (#2107); everyone else closes a risk instead. The OPA policy refuses it
// to grc_engineer as well; this is the testable twin. The delete writes a
// deleted row to the risk's audit trail, which outlives the risk.
func (h *Handler) DeleteRisk(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "only an admin can delete a risk; close it instead")
		return
	}
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
	if err := h.store.Delete(ctx, id, credstore.PrincipalOf(cred)); err != nil {
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
		Category:            r.CategorySlug,
		CategoryName:        r.CategoryName,
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
	if r.CategoryID != uuid.Nil {
		w.CategoryID = r.CategoryID.String()
	}
	if r.OwnerUserID != nil {
		s := r.OwnerUserID.String()
		w.OwnerUserID = &s
	} else {
		w.OwnerUnlinked = strings.TrimSpace(r.TreatmentOwner)
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
	w.ChildCount = r.ChildCount
	if r.Treatment == dbx.RiskTreatmentAccept {
		w.Acceptance = acceptanceWireFrom(r.Acceptance)
	}
	w.Status = string(r.Status)
	if r.Status == dbx.RiskStatusClosed {
		w.Closure = closureWireFrom(r.Closure)
	}
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
