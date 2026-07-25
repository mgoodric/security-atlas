// Package anchors serves the read-only HTTP API for SCF anchors and the
// frameworks/versions catalog. Slice 006 landed the DB-backed anchor
// list/detail; slice 007 added the requirement → anchors reverse-traversal
// route; slice 008 moved the anchor → requirements DB-backed handler to
// internal/api/ucfcoverage and retired the slice-006 in-memory
// `anchorseed` placeholder route from this package.
package anchors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/catalog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler exposes the /v1/anchors + /v1/frameworks routes. Auth is enforced
// by middleware mounted at the router root.
//
// Slice 104: when `?include=state` is set, the listAnchors handler reads
// from `control_evaluations` (RLS-protected). That path requires a
// transaction with `app.current_tenant` GUC set via
// `tenancy.ApplyTenant`. The handler holds a *pgxpool.Pool for that
// case; for the existing non-state paths, the pre-bound `q` over the
// pool keeps the slice-006 read shape unchanged.
type Handler struct {
	q            *dbx.Queries
	pool         *pgxpool.Pool
	defaultLimit int32
	maxLimit     int32
}

// New constructs a Handler. q must be a non-nil sqlc Queries.
//
// Backwards-compatible: callers that do not need the slice-104 joined
// state path can still pass only `q` (pool=nil); the `?include=state`
// query parameter will then respond 500 with a clear "pool not wired"
// error. Production wiring (cmd/atlas + internal/api/httpserver.go)
// passes the pool via NewWithPool.
func New(q *dbx.Queries) *Handler {
	return &Handler{q: q, defaultLimit: 100, maxLimit: 500}
}

// NewWithPool is the slice-104 constructor. The pool is used ONLY for
// the `?include=state` path's tenant-GUC-bearing transaction; every
// other read continues through `q`.
func NewWithPool(q *dbx.Queries, pool *pgxpool.Pool) *Handler {
	return &Handler{q: q, pool: pool, defaultLimit: 100, maxLimit: 500}
}

// Routes returns a chi router with the slice-005 + slice-006 + slice-007 endpoints.
//
// Slice 008 supersedes the slice-006 in-memory `/v1/anchors/{id}/requirements`
// route with a DB-backed handler under internal/api/ucfcoverage. That route
// is no longer registered here. The slice-006 `requirementsForAnchor` method
// and the `anchorseed` mapping field stay in place for now (dead-code on the
// hot path; a future cleanup slice removes them) so the Handler signature
// doesn't churn across slices.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/v1/anchors", h.listAnchors)
	r.Get("/v1/anchors/{id}", h.getAnchor)
	r.Get("/v1/frameworks", h.listFrameworks)
	r.Get("/v1/frameworks/scf/versions", h.listSCFVersions)
	// Slice 007: reverse traversal — given a framework_requirements row
	// (by UUID or by `{slug}:{version}:{code}` form), list every SCF
	// anchor it maps to with relationship_type + strength + source
	// attribution + rationale. Slice 008 ships the richer `/coverage`
	// variant alongside this lightweight one (canvas §7.2).
	r.Get("/v1/requirements/{id}/anchors", h.anchorsForRequirement)
	return r
}

