// Package ucfcoverage implements the UCF graph traversal HTTP API (slice
// 008). Three read-only endpoints query the requirement-anchor-control
// graph defined in canvas §3 + Plans/UCF_GRAPH_MODEL.md:
//
//   - GET /v1/requirements/{id}/coverage  — forward traversal: given a
//     framework requirement, list every SCF anchor it maps to plus the
//     tenant's controls anchored at each.
//
//   - GET /v1/anchors/{id}/requirements   — reverse traversal: given an
//     SCF anchor, list every framework requirement it satisfies. This
//     supersedes the slice-006 in-memory placeholder route on the same
//     path; the response shape is compatible.
//
//   - GET /v1/controls/{id}/coverage      — control-centric traversal:
//     given a control, return the framework requirements its SCF anchor
//     satisfies.
//
// Constitutional invariants honored:
//
//   - Invariant 1 (canvas §3.1, CLAUDE.md): every traversal goes through
//     the SCF anchor spine. No requirement → requirement edge is ever
//     consulted; no such table exists (slice 007 enforces at DDL).
//
//   - Invariant 6 (canvas §5.4, CLAUDE.md): tenant-scoped reads on the
//     `controls` table run inside the request's `app.current_tenant`
//     GUC set by tenancy.Middleware; no app-level `WHERE tenant_id = ?`
//     clause is present anywhere in this package. Cross-tenant queries
//     return empty controls lists, not 403 — RLS makes the foreign rows
//     invisible at the database layer.
//
// Effectiveness scores (canvas §3.3) are deferred to slice 012 — the
// `controls` array omits the field entirely rather than emitting null,
// so slice 012 can add it without a breaking change.
//
// The `?as-of=<timestamp>` and `?scf_release=<version>` query parameters
// are accepted and documented but no-op in v1; slice 012 (point-in-time
// evidence filtering) and a future SCF-release-import feature will
// activate them.
package ucfcoverage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler wires the three slice-008 routes to a pgx pool. Catalog reads
// go through a bare `*dbx.Queries`; tenant reads on `controls` go
// through `inTx` so the GUC is set before the query runs.
//
// Slice 256 — the optional `engine`, `scopeStore`, and `fwScopeStore`
// fields gate the per-row Coverage column on /v1/controls/{id}/coverage.
// When all three are wired, each requirement row carries a numeric
// `coverage` (strength × 30-day effectiveness when the requirement's
// framework_version is in scope; null otherwise — never client-
// computed, see slice 256 P0-1). When any is nil (unit servers built
// without these dependencies) the field is omitted entirely so the
// existing slice-008 wire shape stays backwards-compatible.
type Handler struct {
	pool         *pgxpool.Pool
	q            *dbx.Queries
	engine       *eval.Engine
	scopeStore   *scope.Store
	fwScopeStore *frameworkscope.Store
}

// New constructs a Handler from a pgx pool. pool must be non-nil.
//
// The returned Handler emits the slice-008 coverage response without the
// slice-256 per-row `coverage` field. Call AttachCoverage to wire the
// dependencies that promote that field to first-class.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, q: dbx.New(pool)}
}

// AttachCoverage wires the three stores the slice-256 per-row coverage
// computation needs: the eval engine (30-day pass rate), the scope
// store (control applicability), and the framework_scope store
// (per-framework activated predicate). All three must be non-nil; this
// is enforced at wire-time, not at request-time, so a partial wiring is
// caught by `cmd/atlas` startup rather than by a 500 on the first
// /coverage call.
//
// The two-stage constructor (New + AttachCoverage) preserves the
// existing zero-coverage-fields shape for unit tests that don't need
// eval/scope/framework_scope plumbing, and is the same pattern slice
// 013 used to graft an optional ingest pipeline onto the evidence
// handler without forcing every test to spin up NATS.
func (h *Handler) AttachCoverage(engine *eval.Engine, scopeStore *scope.Store, fwScopeStore *frameworkscope.Store) *Handler {
	h.engine = engine
	h.scopeStore = scopeStore
	h.fwScopeStore = fwScopeStore
	return h
}

