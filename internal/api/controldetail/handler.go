package controldetail

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-064 control-detail read routes over a single
// Store. Every route is a pure read; the Handler holds no write surface.
type Handler struct {
	store *Store
}

// New constructs a Handler over the application pgx pool.
func New(store *Store) *Handler { return &Handler{store: store} }

// ===== wire shapes =====
//
// The four row shapes are the slice-064 acceptance-criteria contracts. They
// are the spec the slice-041 control-detail view will bind its four
// placeholders to (the frontend re-pointing is the documented follow-up).

// evidenceWire is the AC-1 row shape: one evidence-ledger record.
type evidenceWire struct {
	EvidenceID   string          `json:"evidence_id"`
	EvidenceKind *string         `json:"evidence_kind"`
	ObservedAt   string          `json:"observed_at"`
	Source       json.RawMessage `json:"source"`
	ContentHash  string          `json:"content_hash"`
	ScopeCell    *string         `json:"scope_cell"`
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

// Evidence handles GET /v1/evidence?control_id=<id> — paginated
// evidence-ledger records resolved for one control.
//
// Query params:
//   - control_id  (required) the control UUID. 400 if absent or non-UUID.
//   - since/until (optional) RFC3339 observed_at window. Default window is
//     the last 30 days (AC-1).
//   - cursor      (optional) opaque keyset cursor. Omit for the first page.
//   - limit       (optional) page size, default 50, max 200.
//
// Resolution reuses slice 012's control->evidence path: the underlying query
// matches (control_id = $ OR control_ref = $). No tenant_id is read from the
// query or body — the tenant comes solely from the slice-033 middleware.
func (h *Handler) Evidence(w http.ResponseWriter, r *http.Request) {
	if !requireControlRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	rawControlID := r.URL.Query().Get("control_id")
	if rawControlID == "" {
		writeError(w, http.StatusBadRequest, "control_id query parameter is required")
		return
	}
	controlID, err := uuid.Parse(rawControlID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "control_id must be a uuid")
		return
	}

	now := time.Now().UTC()
	since, err := parseRFC3339(r.URL.Query().Get("since"), now.Add(-defaultWindow))
	if err != nil {
		writeError(w, http.StatusBadRequest, "since "+errBadTime.Error())
		return
	}
	until, err := parseRFC3339(r.URL.Query().Get("until"), now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "until "+errBadTime.Error())
		return
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pageRows, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.store.EvidenceForControl(ctx, controlID, evidencePage{
		since:    since,
		until:    until,
		cursor:   cursor,
		pageRows: pageRows,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	page, next := splitEvidencePage(rows, pageRows)
	out := make([]evidenceWire, len(page))
	for i, rec := range page {
		out[i] = evidenceWireFrom(rec)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"control_id":  controlID.String(),
		"evidence":    out,
		"count":       len(out),
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

	rows, err := h.store.PoliciesForControl(ctx, controlID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, map[string]any{
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

	rows, err := h.store.RisksForControl(ctx, controlID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, map[string]any{
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pageRows, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.store.HistoryForControl(ctx, controlID, historyPage{
		cursor:   cursor,
		pageRows: pageRows,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, map[string]any{
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

// splitHistoryPage is splitEvidencePage's twin for the history result set.
func splitHistoryPage(rows []dbx.ListControlEvaluationHistoryPagedRow, pageRows int32) ([]dbx.ListControlEvaluationHistoryPagedRow, string) {
	if int32(len(rows)) <= pageRows {
		return rows, ""
	}
	page := rows[:pageRows]
	last := page[len(page)-1]
	return page, encodeCursor(keyset{ts: last.EvaluatedAt.Time.UTC(), id: last.ID.Bytes})
}

// evidenceWireFrom maps a sqlc evidence row to the AC-1 wire shape. The
// `source` field carries the provenance JSONB verbatim (canvas §2.3:
// connector id, source system id, query hash, runner id); `scope_cell`
// carries the nullable scope_id.
func evidenceWireFrom(rec dbx.ListEvidenceForControlPagedRow) evidenceWire {
	return evidenceWire{
		EvidenceID:   uuidString(rec.ID),
		EvidenceKind: rec.EvidenceKind,
		ObservedAt:   tsString(rec.ObservedAt),
		Source:       jsonOrNull(rec.Provenance),
		ContentHash:  rec.Hash,
		ScopeCell:    uuidPtr(rec.ScopeID),
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
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return nil, uuid.Nil, false
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "control id must be a uuid")
		return nil, uuid.Nil, false
	}
	return ctx, controlID, true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// pgUUID wraps a uuid.UUID as a pgtype.UUID for sqlc param structs.
func pgUUID(u uuid.UUID) pgtype.UUID {
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
