package controldetail

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// controlDetailReader is the per-route read seam the three
// /v1/controls/{id}/{policies,risks,history} paths read through (slice 411,
// contract-tier rollout). It carries JUST the three read methods those
// routes need — deliberately narrow (slice 409 D1 / slice 410's list-only
// precedent: a full read interface over the wider Store surface, which also
// serves the tenant-wide evidence ledger + count, would be a bigger refactor
// than recording three goldens justifies). The contract-tier recorder
// (handler_contract_test.go) injects a fixed-row stub satisfying this seam so
// the three wire shapes record on the plain `go test ./...` unit surface with
// no Postgres pool (ADR-0007 / P0-409-1). The production *Store satisfies it
// verbatim; the seam is unexported and New(*Store) is unchanged (P0-409-2).
// The Evidence handler keeps using the concrete h.store directly — it is the
// tenant-wide ledger window, not part of the control-detail tab cluster the
// e2e suite traverses, and is left for a follow-on (decisions log).
type controlDetailReader interface {
	PoliciesForControl(ctx context.Context, controlID uuid.UUID) ([]dbx.ListPoliciesLinkedToControlRow, error)
	RisksForControl(ctx context.Context, controlID uuid.UUID) ([]dbx.ListRisksLinkedToControlRow, error)
	HistoryForControl(ctx context.Context, controlID uuid.UUID, p historyPage) ([]dbx.ListControlEvaluationHistoryPagedRow, error)
}

// Handler bundles the slice-064 control-detail read routes over a single
// Store. Every route is a pure read; the Handler holds no write surface.
//
// reader is the slice-411 per-route read seam the policies/risks/history
// paths read through; New points it at store, so production behavior is
// identical. The Evidence handler keeps using store directly.
type Handler struct {
	store  *Store
	reader controlDetailReader
}

// New constructs a Handler over the application pgx pool. The slice-411
// per-route read seam (reader) is wired to the same store — the public
// signature is unchanged (P0-409-2).
func New(store *Store) *Handler { return &Handler{store: store, reader: store} }

// newHandlerWithReader constructs a Handler whose policies/risks/history
// paths read through an arbitrary read seam. It exists ONLY for the slice-411
// contract recorder, which injects a fixed-row stub so the three wire shapes
// record with no Postgres pool. Unexported — not part of the public surface.
func newHandlerWithReader(reader controlDetailReader) *Handler {
	return &Handler{reader: reader}
}

// ===== wire shapes =====
//
// The four row shapes are the slice-064 acceptance-criteria contracts. They
// are the spec the slice-041 control-detail view will bind its four
// placeholders to (the frontend re-pointing is the documented follow-up).

// evidenceWire is the AC-1 row shape: one evidence-ledger record.
// Slice 106 adds the `Result` field — the column has always existed on
// `evidence_records.result` but the slice-064 wire shape omitted it.
type evidenceWire struct {
	EvidenceID   string          `json:"evidence_id"`
	EvidenceKind *string         `json:"evidence_kind"`
	ObservedAt   string          `json:"observed_at"`
	Source       json.RawMessage `json:"source"`
	ContentHash  string          `json:"content_hash"`
	ScopeCell    *string         `json:"scope_cell"`
	Result       string          `json:"result"`
}

// policyWire is the AC-2 row shape: one linked policy.
type policyWire struct {
	PolicyID string `json:"policy_id"`
	Title    string `json:"title"`
	Version  string `json:"version"`
	Status   string `json:"status"`
}

// riskWire is the AC-3 row shape: one linked risk + its link weight.
type riskWire struct {
	RiskID        string          `json:"risk_id"`
	Title         string          `json:"title"`
	InherentScore json.RawMessage `json:"inherent_score"`
	ResidualScore json.RawMessage `json:"residual_score"`
	LinkWeight    *float64        `json:"link_weight"`
}

// historyWire is the AC-4 row shape: one control_evaluations ledger row.
type historyWire struct {
	EvaluatedAt     string  `json:"evaluated_at"`
	ScopeCell       *string `json:"scope_cell"`
	ComputedState   string  `json:"computed_state"`
	FreshnessStatus string  `json:"freshness_status"`
	EvidenceCount   int     `json:"evidence_count"`
}

// ===== AC-1: GET /v1/evidence?control_id=<id> =====