// ===== route wiring =====

// RegisterRoutes attaches the three slice-008 read endpoints to the
// supplied chi.Router. Use the Mount-append pattern in
// internal/api/httpserver.go: call this method on the root router so
// the routes live alongside slice-014/017/etc. Never wrap with a second
// chi.NewRouter().Mount("/", ...) — chi panics on duplicate Mount.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/v1/requirements/{id}/coverage", h.RequirementCoverage)
	r.Get("/v1/anchors/{id}/requirements", h.AnchorRequirements)
	r.Get("/v1/controls/{id}/coverage", h.ControlCoverage)
}

// ===== /v1/requirements/{id}/coverage =====

// RequirementCoverage handles GET /v1/requirements/{id}/coverage.
//
// Path forms accepted (matches slice-007 convention):
//   - UUID — direct framework_requirements.id lookup
//   - `{slug}:{version}:{code}` — natural key, e.g. `soc2:2017:CC6.6`
//   - `{slug}::{code}` — convenience, resolves against the framework's
//     "current" version, e.g. `soc2::CC6.6`
//
// Query parameters:
//   - ?framework_version=slug:version — pin the SCF release to a
//     specific framework_version_id (AC-4). When the slug:version pair
//     doesn't resolve, the route still returns 200 with anchors+controls
//     from the unpinned view; slice 012 will tighten the contract.
//   - ?as-of=<RFC3339> — accepted no-op; slice 012 will wire evidence
//     filtering. Caller-friendly so dashboards can start sending the
//     param ahead of slice 012's eval engine.
//   - ?scf_release=<version> — accepted no-op until multiple SCF
//     releases are importable in the same DB.
//
// Returns:
//   - 200 { requirement, anchors[], controls[] } on success
//   - 404 when the requirement id doesn't resolve in any of the three
//     accepted forms
//   - 401 when bearer auth is missing (middleware-handled)
//
// Cross-tenant note: the `requirement` and `anchors` fields are global
// catalog data and visible to any authenticated tenant. The `controls`
// array is RLS-scoped — a tenant traversing a foreign control's
// requirement sees an empty controls list, which is the correct shape
// (canvas §3.5).
func (h *Handler) RequirementCoverage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, ok, err := h.lookupRequirement(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeServerErr(w, r, "lookup requirement", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "requirement not found")
		return
	}

	scfFV := h.resolveSCFRelease(ctx, r)
	anchors, err := h.listAnchorsForRequirement(ctx, req.ID, scfFV)
	if err != nil {
		writeServerErr(w, r, "list anchors for requirement", err)
		return
	}

	anchorIDs := make([]pgtype.UUID, len(anchors))
	for i, a := range anchors {
		anchorIDs[i] = a.scfAnchorID
	}
	controls, err := h.listControlsForAnchors(ctx, anchorIDs)
	if err != nil {
		writeServerErr(w, r, "list controls for anchors", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"requirement": requirementWire{
			ID:    uuidStr(req.ID),
			Code:  req.Code,
			Title: req.Title,
			Body:  req.Body,
		},
		"anchors":  anchorWiresFromAnchors(anchors),
		"controls": controlWiresFromRows(controls),
	})
}

// ===== /v1/anchors/{id}/requirements =====

