package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// trendWindow is the AC-1 posture trend window: 90 days. The trend delta is
// coverage now minus coverage as it stood this far back.
const trendWindow = 90 * 24 * time.Hour

// reader is the dashboard handler's read seam (slice 409). The handler
// depends on this unexported interface, not the concrete *Store, so the
// contract-tier recorder (handler_contract_test.go) can drive the real
// wire-shape transformation with a fixed-row stub on the plain
// `go test ./...` unit surface — no Postgres pool, honoring ADR-0007's
// "recorders ride the unit surface" constraint. The production *Store
// satisfies it verbatim; this interface stays internal (P0-409-2 — the
// handler's public API is unchanged: New still takes a *Store).
type reader interface {
	FrameworkPosture(ctx context.Context, trendCutoff pgtype.Timestamptz) ([]dbx.FrameworkPostureRow, error)
	ActivityFeed(ctx context.Context, cursor keyset, pageRows int32) ([]dbx.ListEvidenceActivityRow, error)
	UpcomingItems(ctx context.Context, categoryFilter string, cursor keyset, pageRows int32) ([]dbx.ListUpcomingItemsRow, error)
}

// Handler bundles the slice-066 dashboard read routes over a single Store.
// Every route is a pure read; the Handler holds no write surface.
type Handler struct {
	store reader
}

// New constructs a Handler over the application pgx pool. The parameter
// type is the concrete *Store (public API unchanged, slice 409 P0-409-2);
// internally it is held behind the unexported reader seam.
func New(store *Store) *Handler { return &Handler{store: store} }

// newHandlerWithReader constructs a Handler over an arbitrary reader. It
// exists only for the slice-409 contract recorder, which injects a
// fixed-row stub so the wire shape records with no Postgres pool. It is
// unexported — not part of the package's public surface.
func newHandlerWithReader(r reader) *Handler { return &Handler{store: r} }

// ===== wire shapes =====
//
// The three row shapes are the slice-066 acceptance-criteria contracts and
// match slice 040's four placeholder contracts (the frontend re-pointing of
// framework-posture-panel / activity-feed-panel / upcoming-panel is the
// documented follow-up).

// postureWire is the AC-1 row shape: one framework version's posture.
type postureWire struct {
	FrameworkID        string  `json:"framework_id"`
	FrameworkVersion   string  `json:"framework_version"`
	CoveragePct        float64 `json:"coverage_pct"`
	FreshnessComposite float64 `json:"freshness_composite"`
	TrendDelta90d      float64 `json:"trend_delta_90d"`
}

// activityWire is the AC-2 row shape: one evidence-ingest activity event.
type activityWire struct {
	TS           string          `json:"ts"`
	EventType    string          `json:"event_type"`
	Actor        string          `json:"actor"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Summary      json.RawMessage `json:"summary"`
}

// upcomingWire is the AC-4 row shape: one upcoming item.
type upcomingWire struct {
	DueDate      string `json:"due_date"`
	Category     string `json:"category"`
	Title        string `json:"title"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

// ===== AC-1: GET /v1/frameworks/posture =====

// FrameworkPosture handles GET /v1/frameworks/posture — per-framework-
// version posture: coverage percentage, a freshness composite, and a
// 90-day trend delta. No tenant_id is read from the query or body — the
// tenant comes solely from the slice-033 middleware.
func (h *Handler) FrameworkPosture(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	cutoff := pgTimestamptz(time.Now().UTC().Add(-trendWindow))
	rows, err := h.store.FrameworkPosture(ctx, cutoff)
	if err != nil {
		httperr.WriteInternal(w, r, "dashboard", err)
		return
	}

	out := make([]postureWire, len(rows))
	for i, p := range rows {
		out[i] = postureWire{
			FrameworkID:        uuidString(p.FrameworkID),
			FrameworkVersion:   p.FrameworkVersion,
			CoveragePct:        p.CoveragePct,
			FreshnessComposite: p.FreshnessComposite,
			TrendDelta90d:      p.TrendDelta90d,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"frameworks": out,
		"count":      len(out),
	})

}

// ===== AC-2: GET /v1/activity =====

// Activity handles GET /v1/activity — a paginated read model over the
// evidence-ingest event archive (the slice-013/015 evidence_audit_log,
// surfaced through the slice-062 admin_audit_log_v view).
//
// Query params:
//   - cursor (optional) opaque keyset cursor. Omit for the first page.
//   - limit  (optional) page size, default 50, max 200.
//
// Rows are newest-first.
func (h *Handler) Activity(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), firstPageActivity())
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	pageRows, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.store.ActivityFeed(ctx, cursor, pageRows)
	if err != nil {
		httperr.WriteInternal(w, r, "dashboard", err)
		return
	}

	page, next := splitActivityPage(rows, pageRows)
	out := make([]activityWire, len(page))
	for i, ev := range page {
		out[i] = activityWire{
			TS:           tsString(ev.Ts),
			EventType:    ev.EventType,
			Actor:        ev.Actor,
			ResourceType: ev.ResourceType,
			ResourceID:   ev.ResourceID,
			Summary:      jsonOrNull(ev.Summary),
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"activity":    out,
		"count":       len(out),
		"next_cursor": next,
	})

}

