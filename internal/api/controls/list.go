// Slice 151 — GET /v1/controls list endpoint.
//
// Surfaces the active (non-superseded) controls for the bearer's tenant.
// Driven by the slice-151 risk-create form's control-link multi-select:
// the form needs a list of tenant `controls.id` UUIDs (FK target of
// `risk_control_links.control_id`), not the SCF anchor IDs that the
// pre-existing `/v1/anchors?include=state` route returns. The two
// surfaces are deliberately separate:
//
//   - `/v1/anchors` is the SCF catalog read model (slice 098 + 104).
//   - `/v1/controls` is the tenant control list (this slice).
//
// The BFF at `/api/controls` (slice 098) continues to proxy `/v1/anchors`
// for the existing `/controls` page; the slice-151 BFF at
// `/api/controls-list` proxies THIS endpoint for the risk-create form.
// That asymmetry is intentional and documented in `D-151-3` of the slice
// 151 PR body — preserving the existing controls-page contract while
// adding the missing tenant control list.
//
// Pagination: v1 returns all active controls in a single response. Active
// control counts per-tenant are bounded by the canvas §2.2 erDiagram (a
// 50-150-person security-product startup runs O(50-300) controls), so a
// flat list is sound for v1. Server-side pagination is a v2 concern when
// catalog scale calls for it.
//
// Empty-set robustness: returns `{"controls": [], "count": 0}` for a
// tenant with zero controls — never `null`, never a 500. Mirrors slice
// 150's empty-set convention.

package controls

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/catalog"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ListHandler exposes GET /v1/controls. Separate type from the slice-009
// upload Handler so the dependencies stay scoped to what each route
// actually needs.
type ListHandler struct {
	store *control.Store
}

// NewListHandler wires a ListHandler over the same control.Store the
// upload handler uses.
func NewListHandler(store *control.Store) *ListHandler {
	return &ListHandler{store: store}
}

// activeControlWire is the on-the-wire shape of a single row. Field
// selection matches the slice-151 risk-create multi-select's render
// needs: caller wants a stable id, a human-readable title, and enough
// metadata to disambiguate when titles collide (family + SCF code).
//
// `lifecycle_state` is included so future consumers can dim or filter
// non-`active` rows; the slice-151 form treats every returned row as a
// valid pick (the sqlc query already filters superseded rows).
type activeControlWire struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	ControlFamily  string `json:"control_family"`
	SCFID          string `json:"scf_id"`
	LifecycleState string `json:"lifecycle_state"`
	BundleID       string `json:"bundle_id"`
}

type listResp struct {
	Controls []activeControlWire `json:"controls"`
	Count    int                 `json:"count"`
}