// listAnchors returns the SCF anchor catalog. Paginated via ?limit= and
// ?offset=; ?framework_version_id= optionally narrows the list.
//
// Slice 104 — `?include=state` (additive): when set, the response shape
// becomes `{ anchors: [{ ...anchorWire, state: stateWire | null }] }`.
// The state column is computed by a single CTE+join SQL query that picks
// the latest control_evaluations row per (control, scope_cell) and
// aggregates worst-state-wins per anchor (see decisions D1/D2/D4 in
// docs/audit-log/104-anchors-include-state-decisions.md). There is NO
// per-anchor loop calling the eval engine (slice 104 P0 anti-criterion).
//
// Unknown `include` values are silently ignored (slice 094 precedent —
// additive query params are not errors).
func (h *Handler) listAnchors(w http.ResponseWriter, r *http.Request) {
	limit, offset := h.pagination(r)
	ctx := r.Context()
	withState := includesState(r)

	// Slice 224: optional `?scope=<cell_id>` filter. When set, the
	// with-state queries narrow the worst_per_anchor rollup to
	// evaluations recorded against the given scope cell only. A bad
	// UUID returns 400; an unset value is the no-filter sentinel
	// (pgtype.UUID{Valid:false}). Out-of-tenant cell ids return zero
	// rows naturally via the existing tenant RLS on
	// control_evaluations — no 404 leak.
	scopeCell, ok := h.scopeCell(w, r)
	if !ok {
		return
	}

	if fvID := r.URL.Query().Get("framework_version_id"); fvID != "" {
		uid, err := uuid.Parse(fvID)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "framework_version_id must be a UUID")
			return
		}
		if withState {
			var rows []dbx.ListSCFAnchorsForVersionWithStateRow
			err := h.inTenantTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
				var inner error
				rows, inner = q.ListSCFAnchorsForVersionWithState(ctx, dbx.ListSCFAnchorsForVersionWithStateParams{
					FrameworkVersionID: pgtype.UUID{Bytes: uid, Valid: true},
					Limit:              limit,
					Offset:             offset,
					ScopeCellID:        scopeCell,
				})
				return inner
			})
			if err != nil {
				httperr.WriteInternal(w, r, "list anchors for version (with state)", err)
				return
			}
			httpresp.WriteJSON(w, http.StatusOK, map[string]any{"anchors": forVersionRowsToStateWire(rows)})
			return
		}
		rows, err := h.q.ListSCFAnchorsForVersion(ctx, dbx.ListSCFAnchorsForVersionParams{
			FrameworkVersionID: pgtype.UUID{Bytes: uid, Valid: true},
			Limit:              limit,
			Offset:             offset,
		})
		if err != nil {
			httperr.WriteInternal(w, r, "list anchors for version", err)
			return
		}
		httpresp.WriteJSON(w, http.StatusOK, map[string]any{"anchors": rowsToWire(rows)})
		return
	}

	if withState {
		var rows []dbx.ListSCFAnchorsLatestWithStateRow
		err := h.inTenantTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
			var inner error
			rows, inner = q.ListSCFAnchorsLatestWithState(ctx, dbx.ListSCFAnchorsLatestWithStateParams{
				Limit:       limit,
				Offset:      offset,
				ScopeCellID: scopeCell,
			})
			return inner
		})
		if err != nil {
			httperr.WriteInternal(w, r, "list anchors latest (with state)", err)
			return
		}
		httpresp.WriteJSON(w, http.StatusOK, map[string]any{"anchors": latestRowsToStateWire(rows)})
		return
	}

	rows, err := h.q.ListSCFAnchorsLatest(ctx, dbx.ListSCFAnchorsLatestParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "list anchors latest", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"anchors": rowsToWire(rows)})
}

// inTenantTx opens a tx on the pool, sets the `app.current_tenant` GUC
// from the request context's tenant binding (slice-033 middleware
// wired this), runs fn, and commits. Mirrors the ucfcoverage pattern.
// Required for the slice-104 `?include=state` join — control_evaluations
// + controls are tenant-scoped under FORCE ROW LEVEL SECURITY, so RLS
// denies on unset GUC.
func (h *Handler) inTenantTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	if h.pool == nil {
		return fmt.Errorf("anchors: pool not wired; ?include=state requires NewWithPool")
	}
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("anchors: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("anchors: commit tx: %w", err)
	}
	return nil
}

// scopeCell parses the optional `?scope=<uuid>` query parameter. Returns
// the parsed UUID wrapped in a pgtype.UUID (Valid=true) when set, or an
// invalid pgtype.UUID (Valid=false) when the query param is absent —
// the SQL queries treat the invalid sentinel as "no filter". On a
// malformed UUID it writes a 400 and returns ok=false so the caller
// short-circuits the handler. Slice 224.
func (h *Handler) scopeCell(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	v := r.URL.Query().Get("scope")
	if v == "" {
		return pgtype.UUID{Valid: false}, true
	}
	uid, err := uuid.Parse(v)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "scope must be a UUID")
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: uid, Valid: true}, true
}