// AnchorRequirements handles GET /v1/anchors/{id}/requirements (AC-2 /
// reverse traversal). Replaces the slice-006 in-memory placeholder on
// the same path with DB-backed traversal.
//
// Path forms accepted:
//   - UUID — direct scf_anchors.id lookup
//   - scf_id (e.g., "IAC-06") — natural key lookup
//
// Query parameters:
//   - ?framework_version=slug:version — pin the response to one
//     framework_version (e.g. `soc2:2017`). When the param is absent,
//     every framework version is included.
//
// Returns:
//   - 200 { anchor, requirements[] }
//   - 404 when the anchor id doesn't resolve
//   - 401 when bearer auth is missing
//
// Backwards-compat: the response key `requirements` matches the
// slice-006 in-memory shape, so the slice-007
// TestRequirementsForAnchor_StillReturnsMappings test still asserts a
// non-empty list against this route. Individual row fields are
// supersets of the in-memory shape.
func (h *Handler) AnchorRequirements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	anchor, ok, err := h.lookupAnchor(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeServerErr(w, r, "lookup anchor", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "anchor not found")
		return
	}

	var out []requirementForAnchorWire
	if fvParam := r.URL.Query().Get("framework_version"); fvParam != "" {
		fv, ok := h.resolveFrameworkVersion(ctx, fvParam)
		if !ok {
			// Pin resolves to nothing: empty list, not 404 — the anchor
			// exists; only the pin found no matches.
			writeJSON(w, http.StatusOK, map[string]any{
				"anchor":       anchorWireFromRow(anchor),
				"requirements": []requirementForAnchorWire{},
			})
			return
		}
		got, err := h.q.ListRequirementsForAnchorByFrameworkVersion(ctx, dbx.ListRequirementsForAnchorByFrameworkVersionParams{
			ScfAnchorID:        anchor.ID,
			FrameworkVersionID: fv.ID,
		})
		if err != nil {
			writeServerErr(w, r, "list requirements for anchor (pinned)", err)
			return
		}
		out = mapPinnedRequirements(got)
	} else {
		got, err := h.q.ListRequirementsForAnchor(ctx, anchor.ID)
		if err != nil {
			writeServerErr(w, r, "list requirements for anchor", err)
			return
		}
		out = mapRequirements(got)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"anchor":       anchorWireFromRow(anchor),
		"requirements": out,
	})
}

// ===== /v1/controls/{id}/coverage =====