// List handles GET /v1/controls — returns every active control for the
// bearer's tenant. Always 200 with an envelope; an empty tenant returns
// `{"controls": [], "count": 0}` (slice 150 convention).
func (h *ListHandler) List(w http.ResponseWriter, r *http.Request) {
	// Slice 033: the tenancy middleware (mounted after bearer-auth) has
	// already lifted the credential's tenant id into r.Context() via
	// tenancy.WithTenant. We confirm; a missing tenant means the route
	// was reached without a credential (misconfig) — 401 keeps the
	// shape consistent with the other bearer-auth'd handlers.
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	if includesState(r) {
		h.listWithState(w, r)
		return
	}

	rows, err := h.store.List(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list controls", err)
		return
	}

	out := make([]activeControlWire, len(rows))
	for i, c := range rows {
		out[i] = activeControlWire{
			ID:             c.ID.String(),
			Title:          c.Title,
			ControlFamily:  c.ControlFamily,
			SCFID:          c.SCFID,
			LifecycleState: c.LifecycleState,
			BundleID:       c.BundleID,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, listResp{Controls: out, Count: len(out)})
}

// ---- issue #1741: GET /v1/controls?include=state ----
//
// The /controls page's default view lists the tenant's own controls, not
// the SCF catalog. `?include=state` returns one page of active controls,
// each with its own evaluation rollup and its anchor's framework set,
// plus the unpaged `total`. Without `include=state` the response above is
// unchanged, so the slice-151 risk-form picker keeps its flat list.

const (
	stateListDefaultLimit int32 = 100
	stateListMaxLimit     int32 = 500
)

// controlStateWire mirrors the per-anchor rollup cell on
// `/v1/anchors?include=state` so the page renders both views with one
// cell type.
type controlStateWire struct {
	Result          string  `json:"result"`
	FreshnessStatus string  `json:"freshness_status"`
	LastObservedAt  *string `json:"last_observed_at"`
	EvaluatedAt     string  `json:"evaluated_at"`
}

type controlWithStateWire struct {
	activeControlWire
	SCFAnchorID *string           `json:"scf_anchor_id"`
	State       *controlStateWire `json:"state"`
	Frameworks  []string          `json:"frameworks"`
	// AnchorFit is exact|partial|none; AnchorFitNote explains a non-exact
	// fit and is null for an exact one (issue #1736).
	AnchorFit     string  `json:"anchor_fit"`
	AnchorFitNote *string `json:"anchor_fit_note"`
	// Demo is true when the in-place demo seed added this control to the
	// organisation (#2043), so the page can badge it. Omitted otherwise.
	Demo bool `json:"demo,omitempty"`
	// OwnerUserID is the person "assign owner" made this control's owner
	// (#2128), or null when nobody is assigned. The manifest owner_role is
	// the default owner role and is a separate field on the detail view.
	OwnerUserID *string `json:"owner_user_id"`
	// LegacyAnchor is true while the control has an undecided SCF remap
	// proposal (open or rejected): it is still on an older release's
	// anchor and a reviewer has not settled it (#2392).
	LegacyAnchor bool `json:"legacy_anchor"`
}

type listWithStateResp struct {
	Controls []controlWithStateWire `json:"controls"`
	Count    int                    `json:"count"`
	Total    int64                  `json:"total"`
	// Families is every family of the tenant's active controls, unfiltered
	// and unpaged, so the page's Family filter offers the whole set while it
	// holds one page of rows (#2131).
	Families []string `json:"families"`
}

// ownerUnowned is the `?owner=` value that keeps only controls with no
// assigned person (#2131).
const ownerUnowned = "none"

// stateResultFilters are the `?result=` values: the evidence_result enum.
var stateResultFilters = map[string]bool{
	"pass": true, "fail": true, "inconclusive": true, "excepted": true, "na": true,
}

// freshnessFilters are the `?freshness=` values: the freshness_status
// CHECK vocabulary the evaluator writes. There is no "expired" state;
// the filter offered one until #2359, and it is now a 400.
var freshnessFilters = map[string]bool{
	eval.FreshnessFresh: true, eval.FreshnessStale: true, eval.FreshnessNoEvidence: true,
}

// parseListFilter reads the `?include=state` filters and sort (#2131). Every
// value is checked against an allow-list or parsed as a UUID; the returned
// error is the 400 message.
func parseListFilter(r *http.Request) (control.ListFilter, string) {
	q := r.URL.Query()
	var f control.ListFilter
	if v := q.Get("scope"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, "scope must be a UUID"
		}
		f.ScopeCell = pgtype.UUID{Bytes: id, Valid: true}
	}
	f.AnchorFit = q.Get("anchor_fit")
	if !validAnchorFitFilter(f.AnchorFit) {
		return f, "anchor_fit must be one of exact|partial|none"
	}
	// #2128: ?owner=<user id> narrows the page and total to that person's
	// controls. RLS keeps another tenant's assignments invisible, so a
	// foreign user id matches nothing. #2131: ?owner=none keeps the
	// controls nobody owns.
	switch v := q.Get("owner"); v {
	case "":
	case ownerUnowned:
		f.Unowned = true
	default:
		id, err := uuid.Parse(v)
		if err != nil {
			return f, "owner must be a UUID or none"
		}
		f.Owner = pgtype.UUID{Bytes: id, Valid: true}
	}
	f.Result = q.Get("result")
	if f.Result != "" && !stateResultFilters[f.Result] {
		return f, "result must be one of pass|fail|inconclusive|excepted|na"
	}
	f.Freshness = q.Get("freshness")
	if f.Freshness != "" && !freshnessFilters[f.Freshness] {
		return f, "freshness must be one of fresh|stale|no_evidence"
	}
	f.Family = q.Get("family")
	// The framework filter matches the catalog framework slugs the list's
	// frameworks column shows; an unknown slug matches nothing.
	f.Framework = q.Get("framework")
	// #2392: ?legacy_anchor=true keeps the controls an undecided remap
	// proposal flags.
	switch q.Get("legacy_anchor") {
	case "", "false":
	case "true":
		f.LegacyAnchorOnly = true
	default:
		return f, "legacy_anchor must be true or false"
	}
	sortKey, desc, err := control.ParseListSort(q.Get("sort"))
	if err != nil {
		return f, err.Error()
	}
	f.Sort, f.SortDesc = sortKey, desc
	return f, ""
}