// ===== AC-4: GET /v1/upcoming =====

// Upcoming handles GET /v1/upcoming — a unified rollup across expiring
// exceptions, policy-ack expirations, vendor reviews, and audit-period
// milestones, merged into one date-sorted (ascending) paginated feed.
//
// Query params:
//   - cursor   (optional) opaque keyset cursor. Omit for the first page.
//   - limit    (optional) page size, default 50, max 200.
//   - category (optional) one of exception / policy_ack / vendor_review /
//     audit_period — narrows to one source. Omit for all.
func (h *Handler) Upcoming(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	category := r.URL.Query().Get("category")
	if category != "" && !validUpcomingCategory(category) {
		httpresp.WriteError(w, http.StatusBadRequest,
			"category must be one of: exception, policy_ack, vendor_review, audit_period")

		return
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), firstPageUpcoming())
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	pageRows, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.store.UpcomingItems(ctx, category, cursor, pageRows)
	if err != nil {
		httperr.WriteInternal(w, r, "dashboard", err)
		return
	}

	page, next := splitUpcomingPage(rows, pageRows)
	out := make([]upcomingWire, len(page))
	for i, it := range page {
		out[i] = upcomingWire{
			DueDate:      tsString(it.DueDate),
			Category:     it.Category,
			Title:        anyToString(it.Title),
			ResourceType: it.ResourceType,
			ResourceID:   it.ResourceID,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"upcoming":    out,
		"count":       len(out),
		"next_cursor": next,
	})

}

// ===== page-splitting =====

// splitActivityPage trims the +1 probe row off an activity result set and
// computes the next_cursor. When the store returned more than pageRows
// rows, a next page exists and next_cursor is the keyset of the last
// returned page row. Otherwise next_cursor is "" (no next page).
func splitActivityPage(rows []dbx.ListEvidenceActivityRow, pageRows int32) ([]dbx.ListEvidenceActivityRow, string) {
	if int32(len(rows)) <= pageRows {
		return rows, ""
	}
	page := rows[:pageRows]
	last := page[len(page)-1]
	return page, encodeCursor(keyset{ts: last.Ts.Time.UTC(), id: last.ResourceID})
}

// splitUpcomingPage is splitActivityPage's twin for the upcoming rollup.
func splitUpcomingPage(rows []dbx.ListUpcomingItemsRow, pageRows int32) ([]dbx.ListUpcomingItemsRow, string) {
	if int32(len(rows)) <= pageRows {
		return rows, ""
	}
	page := rows[:pageRows]
	last := page[len(page)-1]
	return page, encodeCursor(keyset{ts: last.DueDate.Time.UTC(), id: last.ResourceID})
}

// ===== helpers =====

// validUpcomingCategory reports whether c is one of the four rollup
// category names. The empty string ("all") is handled before this is
// called.
func validUpcomingCategory(c string) bool {
	switch c {
	case "exception", "policy_ack", "vendor_review", "audit_period":
		return true
	default:
		return false
	}
}

// tenantContext confirms the upstream slice-033 tenancy middleware lifted a
// tenant id onto the request context. Absent it, the request is
// unauthenticated.
func tenantContext(r *http.Request) (context.Context, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

// pgTimestamptz wraps a time.Time as a pgtype.Timestamptz. A zero time
// yields an invalid (NULL) timestamptz.
func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// pgUUID wraps a uuid.UUID as a pgtype.UUID for sqlc param structs.
func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// uuidString renders a pgtype.UUID; an invalid value renders "".
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
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

// anyToString renders the upcoming rollup's `title` column. sqlc types it
// as interface{} because it is a `||` string concatenation it cannot
// statically resolve; at runtime pgx scans it as a string. A nil value
// (impossible for the rollup's non-null concatenations, but defensive)
// renders "".
func anyToString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}