// ControlCoverage handles GET /v1/controls/{id}/coverage (AC-3).
//
// Path: UUID-only — controls are tenant-scoped so the natural-key
// approach used for requirements doesn't apply (the same bundle_id can
// exist in multiple tenants).
//
// Query parameters:
//   - ?framework_version=slug:version — pin to one framework version
//
// Returns:
//   - 200 { control, anchor, requirements[] } where anchor is null when
//     the control has no scf_anchor_id (not 404 — the control still
//     exists; it just isn't anchored to the canonical graph yet)
//   - 404 when the control id doesn't resolve in the caller's tenant
//   - 400 when the path segment isn't a UUID
//   - 401 when bearer auth is missing
//
// Tenant isolation: the control lookup runs inside a tenant-tx so RLS
// filters foreign rows. Cross-tenant traversal returns 404 because the
// caller cannot see the foreign control row at all.
func (h *Handler) ControlCoverage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	cid, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "control id must be a UUID")
		return
	}

	var (
		controlOK bool
		ctrl      dbx.GetControlByIDRow
	)
	if err := h.inTenantTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		got, err := q.GetControlByID(ctx, dbx.GetControlByIDParams{
			TenantID: pgUUIDFromTenantCtx(ctx),
			ID:       pgtype.UUID{Bytes: cid, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		ctrl = got
		controlOK = true
		return nil
	}); err != nil {
		writeServerErr(w, r, "lookup control", err)
		return
	}
	if !controlOK {
		writeError(w, http.StatusNotFound, "control not found")
		return
	}

	out := map[string]any{
		"control": controlWireFromControlRow(ctrl),
	}
	if !ctrl.ScfAnchorID.Valid {
		// Control exists but isn't anchored. Return 200 with null
		// anchor + empty requirements. The dashboard surfaces this as
		// "not yet mapped to the canonical graph."
		out["anchor"] = nil
		out["requirements"] = []requirementForAnchorWire{}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Anchor metadata: pull from scf_anchors directly (catalog read).
	anchor, err := h.q.GetSCFAnchorByID(ctx, ctrl.ScfAnchorID)
	if err != nil {
		writeServerErr(w, r, "lookup control anchor", err)
		return
	}
	out["anchor"] = anchorWireFromRow(anchor)

	var reqs []requirementForAnchorWire
	fvParam := r.URL.Query().Get("framework_version")
	if fvParam != "" {
		fv, ok := h.resolveFrameworkVersion(ctx, fvParam)
		if !ok {
			out["requirements"] = []requirementForAnchorWire{}
			writeJSON(w, http.StatusOK, out)
			return
		}
		got, err := h.q.ListRequirementsForAnchorByFrameworkVersion(ctx, dbx.ListRequirementsForAnchorByFrameworkVersionParams{
			ScfAnchorID:        ctrl.ScfAnchorID,
			FrameworkVersionID: fv.ID,
		})
		if err != nil {
			writeServerErr(w, r, "list requirements for control (pinned)", err)
			return
		}
		reqs = mapPinnedRequirements(got)
	} else {
		got, err := h.q.ListRequirementsForAnchor(ctx, ctrl.ScfAnchorID)
		if err != nil {
			writeServerErr(w, r, "list requirements for control", err)
			return
		}
		reqs = mapRequirements(got)
	}

	// Slice 256 — per-row weighted Coverage column.
	//
	// Coverage[i] = strength[i] × 30d_pass_rate     when the requirement's
	//                                                framework_version is in
	//                                                scope for this control
	//             = null                            when out of scope OR
	//                                                no effectiveness data
	//
	// The "no effectiveness data" rule (TotalCount == 0) maps to JSON null
	// per AC-2: callers must distinguish "no data yet" from "perfectly
	// failing" (0). Effectiveness is computed once per request — it's a
	// per-control rollup, not per-row — and reused across every
	// requirement. Out-of-scope determination mirrors the slice-018
	// /effective-scope endpoint: control.applicability ∩
	// framework_scope.predicate; empty intersection => out of scope =>
	// coverage null. Wiring is opt-in (h.engine/h.scopeStore/h.fwScopeStore
	// all non-nil); unit servers that didn't wire them leave the field
	// omitted entirely, preserving the slice-008 shape.
	if h.engine != nil && h.scopeStore != nil && h.fwScopeStore != nil && len(reqs) > 0 {
		if err := h.applyCoverage(ctx, cid, reqs); err != nil {
			writeServerErr(w, r, "compute coverage", err)
			return
		}
	}

	out["requirements"] = reqs
	writeJSON(w, http.StatusOK, out)
}

// applyCoverage fills the `coverage` field on each requirement in `reqs`.
// Computes 30-day effectiveness once, then resolves in-scope/out-of-scope
// per distinct framework_version_id (one /effective-scope intersection
// per fv), then assigns coverage = strength × pass_rate when in scope and
// effectiveness has any data, else nil.
//
// Errors propagate to the caller; this is on the request-serving path
// so a transient DB error must surface as a 500 rather than a silently
// zeroed Coverage column. The function mutates `reqs` in place.
func (h *Handler) applyCoverage(ctx context.Context, controlID uuid.UUID, reqs []requirementForAnchorWire) error {
	// 30-day pass rate for the whole control (canvas §6.2
	// operational_score). Computed once per request — every row in the
	// table shares the same multiplier.
	eff, err := h.engine.Effectiveness(ctx, controlID)
	if err != nil {
		return fmt.Errorf("effectiveness: %w", err)
	}
	hasEffectivenessData := eff.TotalCount > 0
	passRate := eff.PassRate

	// Resolve in-scope per distinct framework_version_id. The
	// frameworkscope.Store.Activated lookup is cheap (single-row SELECT)
	// and the scope intersection is in-memory over the control's
	// applicability set — but doing it once per fv (not once per row)
	// keeps the table O(rows + fvs) rather than O(rows × fvs).
	applicability, err := h.scopeStore.ControlApplicability(ctx, controlID)
	if err != nil {
		return fmt.Errorf("control applicability: %w", err)
	}
	inScopeByFV := make(map[string]bool, 4)
	for _, req := range reqs {
		fvIDStr := req.FrameworkVersionID
		if _, seen := inScopeByFV[fvIDStr]; seen {
			continue
		}
		fvID, perr := uuid.Parse(fvIDStr)
		if perr != nil {
			// A malformed UUID on a catalog row would be a slice-007 bug,
			// not a request-shape bug. Treat as out-of-scope so the row
			// renders n/a rather than 500-ing the whole response.
			inScopeByFV[fvIDStr] = false
			continue
		}
		activated, aerr := h.fwScopeStore.Activated(ctx, fvID)
		if aerr != nil {
			if errors.Is(aerr, frameworkscope.ErrNotFound) {
				// No activated framework_scope → no audit-bound predicate
				// → effectively out of scope (canvas §5.5; matches the
				// slice-018 EffectiveScope handler's behavior).
				inScopeByFV[fvIDStr] = false
				continue
			}
			return fmt.Errorf("activated framework_scope: %w", aerr)
		}
		cells, ierr := frameworkscope.EffectiveScope(ctx, applicability, activated.Predicate)
		if ierr != nil {
			return fmt.Errorf("intersect: %w", ierr)
		}
		inScopeByFV[fvIDStr] = len(cells) > 0
	}

	for i := range reqs {
		inScope := inScopeByFV[reqs[i].FrameworkVersionID]
		if !inScope || !hasEffectivenessData {
			reqs[i].Coverage = nil
			continue
		}
		v := reqs[i].Strength * passRate
		reqs[i].Coverage = &v
	}
	return nil
}

// ===== internal helpers =====

// anchorEdge is the internal in-memory shape of "one SCF anchor with
// the STRM edge metadata from a specific requirement." Used to keep
// RequirementCoverage's two code paths (pinned vs unpinned) producing
// the same wire shape without duplicating the JSON struct.
type anchorEdge struct {
	edgeID            pgtype.UUID
	scfAnchorID       pgtype.UUID
	scfID             string
	family            string
	anchorTitle       string
	anchorDescription string
	relationshipType  string
	strength          float64
	sourceAttribution string
	rationale         string
}

// listAnchorsForRequirement runs either the unpinned or pinned variant
// based on whether an SCF framework_version filter was supplied.
func (h *Handler) listAnchorsForRequirement(ctx context.Context, reqID pgtype.UUID, scfFV *dbx.FrameworkVersion) ([]anchorEdge, error) {
	if scfFV == nil {
		rows, err := h.q.ListAnchorsForRequirementWithEdges(ctx, reqID)
		if err != nil {
			return nil, err
		}
		out := make([]anchorEdge, len(rows))
		for i, r := range rows {
			out[i] = anchorEdge{
				edgeID:            r.EdgeID,
				scfAnchorID:       r.ScfAnchorID,
				scfID:             r.ScfID,
				family:            r.Family,
				anchorTitle:       r.AnchorTitle,
				anchorDescription: r.AnchorDescription,
				relationshipType:  string(r.RelationshipType),
				strength:          r.Strength,
				sourceAttribution: string(r.SourceAttribution),
				rationale:         r.Rationale,
			}
		}
		return out, nil
	}
	rows, err := h.q.ListAnchorsForRequirementWithEdgesByFrameworkVersion(ctx, dbx.ListAnchorsForRequirementWithEdgesByFrameworkVersionParams{
		FrameworkRequirementID: reqID,
		FrameworkVersionID:     scfFV.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]anchorEdge, len(rows))
	for i, r := range rows {
		out[i] = anchorEdge{
			edgeID:            r.EdgeID,
			scfAnchorID:       r.ScfAnchorID,
			scfID:             r.ScfID,
			family:            r.Family,
			anchorTitle:       r.AnchorTitle,
			anchorDescription: r.AnchorDescription,
			relationshipType:  string(r.RelationshipType),
			strength:          r.Strength,
			sourceAttribution: string(r.SourceAttribution),
			rationale:         r.Rationale,
		}
	}
	return out, nil
}

// listControlsForAnchors runs the tenant-scoped controls lookup inside
// the request's `app.current_tenant` GUC (set by tenancy.Middleware via
// tenancy.WithTenant). RLS does the filtering — no `WHERE tenant_id`
// clause is present in the SQL.
func (h *Handler) listControlsForAnchors(ctx context.Context, anchorIDs []pgtype.UUID) ([]dbx.ListControlsForAnchorsRow, error) {
	if len(anchorIDs) == 0 {
		return nil, nil
	}
	var out []dbx.ListControlsForAnchorsRow
	if err := h.inTenantTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		got, err := q.ListControlsForAnchors(ctx, anchorIDs)
		if err != nil {
			return err
		}
		out = got
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// inTenantTx opens a tx on the pool, sets the `app.current_tenant` GUC
// from the request context's tenant binding (slice-033 middleware
// wired this), runs fn, and commits. The pattern mirrors
// internal/risk/store.go's inTx. Required for any read against a
// tenant-scoped table — RLS denies on unset GUC.
func (h *Handler) inTenantTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ucfcoverage: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ucfcoverage: commit tx: %w", err)
	}
	return nil
}