// includesState returns true when the request asked for the joined state
// column via `?include=state`. The query string accepts a CSV list per
// the OpenAPI `?include=` convention (e.g. `?include=state,coverage`),
// so the helper splits and matches token-by-token. Unknown tokens are
// ignored — they are NOT errors (slice 094 calendar precedent: additive
// query params don't break the caller).
func includesState(r *http.Request) bool {
	for _, v := range r.URL.Query()["include"] {
		for _, tok := range strings.Split(v, ",") {
			if strings.TrimSpace(tok) == "state" {
				return true
			}
		}
	}
	return false
}

// getAnchor returns one anchor. `:id` may be a UUID or an scf_id
// (e.g., "IAC-06"); scf_id resolves against the current SCF version.
func (h *Handler) getAnchor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	anchor, ok, err := h.lookupAnchor(r.Context(), id)
	if err != nil {
		httperr.WriteInternal(w, r, "lookup anchor", err)
		return
	}
	if !ok {
		httpresp.WriteError(w, http.StatusNotFound, "anchor not found")
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"anchor": anchor})
}

// listFrameworks returns the framework catalog (global only).
func (h *Handler) listFrameworks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListFrameworks(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list frameworks", err)
		return
	}
	out := make([]frameworkWire, 0, len(rows))
	for _, f := range rows {
		out = append(out, frameworkWire{
			ID:              uuidStr(f.ID),
			Name:            f.Name,
			Slug:            f.Slug,
			Issuer:          f.Issuer,
			LatestVersionID: uuidStr(f.LatestVersionID),
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"frameworks": out})
}

// listSCFVersions returns every SCF framework_version for the slice's
// audit-replay use case (old versions stay queryable).
func (h *Handler) listSCFVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListFrameworkVersionsBySlug(r.Context(), "scf")
	if err != nil {
		httperr.WriteInternal(w, r, "list scf versions", err)
		return
	}
	out := make([]frameworkVersionWire, 0, len(rows))
	for _, v := range rows {
		out = append(out, frameworkVersionWire{
			ID:            uuidStr(v.ID),
			Version:       v.Version,
			Status:        string(v.Status),
			EffectiveFrom: dateStr(v.EffectiveFrom),
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"versions": out})
}