func (h *ListHandler) listWithState(w http.ResponseWriter, r *http.Request) {
	limit, offset := listPagination(r)
	filter, msg := parseListFilter(r)
	if msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	rows, total, err := h.store.ListWithState(r.Context(), limit, offset, filter)
	if err != nil {
		httperr.WriteInternal(w, r, "list controls with state", err)
		return
	}
	families, err := h.store.ListFamilies(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list controls with state", err)
		return
	}
	if families == nil {
		families = []string{}
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = uuid.UUID(row.ID.Bytes)
	}
	demo, err := h.store.DemoControls(r.Context(), ids)
	if err != nil {
		httperr.WriteInternal(w, r, "list controls with state", err)
		return
	}
	out := make([]controlWithStateWire, len(rows))
	for i, row := range rows {
		out[i] = controlWithStateWireFrom(row)
		out[i].Demo = demo.Has(ids[i])
	}
	httpresp.WriteJSON(w, http.StatusOK, listWithStateResp{Controls: out, Count: len(out), Total: total, Families: families})
}

func controlWithStateWireFrom(row dbx.ListActiveControlsWithStateRow) controlWithStateWire {
	scfCode := ""
	if row.ScfID != nil {
		scfCode = *row.ScfID
	}
	out := controlWithStateWire{
		activeControlWire: activeControlWire{
			ID:             uuid.UUID(row.ID.Bytes).String(),
			Title:          row.Title,
			ControlFamily:  row.ControlFamily,
			SCFID:          scfCode,
			LifecycleState: string(row.LifecycleState),
			BundleID:       row.BundleID,
		},
		Frameworks:    []string{},
		AnchorFit:     row.AnchorFit,
		AnchorFitNote: row.AnchorFitNote,
		LegacyAnchor:  row.LegacyAnchor,
	}
	if row.ScfAnchorID.Valid {
		id := uuid.UUID(row.ScfAnchorID.Bytes).String()
		out.SCFAnchorID = &id
	}
	if row.OwnerUserID.Valid {
		id := uuid.UUID(row.OwnerUserID.Bytes).String()
		out.OwnerUserID = &id
	}
	if len(row.FrameworkSlugs) > 0 {
		out.Frameworks = catalog.SortedFrameworkDisplayCodes(row.FrameworkSlugs)
	}
	if row.StateResult != nil {
		cell := &controlStateWire{
			Result:      string(*row.StateResult),
			EvaluatedAt: row.StateEvaluatedAt.Time.UTC().Format(time.RFC3339Nano),
		}
		if row.StateFreshnessStatus != nil {
			cell.FreshnessStatus = *row.StateFreshnessStatus
		}
		if row.StateLastObservedAt.Valid {
			ts := row.StateLastObservedAt.Time.UTC().Format(time.RFC3339Nano)
			cell.LastObservedAt = &ts
		}
		out.State = cell
	}
	return out
}

// validAnchorFitFilter reports whether `?anchor_fit=` is absent (no
// filter) or one of the three fit values.
func validAnchorFitFilter(v string) bool {
	switch v {
	case "", control.AnchorFitExact, control.AnchorFitPartial, control.AnchorFitNone:
		return true
	}
	return false
}

// includesState reports whether `?include=` names `state`; it accepts the
// CSV form (`?include=state,coverage`) like the anchors handler.
func includesState(r *http.Request) bool {
	for _, v := range r.URL.Query()["include"] {
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == "state" {
				return true
			}
		}
	}
	return false
}

// listPagination parses `?limit=` / `?offset=`; malformed values fall back
// to the defaults and limit is capped at stateListMaxLimit, matching the
// anchors handler.
func listPagination(r *http.Request) (int32, int32) {
	limit := stateListDefaultLimit
	offset := int32(0)
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 32); err == nil && parsed > 0 {
			limit = int32(min(parsed, int64(stateListMaxLimit)))
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 32); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}
	return limit, offset
}