// pgUUIDFromTenantCtx converts the context-bound tenant id string into
// a pgtype.UUID for sqlc-generated query params that still take a
// tenant_id parameter. Slice 008's traversal queries don't, but the
// reused slice-009 `GetControlByID` does.
func pgUUIDFromTenantCtx(ctx context.Context) pgtype.UUID {
	t, _ := tenancy.TenantFromContext(ctx)
	u, _ := uuid.Parse(t)
	return pgtype.UUID{Bytes: u, Valid: true}
}

// lookupRequirement resolves the {id} path segment to a
// framework_requirement row, supporting UUID, slug:version:code, and
// slug::code forms. Identical pattern to slice-007's
// anchors.lookupRequirement; duplicated here per the 2-call duplication
// allowance — a third user would justify hoisting to a shared package.
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

// lookupAnchor resolves the {id} path segment to a scf_anchors row,
// supporting UUID and bare scf_id forms.
func (h *Handler) lookupAnchor(ctx context.Context, idOrSCFID string) (dbx.ScfAnchor, bool, error) {
	if uid, err := uuid.Parse(idOrSCFID); err == nil {
		row, err := h.q.GetSCFAnchorByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.ScfAnchor{}, false, nil
		}
		if err != nil {
			return dbx.ScfAnchor{}, false, err
		}
		return row, true, nil
	}
	row, err := h.q.GetSCFAnchorBySCFID(ctx, idOrSCFID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbx.ScfAnchor{}, false, nil
	}
	if err != nil {
		return dbx.ScfAnchor{}, false, err
	}
	return row, true, nil
}