// anchorsForRequirement returns one framework_requirements row plus every
// fw_to_scf_edges row that originates from it, joined to the scf_anchors
// table for the anchor metadata. The path segment accepts:
//
//   - a UUID — direct framework_requirements.id lookup
//   - `{framework_slug}:{version}:{code}` — natural-key form, e.g.,
//     `soc2:2017:CC6.6`
//   - `{framework_slug}::{code}` — convenience form that resolves
//     {code} against the framework's "current" version, e.g.,
//     `soc2::CC6.6`
//
// Returns 404 when no requirement matches.
func (h *Handler) anchorsForRequirement(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, ok, err := h.lookupRequirement(r.Context(), id)
	if err != nil {
		httperr.WriteInternal(w, r, "lookup requirement", err)
		return
	}
	if !ok {
		httpresp.WriteError(w, http.StatusNotFound, "requirement not found")
		return
	}

	rows, err := h.q.ListFwToScfEdgesForRequirement(r.Context(), req.ID)
	if err != nil {
		httperr.WriteInternal(w, r, "list edges for requirement", err)
		return
	}
	out := make([]requirementEdgeWire, 0, len(rows))
	for _, row := range rows {
		out = append(out, requirementEdgeWire{
			EdgeID:            uuidStr(row.ID),
			SCFAnchorID:       uuidStr(row.ScfAnchorID),
			SCFID:             row.ScfID,
			Family:            row.Family,
			AnchorTitle:       row.AnchorTitle,
			RelationshipType:  string(row.RelationshipType),
			Strength:          row.Strength,
			SourceAttribution: string(row.SourceAttribution),
			MappingTier:       string(row.MappingTier),
			Rationale:         row.Rationale,
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"requirement": requirementWire{
			ID:    uuidStr(req.ID),
			Code:  req.Code,
			Title: req.Title,
			Body:  req.Body,
		},
		"anchors": out,
	})

}

// lookupRequirement resolves the {id} path segment to a framework_requirement
// row, supporting all three forms documented on anchorsForRequirement.
func (h *Handler) lookupRequirement(ctx context.Context, idOrCode string) (dbx.FrameworkRequirement, bool, error) {
	if uid, err := uuid.Parse(idOrCode); err == nil {
		row, err := h.q.GetFrameworkRequirementByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.FrameworkRequirement{}, false, nil
		}
		if err != nil {
			return dbx.FrameworkRequirement{}, false, err
		}
		return row, true, nil
	}

	// Natural-key form: slug:version:code (version may be empty —
	// `soc2::CC6.6` resolves against the framework's current version).
	parts := strings.SplitN(idOrCode, ":", 3)
	if len(parts) != 3 {
		return dbx.FrameworkRequirement{}, false, nil
	}
	slug, version, code := parts[0], parts[1], parts[2]
	if version == "" {
		row, err := h.q.GetFrameworkRequirementByCurrentVersion(ctx, dbx.GetFrameworkRequirementByCurrentVersionParams{
			Slug: slug,
			Code: code,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.FrameworkRequirement{}, false, nil
		}
		return row, err == nil, err
	}
	row, err := h.q.GetFrameworkRequirementByFrameworkSlugVersionCode(ctx, dbx.GetFrameworkRequirementByFrameworkSlugVersionCodeParams{
		Slug:    slug,
		Version: version,
		Code:    code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbx.FrameworkRequirement{}, false, nil
	}
	return row, err == nil, err
}

func (h *Handler) lookupAnchor(ctx context.Context, idOrSCFID string) (anchorWire, bool, error) {
	if uid, err := uuid.Parse(idOrSCFID); err == nil {
		row, err := h.q.GetSCFAnchorByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return anchorWire{}, false, nil
		}
		if err != nil {
			return anchorWire{}, false, err
		}
		return anchorWireFromRow(row), true, nil
	}
	row, err := h.q.GetSCFAnchorBySCFID(ctx, idOrSCFID)
	if errors.Is(err, pgx.ErrNoRows) {
		return anchorWire{}, false, nil
	}
	if err != nil {
		return anchorWire{}, false, err
	}
	return anchorWireFromRow(row), true, nil
}

func (h *Handler) pagination(r *http.Request) (int32, int32) {
	limit := h.defaultLimit
	offset := int32(0)
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = int32(parsed)
			if limit > h.maxLimit {
				limit = h.maxLimit
			}
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}
	return limit, offset
}

// ---- wire types ----