// Evidence handles GET /v1/evidence[?control_id=<id>] — paginated
// evidence-ledger records. Slice 106 made control_id OPTIONAL: when
// absent the handler dispatches to the tenant-wide ledger window.
//
// Query params:
//   - control_id        (optional, slice 106) the control UUID. 400 if
//     present but non-UUID. When absent the handler returns the tenant-
//     wide ledger window (RLS continues to scope the tenant).
//   - kind              (optional, slice 106) narrows to one evidence_kind.
//   - result            (optional, slice 106) narrows to one evidence_result
//     enum value (pass/fail/na/inconclusive). 400 on an invalid value.
//   - source_actor_type (optional, slice 106) JSONB predicate on
//     source_attribution->>'actor_type'.
//   - source_actor_id   (optional, slice 106) JSONB predicate on
//     source_attribution->>'actor_id'.
//   - scope_cell_id     (optional, slice 234) narrows to one scope cell.
//     400 if present but non-UUID. Ignored on the per-control path.
//   - since/until       (optional) RFC3339 observed_at window. Default is
//     the last 30 days.
//   - cursor            (optional) opaque keyset cursor.
//   - limit             (optional) page size, default 50, max 200.
//
// Per-control resolution reuses slice 012's control->evidence path
// (control_id = $ OR control_ref = $). Tenant-wide resolution is over a
// tenant_id-scoped predicate plus optional filters. No tenant_id is ever
// read from the client — the tenant comes solely from the slice-033
// middleware (anti-criterion P0-A4).
func (h *Handler) Evidence(w http.ResponseWriter, r *http.Request) {
	if !requireControlRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	q := r.URL.Query()

	// control_id is OPTIONAL post-slice-106. When present, it must parse.
	var (
		controlID    uuid.UUID
		hasControlID bool
	)
	if raw := q.Get("control_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "control_id must be a uuid")
			return
		}
		controlID = parsed
		hasControlID = true
	}

	// ?result= must be one of the evidence_result enum values when present.
	// Validated BEFORE the SQL round-trip so a typo is a clean 400.
	resultFilter := q.Get("result")
	if !isValidResult(resultFilter) {
		httpresp.WriteError(w, http.StatusBadRequest, "result must be one of: pass, fail, na, inconclusive")
		return
	}

	// Slice 234 — optional scope_cell_id filter. uuid.Nil is the no-filter
	// sentinel; a bad UUID returns 400 before the SQL round-trip. The
	// per-control path (?control_id=…) ignores this param: that branch
	// already resolves a single control's evidence and never benefits
	// from a scope-cell narrowing.
	var scopeCellID uuid.UUID
	if raw := q.Get("scope_cell_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "scope_cell_id must be a uuid")
			return
		}
		scopeCellID = parsed
	}

	now := time.Now().UTC()
	since, err := parseRFC3339(q.Get("since"), now.Add(-defaultWindow))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "since "+errBadTime.Error())
		return
	}
	until, err := parseRFC3339(q.Get("until"), now)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "until "+errBadTime.Error())
		return
	}
	cursor, err := decodeCursor(q.Get("cursor"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	pageRows, err := parseLimit(q.Get("limit"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Slice 236 — tenant-wide ledger total (ignores filter predicates and
	// the [since, until] window). Surfaced as `total` on both wire paths
	// so the frontend can render `Showing N of M records` and
	// disambiguate "ledger is empty tenant-wide" from "filters narrowed
	// to zero". Per P0-236-2 the count is NOT filtered; per P0-236-3 it
	// is not cached outside the request. The same RLS-bound pool the
	// list query rides keeps tenant isolation intact (canvas invariant
	// #6). The count is cheap — `evidence_records` is indexed on
	// `tenant_id` — so the parallel query has no observable cost vs the
	// list read alone.
	total, err := h.store.CountEvidenceForTenant(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}

	if !hasControlID {
		// Slice 106 tenant-wide path.
		rows, err := h.store.EvidencePaged(ctx, evidenceListPage{
			since:           since,
			until:           until,
			kind:            q.Get("kind"),
			result:          resultFilter,
			sourceActorType: q.Get("source_actor_type"),
			sourceActorID:   q.Get("source_actor_id"),
			scopeCellID:     scopeCellID,
			cursor:          cursor,
			pageRows:        pageRows,
		})
		if err != nil {
			httperr.WriteInternal(w, r, "controldetail", err)
			return
		}
		page, next := splitEvidenceListPage(rows, pageRows)
		out := make([]evidenceWire, len(page))
		for i, rec := range page {
			out[i] = evidenceWireFromListRow(rec)
		}
		httpresp.WriteJSON(w, http.StatusOK, map[string]any{
			"control_id":  "",
			"evidence":    out,
			"count":       len(out),
			"total":       total,
			"next_cursor": next,
		})

		return
	}

	// Per-control path (slice 064 behavior, preserved verbatim).
	rows, err := h.store.EvidenceForControl(ctx, controlID, evidencePage{
		since:    since,
		until:    until,
		cursor:   cursor,
		pageRows: pageRows,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}

	page, next := splitEvidencePage(rows, pageRows)
	out := make([]evidenceWire, len(page))
	for i, rec := range page {
		out[i] = evidenceWireFrom(rec)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"control_id":  controlID.String(),
		"evidence":    out,
		"count":       len(out),
		"total":       total,
		"next_cursor": next,
	})

}

// ===== AC-2: GET /v1/controls/{id}/policies =====

// Policies handles GET /v1/controls/{id}/policies — policies linked to the
// control via slice 022's policies.linked_control_ids array.
func (h *Handler) Policies(w http.ResponseWriter, r *http.Request) {
	ctx, controlID, ok := guardAndResolvePathControl(w, r)
	if !ok {
		return
	}

	rows, err := h.reader.PoliciesForControl(ctx, controlID)
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}
	out := make([]policyWire, len(rows))
	for i, p := range rows {
		out[i] = policyWire{
			PolicyID: uuidString(p.ID),
			Title:    p.Title,
			Version:  p.Version,
			Status:   p.Status,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"control_id": controlID.String(),
		"policies":   out,
		"count":      len(out),
	})

}

// ===== AC-3: GET /v1/controls/{id}/risks =====

// Risks handles GET /v1/controls/{id}/risks — risks linked to the control
// via slice 020's risk_control_links, each with the per-link design_score
// surfaced as link_weight.
func (h *Handler) Risks(w http.ResponseWriter, r *http.Request) {
	ctx, controlID, ok := guardAndResolvePathControl(w, r)
	if !ok {
		return
	}

	rows, err := h.reader.RisksForControl(ctx, controlID)
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}
	out := make([]riskWire, len(rows))
	for i, rk := range rows {
		out[i] = riskWire{
			RiskID:        uuidString(rk.ID),
			Title:         rk.Title,
			InherentScore: jsonOrNull(rk.InherentScore),
			ResidualScore: jsonOrNull(rk.ResidualScore),
			LinkWeight:    numericToFloat(rk.DesignScore),
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"control_id": controlID.String(),
		"risks":      out,
		"count":      len(out),
	})

}

// ===== AC-4: GET /v1/controls/{id}/history =====

// History handles GET /v1/controls/{id}/history — the control's evaluation
// history from slice 012's control_evaluations ledger, newest-first,
// keyset-paginated (?cursor= + ?limit=, same bounds as the evidence
// endpoint).
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	ctx, controlID, ok := guardAndResolvePathControl(w, r)
	if !ok {
		return
	}

	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	pageRows, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.reader.HistoryForControl(ctx, controlID, historyPage{
		cursor:   cursor,
		pageRows: pageRows,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}

	page, next := splitHistoryPage(rows, pageRows)
	out := make([]historyWire, len(page))
	for i, ev := range page {
		out[i] = historyWire{
			EvaluatedAt:     tsString(ev.EvaluatedAt),
			ScopeCell:       uuidPtr(ev.ScopeCellID),
			ComputedState:   string(ev.Result),
			FreshnessStatus: ev.FreshnessStatus,
			EvidenceCount:   int(ev.EvidenceCountInWindow),
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"control_id":  controlID.String(),
		"history":     out,
		"count":       len(out),
		"next_cursor": next,
	})

}

// ===== page-splitting =====

// splitEvidencePage trims the +1 probe row off an evidence result set and
// computes the next_cursor. When the store returned more than pageRows rows,
// a next page exists and next_cursor is the keyset of the last returned
// page row. Otherwise next_cursor is "" (no next page).
func splitEvidencePage(rows []dbx.ListEvidenceForControlPagedRow, pageRows int32) ([]dbx.ListEvidenceForControlPagedRow, string) {
	if int32(len(rows)) <= pageRows {
		return rows, ""
	}
	page := rows[:pageRows]
	last := page[len(page)-1]
	return page, encodeCursor(keyset{ts: last.ObservedAt.Time.UTC(), id: last.ID.Bytes})
}

// splitEvidenceListPage is splitEvidencePage's twin for the tenant-wide
// ledger result set (slice 106). Same +1 probe-row idiom.
func splitEvidenceListPage(rows []dbx.ListEvidencePagedRow, pageRows int32) ([]dbx.ListEvidencePagedRow, string) {
	if int32(len(rows)) <= pageRows {
		return rows, ""
	}
	page := rows[:pageRows]
	last := page[len(page)-1]
	return page, encodeCursor(keyset{ts: last.ObservedAt.Time.UTC(), id: last.ID.Bytes})
}

// splitHistoryPage is splitEvidencePage's twin for the history result set.
func splitHistoryPage(rows []dbx.ListControlEvaluationHistoryPagedRow, pageRows int32) ([]dbx.ListControlEvaluationHistoryPagedRow, string) {
	if int32(len(rows)) <= pageRows {
		return rows, ""
	}
	page := rows[:pageRows]
	last := page[len(page)-1]
	return page, encodeCursor(keyset{ts: last.EvaluatedAt.Time.UTC(), id: last.ID.Bytes})
}

// evidenceWireFrom maps a sqlc per-control evidence row to the AC-1 wire
// shape. The `source` field carries the provenance JSONB verbatim (canvas
// §2.3: connector id, source system id, query hash, runner id);
// `scope_cell` carries the nullable scope_id. Slice 106 surfaces
// `result` from the evidence_records.result column (always present in
// the DB; the slice-064 shape omitted it).
func evidenceWireFrom(rec dbx.ListEvidenceForControlPagedRow) evidenceWire {
	return evidenceWire{
		EvidenceID:   uuidString(rec.ID),
		EvidenceKind: rec.EvidenceKind,
		ObservedAt:   tsString(rec.ObservedAt),
		Source:       jsonOrNull(rec.Provenance),
		ContentHash:  rec.Hash,
		ScopeCell:    uuidPtr(rec.ScopeID),
		Result:       string(rec.Result),
	}
}

// evidenceWireFromListRow is evidenceWireFrom's twin for the tenant-wide
// query row type (slice 106). The two row types are structurally
// identical — sqlc emits them as separate types because they come from
// distinct named queries.
func evidenceWireFromListRow(rec dbx.ListEvidencePagedRow) evidenceWire {
	return evidenceWire{
		EvidenceID:   uuidString(rec.ID),
		EvidenceKind: rec.EvidenceKind,
		ObservedAt:   tsString(rec.ObservedAt),
		Source:       jsonOrNull(rec.Provenance),
		ContentHash:  rec.Hash,
		ScopeCell:    uuidPtr(rec.ScopeID),
		Result:       string(rec.Result),
	}
}

// ===== helpers =====

// tenantContext confirms the upstream slice-033 tenancy middleware lifted a
// tenant id onto the request context. Absent it, the request is
// unauthenticated.
func tenantContext(r *http.Request) (context.Context, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

// guardAndResolvePathControl is the shared preamble for the three
// /v1/controls/{id}/... handlers: it runs the control-read role guard
// (403 on denial), confirms the tenant context (401 on absence), and
// parses the {id} path param (400 on a non-uuid). On any failure it has
// already written the response and returns ok=false. The Evidence handler
// does not use this — its control id is a query param, not a path param.
func guardAndResolvePathControl(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, bool) {
	if !requireControlRead(w, r) {
		return nil, uuid.Nil, false
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return nil, uuid.Nil, false
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control id must be a uuid")
		return nil, uuid.Nil, false
	}
	return ctx, controlID, true
}

// pgUUID wraps a uuid.UUID as a pgtype.UUID for sqlc param structs.
func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// optUUID wraps a uuid.UUID as a pgtype.UUID, treating uuid.Nil as the
// no-filter sentinel (Valid=false). Used by the slice-234 scope_cell_id
// filter on the tenant-wide evidence ledger query — same shape as the
// slice-224 scope filter on the anchors rollup query.
func optUUID(u uuid.UUID) pgtype.UUID {
	if u == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

// pgTimestamptz wraps a time.Time as a pgtype.Timestamptz. A zero time
// yields an invalid (NULL) timestamptz.
func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// uuidString renders a pgtype.UUID; an invalid value renders "".
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// uuidPtr renders a nullable pgtype.UUID as a *string — nil when NULL.
func uuidPtr(u pgtype.UUID) *string {
	if !u.Valid {
		return nil
	}
	s := uuid.UUID(u.Bytes).String()
	return &s
}

// tsString renders a pgtype.Timestamptz as RFC3339Nano; an invalid value
// renders "".
func tsString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}

// jsonOrNull passes a JSONB column ([]byte) through as json.RawMessage. A
// nil or empty slice renders as JSON null so the wire shape is always valid
// JSON.
func jsonOrNull(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

// numericToFloat converts a pgtype.Numeric (the risk_control_links
// design_score, NUMERIC(4,3)) to a *float64 — nil when NULL. The score is a
// bounded [0,1] factor, so float64 is lossless for the three-decimal range.
func numericToFloat(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	v := f.Float64
	return &v
}