// resolveFrameworkVersion parses ?framework_version=slug:version. Returns
// (row, true) on success; (zero, false) if the param shape is invalid
// or the slug:version pair doesn't exist in the catalog. Handlers
// interpret false as "no rows match" (200 + empty list), not 404 —
// the underlying anchor/requirement still resolved, just not the pin.
func (h *Handler) resolveFrameworkVersion(ctx context.Context, param string) (dbx.FrameworkVersion, bool) {
	parts := strings.SplitN(param, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return dbx.FrameworkVersion{}, false
	}
	row, err := h.q.GetFrameworkVersionBySlugAndVersion(ctx, dbx.GetFrameworkVersionBySlugAndVersionParams{
		Slug:    parts[0],
		Version: parts[1],
	})
	if err != nil {
		return dbx.FrameworkVersion{}, false
	}
	return row, true
}

// resolveSCFRelease parses ?scf_release=<version> into a framework_version
// row for the SCF framework. Returns nil on absence (no filter) or
// when the version doesn't resolve. Distinct from
// resolveFrameworkVersion because the slug is fixed to "scf".
func (h *Handler) resolveSCFRelease(ctx context.Context, r *http.Request) *dbx.FrameworkVersion {
	v := r.URL.Query().Get("scf_release")
	if v == "" {
		return nil
	}
	row, err := h.q.GetFrameworkVersionBySlugAndVersion(ctx, dbx.GetFrameworkVersionBySlugAndVersionParams{
		Slug:    "scf",
		Version: v,
	})
	if err != nil {
		return nil
	}
	return &row
}

// ===== wire types =====