type anchorWire struct {
	ID          string `json:"id"`
	SCFID       string `json:"scf_id"`
	Family      string `json:"family"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type frameworkWire struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Issuer          string `json:"issuer"`
	LatestVersionID string `json:"latest_version_id,omitempty"`
}

type frameworkVersionWire struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	Status        string `json:"status"`
	EffectiveFrom string `json:"effective_from,omitempty"`
}

// requirementWire is the public shape of a framework_requirement.
type requirementWire struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// requirementEdgeWire is one row of "anchors for a requirement" — the
// STRM edge metadata plus the anchor's identifying fields. Joined view
// so callers don't need a second round trip per anchor.
type requirementEdgeWire struct {
	EdgeID            string  `json:"edge_id"`
	SCFAnchorID       string  `json:"scf_anchor_id"`
	SCFID             string  `json:"scf_id"`
	Family            string  `json:"family"`
	AnchorTitle       string  `json:"anchor_title"`
	RelationshipType  string  `json:"relationship_type"`
	Strength          float64 `json:"strength"`
	SourceAttribution string  `json:"source_attribution"`
	// MappingTier is the slice-483 trust tier (draft | under_review |
	// verified | rejected) — additive, orthogonal to source_attribution.
	// Reviewer identity is NOT exposed here (P0-483-6).
	MappingTier string `json:"mapping_tier"`
	Rationale   string `json:"rationale,omitempty"`
}

func rowsToWire[R anchorRow](rows []R) []anchorWire {
	out := make([]anchorWire, len(rows))
	for i, r := range rows {
		out[i] = anchorWireFromRow(r)
	}
	return out
}

// ---- slice 104: anchors with optional joined state ----

// anchorStateCellWire is the per-anchor state rollup the `?include=state`
// extension attaches to each anchor. The field names mirror the slice-012
// `stateWire` (internal/api/controlstate/handlers.go) so the frontend can
// reuse the existing `ControlStateEntry` shape minus the engine-internal
// `scope_cell_id` / `evaluated_at` / `freshness_class` / `trigger` fields
// — those are per-cell evaluation provenance, not part of the rollup.
type anchorStateCellWire struct {
	Result          string  `json:"result"`
	FreshnessStatus string  `json:"freshness_status"`
	LastObservedAt  *string `json:"last_observed_at"`
	EvaluatedAt     string  `json:"evaluated_at"`
}

// anchorWithStateWire is the JSON shape returned by ?include=state.
// `State` is nil when no tenant control satisfies the anchor.
//
// Slice 226: the `frameworks` field carries the set of framework
// DISPLAY ABBREVIATIONS (e.g. `["SOC2","ISO","CSF"]`) the anchor
// satisfies via fw_to_scf_edges. The wire ships display codes (not
// slugs) so the frontend renders verbatim — the abbreviation authority
// is `internal/catalog.FrameworkDisplayCode` and the frontend never
// knows about the slug→display map (P0-226-2). The field is omitted on
// the bare anchor list (no `?include=state`); it MAY be an empty array
// when an anchor has no satisfaction edges yet (e.g. SCF anchor in a
// family no framework has crosswalked).
type anchorWithStateWire struct {
	anchorWire
	State      *anchorStateCellWire `json:"state"`
	Frameworks []string             `json:"frameworks"`
}

// stateRowMeta is the subset of fields the wire-encoding step needs from
// the sqlc-generated row type. Declaring it as an interface keeps the
// state-conversion logic unit-testable without standing up sqlc fixtures.
type stateRowMeta interface {
	StateValid() bool
	StateResult() string
	StateFreshness() string
	StateLastObservedAt() (time.Time, bool)
	StateEvaluatedAt() time.Time
}

// anchorStateWireFrom converts a state row's nullable columns into the
// JSON-ready cell. Returns nil when the LEFT JOIN produced no state row
// (anchor has no tenant control); otherwise returns a fully-populated
// cell. Pure function — unit-tested without DB fixtures.
func anchorStateWireFrom(meta stateRowMeta) *anchorStateCellWire {
	if !meta.StateValid() {
		return nil
	}
	cell := &anchorStateCellWire{
		Result:          meta.StateResult(),
		FreshnessStatus: meta.StateFreshness(),
		EvaluatedAt:     meta.StateEvaluatedAt().UTC().Format(time.RFC3339Nano),
	}
	if t, ok := meta.StateLastObservedAt(); ok {
		s := t.UTC().Format(time.RFC3339Nano)
		cell.LastObservedAt = &s
	}
	return cell
}

// latestStateRow / forVersionStateRow adapt the two sqlc row types to the
// stateRowMeta interface. The two row types share an identical column
// shape (the SQL CTEs differ only in the WHERE clause) so the adapters
// are byte-identical except for the underlying type — keeping them
// separate preserves type-safety at the call site.

type latestStateRow struct {
	r dbx.ListSCFAnchorsLatestWithStateRow
}

// Slice 159: row adapters switched from `NullEvidenceResult.Valid` /
// `pgtype.Text.String` API to pointer-style nil-check + dereference.
// sqlc v1.31.1 emits `*EvidenceResult` / `*string` for the LEFT-JOIN
// nullable CTE columns under `emit_pointers_for_null_types: true`.
// The wire shape (`state: null` vs populated cell) is unchanged.
func (l latestStateRow) StateValid() bool { return l.r.StateResult != nil }
func (l latestStateRow) StateResult() string {
	if l.r.StateResult == nil {
		return ""
	}
	return string(*l.r.StateResult)
}
func (l latestStateRow) StateFreshness() string {
	if l.r.StateFreshnessStatus == nil {
		return ""
	}
	return *l.r.StateFreshnessStatus
}
func (l latestStateRow) StateEvaluatedAt() time.Time {
	return l.r.StateEvaluatedAt.Time
}
func (l latestStateRow) StateLastObservedAt() (time.Time, bool) {
	if !l.r.StateLastObservedAt.Valid {
		return time.Time{}, false
	}
	return l.r.StateLastObservedAt.Time, true
}

type forVersionStateRow struct {
	r dbx.ListSCFAnchorsForVersionWithStateRow
}

func (f forVersionStateRow) StateValid() bool { return f.r.StateResult != nil }
func (f forVersionStateRow) StateResult() string {
	if f.r.StateResult == nil {
		return ""
	}
	return string(*f.r.StateResult)
}
func (f forVersionStateRow) StateFreshness() string {
	if f.r.StateFreshnessStatus == nil {
		return ""
	}
	return *f.r.StateFreshnessStatus
}
func (f forVersionStateRow) StateEvaluatedAt() time.Time {
	return f.r.StateEvaluatedAt.Time
}
func (f forVersionStateRow) StateLastObservedAt() (time.Time, bool) {
	if !f.r.StateLastObservedAt.Valid {
		return time.Time{}, false
	}
	return f.r.StateLastObservedAt.Time, true
}

func latestRowsToStateWire(rows []dbx.ListSCFAnchorsLatestWithStateRow) []anchorWithStateWire {
	out := make([]anchorWithStateWire, len(rows))
	for i, r := range rows {
		out[i] = anchorWithStateWire{
			anchorWire: anchorWire{
				ID:          uuidStr(r.ID),
				SCFID:       r.ScfID,
				Family:      r.Family,
				Name:        r.Title,
				Description: r.Description,
			},
			State:      anchorStateWireFrom(latestStateRow{r: r}),
			Frameworks: frameworksWire(r.FrameworkSlugs),
		}
	}
	return out
}

func forVersionRowsToStateWire(rows []dbx.ListSCFAnchorsForVersionWithStateRow) []anchorWithStateWire {
	out := make([]anchorWithStateWire, len(rows))
	for i, r := range rows {
		out[i] = anchorWithStateWire{
			anchorWire: anchorWire{
				ID:          uuidStr(r.ID),
				SCFID:       r.ScfID,
				Family:      r.Family,
				Name:        r.Title,
				Description: r.Description,
			},
			State:      anchorStateWireFrom(forVersionStateRow{r: r}),
			Frameworks: frameworksWire(r.FrameworkSlugs),
		}
	}
	return out
}

// frameworksWire converts the DB-returned slug slice to the per-row
// display-abbreviation slice the wire ships. Returns a non-nil empty
// slice (not nil) when the input is nil/empty so the JSON wire renders
// `[]` rather than `null` — the frontend treats both as "no
// frameworks", but the consistent `[]` shape simplifies type narrowing
// (the field is always an array on the wire).
func frameworksWire(slugs []string) []string {
	if len(slugs) == 0 {
		return []string{}
	}
	return catalog.SortedFrameworkDisplayCodes(slugs)
}

type anchorRow interface {
	dbx.ScfAnchor
}

func anchorWireFromRow[R anchorRow](r R) anchorWire {
	a := dbx.ScfAnchor(r)
	return anchorWire{
		ID:          uuidStr(a.ID),
		SCFID:       a.ScfID,
		Family:      a.Family,
		Name:        a.Title,
		Description: a.Description,
	}
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func dateStr(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}

// ---- helpers ----