// requirementWire is the shape of a framework_requirement in slice-008
// responses. Matches slice-007's requirementWire shape — when both
// slices ship the same wire type, callers don't need per-route
// deserializers.
type requirementWire struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// anchorWire is the shape of an SCF anchor in slice-008 responses.
// Includes the STRM edge metadata when present (in RequirementCoverage)
// and is omitted when not relevant (in ControlCoverage's `anchor` field
// which is the bare anchor, no edge).
type anchorWire struct {
	ID                string  `json:"id"`
	SCFID             string  `json:"scf_id"`
	Family            string  `json:"family"`
	Name              string  `json:"name"`
	Description       string  `json:"description,omitempty"`
	EdgeID            string  `json:"edge_id,omitempty"`
	RelationshipType  string  `json:"relationship_type,omitempty"`
	Strength          float64 `json:"strength,omitempty"`
	SourceAttribution string  `json:"source_attribution,omitempty"`
	Rationale         string  `json:"rationale,omitempty"`
}

// controlWire is the shape of a control in slice-008 responses.
// Effectiveness is intentionally absent (slice 012's territory) — the
// field is omitted entirely rather than emitted as null so slice 012
// can add it without a breaking-change semver bump.
type controlWire struct {
	ID                 string `json:"id"`
	BundleID           string `json:"bundle_id"`
	Version            int32  `json:"version"`
	SCFID              string `json:"scf_id,omitempty"`
	SCFAnchorID        string `json:"scf_anchor_id,omitempty"`
	Title              string `json:"title"`
	ControlFamily      string `json:"control_family"`
	ImplementationType string `json:"implementation_type"`
	OwnerRole          string `json:"owner_role"`
	LifecycleState     string `json:"lifecycle_state"`
	FreshnessClass     string `json:"freshness_class,omitempty"`
}

// requirementForAnchorWire is the shape of one row in
// AnchorRequirements + ControlCoverage. Carries enough framework
// metadata that callers don't need a second round-trip per row.
//
// Slice 256 — `Coverage` is the per-row weighted score
// (strength × 30-day effectiveness, intersected with the framework's
// scope predicate). `*float64` so we can JSON-encode `null` when the
// requirement's framework_version is out of scope OR the control has
// no effectiveness data yet (TotalCount == 0). Distinguishing null from
// 0 is the AC-2 contract — "no data" must NOT degrade to "perfectly
// failing". The field is always emitted (no `omitempty`) so the wire
// shape is a stable contract: callers always see `coverage: <number>`
// or `coverage: null`, never an absent key. On
// /v1/anchors/{id}/requirements (which does not compute coverage) the
// field emits as `null` — the honest "not computed for this surface"
// shape rather than a silent omission.
type requirementForAnchorWire struct {
	EdgeID                 string   `json:"edge_id"`
	RequirementID          string   `json:"requirement_id"`
	Code                   string   `json:"code"`
	Title                  string   `json:"title"`
	Body                   string   `json:"body,omitempty"`
	FrameworkSlug          string   `json:"framework_slug"`
	FrameworkName          string   `json:"framework_name"`
	FrameworkVersion       string   `json:"framework_version"`
	FrameworkVersionID     string   `json:"framework_version_id"`
	FrameworkVersionStatus string   `json:"framework_version_status"`
	RelationshipType       string   `json:"relationship_type"`
	Strength               float64  `json:"strength"`
	Coverage               *float64 `json:"coverage"`
	SourceAttribution      string   `json:"source_attribution"`
	Rationale              string   `json:"rationale,omitempty"`
}

func anchorWireFromRow(a dbx.ScfAnchor) anchorWire {
	return anchorWire{
		ID:          uuidStr(a.ID),
		SCFID:       a.ScfID,
		Family:      a.Family,
		Name:        a.Title,
		Description: a.Description,
	}
}

func anchorWiresFromAnchors(anchors []anchorEdge) []anchorWire {
	out := make([]anchorWire, 0, len(anchors))
	for _, a := range anchors {
		out = append(out, anchorWire{
			ID:                uuidStr(a.scfAnchorID),
			SCFID:             a.scfID,
			Family:            a.family,
			Name:              a.anchorTitle,
			Description:       a.anchorDescription,
			EdgeID:            uuidStr(a.edgeID),
			RelationshipType:  a.relationshipType,
			Strength:          a.strength,
			SourceAttribution: a.sourceAttribution,
			Rationale:         a.rationale,
		})
	}
	return out
}

func controlWiresFromRows(rows []dbx.ListControlsForAnchorsRow) []controlWire {
	out := make([]controlWire, 0, len(rows))
	for _, c := range rows {
		w := controlWire{
			ID:                 uuidStr(c.ID),
			BundleID:           c.BundleID,
			Version:            c.Version,
			SCFAnchorID:        uuidStr(c.ScfAnchorID),
			Title:              c.Title,
			ControlFamily:      c.ControlFamily,
			ImplementationType: string(c.ImplementationType),
			OwnerRole:          c.OwnerRole,
			LifecycleState:     string(c.LifecycleState),
		}
		if c.ScfID != nil {
			w.SCFID = *c.ScfID
		}
		if c.FreshnessClass != nil {
			w.FreshnessClass = *c.FreshnessClass
		}
		out = append(out, w)
	}
	return out
}

func controlWireFromControlRow(c dbx.GetControlByIDRow) controlWire {
	w := controlWire{
		ID:                 uuidStr(c.ID),
		BundleID:           c.BundleID,
		Version:            c.Version,
		SCFAnchorID:        uuidStr(c.ScfAnchorID),
		Title:              c.Title,
		ControlFamily:      c.ControlFamily,
		ImplementationType: string(c.ImplementationType),
		OwnerRole:          c.OwnerRole,
		LifecycleState:     string(c.LifecycleState),
	}
	if c.ScfID != nil {
		w.SCFID = *c.ScfID
	}
	if c.FreshnessClass != nil {
		w.FreshnessClass = *c.FreshnessClass
	}
	return w
}

func mapRequirements(rows []dbx.ListRequirementsForAnchorRow) []requirementForAnchorWire {
	out := make([]requirementForAnchorWire, 0, len(rows))
	for _, x := range rows {
		out = append(out, requirementForAnchorWire{
			EdgeID:                 uuidStr(x.EdgeID),
			RequirementID:          uuidStr(x.FrameworkRequirementID),
			Code:                   x.Code,
			Title:                  x.RequirementTitle,
			Body:                   x.RequirementBody,
			FrameworkSlug:          x.FrameworkSlug,
			FrameworkName:          x.FrameworkName,
			FrameworkVersion:       x.FrameworkVersion,
			FrameworkVersionID:     uuidStr(x.FrameworkVersionID),
			FrameworkVersionStatus: string(x.FrameworkVersionStatus),
			RelationshipType:       string(x.RelationshipType),
			Strength:               x.Strength,
			SourceAttribution:      string(x.SourceAttribution),
			Rationale:              x.Rationale,
		})
	}
	return out
}

func mapPinnedRequirements(rows []dbx.ListRequirementsForAnchorByFrameworkVersionRow) []requirementForAnchorWire {
	out := make([]requirementForAnchorWire, 0, len(rows))
	for _, x := range rows {
		out = append(out, requirementForAnchorWire{
			EdgeID:                 uuidStr(x.EdgeID),
			RequirementID:          uuidStr(x.FrameworkRequirementID),
			Code:                   x.Code,
			Title:                  x.RequirementTitle,
			Body:                   x.RequirementBody,
			FrameworkSlug:          x.FrameworkSlug,
			FrameworkName:          x.FrameworkName,
			FrameworkVersion:       x.FrameworkVersion,
			FrameworkVersionID:     uuidStr(x.FrameworkVersionID),
			FrameworkVersionStatus: string(x.FrameworkVersionStatus),
			RelationshipType:       string(x.RelationshipType),
			Strength:               x.Strength,
			SourceAttribution:      string(x.SourceAttribution),
			Rationale:              x.Rationale,
		})
	}
	return out
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	httperr.WriteInternal(w, r, op, err)
}
