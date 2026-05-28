// Package adminauditlog is the HTTP surface for /v1/admin/audit-log (slice 062).
//
// One route:
//
//	GET /v1/admin/audit-log    -- paginated UNION read across the seven
//	                              per-domain audit-log tables (slice 035
//	                              decision_audit_log, slice 013
//	                              evidence_audit_log, slice 021
//	                              exception_audit_log, slice 059
//	                              feature_flag_audit_log, slice 036
//	                              artifact_access_log, slice 026
//	                              sample_audit_log, slice 028
//	                              audit_period_audit_log) via the
//	                              admin_audit_log_v view (migration _022).
//
// Admin-only -- the slice 035 OPA RBAC middleware also gates the path;
// this handler is defense-in-depth.
//
// Query parameters:
//
//	?actor=<user_id or credential_id>
//	?event_type=<exact event name>
//	?since=<RFC3339>
//	?until=<RFC3339>
//	?cursor=<opaque base64>
//	?limit=<int>            (default 50, max 200)
//
// Pagination uses an opaque composite cursor over (ts, source_table,
// resource_id) so duplicate ts values across the seven source tables
// don't cause page boundary instability. The cursor is base64-encoded
// JSON; clients should treat it as opaque.
//
// Constitutional invariants honored:
//
//   - Invariant 6 (RLS): the admin_audit_log_v view is NOT a SECURITY
//     DEFINER object and does NOT bypass RLS. Each branch's source-table
//     tenant_read policy fires under the caller's app.current_tenant GUC.
//     A non-tenant request returns zero rows, not a permission error.
//   - Anti-criterion P0: one UNION query, not N per-table queries.
//     Paginated by composite cursor on the view's uniform columns.
package adminauditlog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// Handler owns the audit-log route.
//
// Slice 145: the optional `limiter` field overrides the process-wide
// singleton [export.DefaultLimiter] when set — used by integration
// tests to pin a small, deterministic concurrency cap. Production
// callers leave it nil; the handler resolves the singleton lazily on
// every request.
type Handler struct {
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

// New constructs a Handler.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// --- response shapes ---

// Row is one entry in the audit-log response.
type Row struct {
	TS           time.Time       `json:"ts"`
	SourceTable  string          `json:"source_table"`
	EventType    string          `json:"event_type"`
	Actor        string          `json:"actor"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Summary      json.RawMessage `json:"summary"`
}

// ListResponse is the GET /v1/admin/audit-log shape.
type ListResponse struct {
	Rows       []Row  `json:"rows"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// cursorPayload is the JSON shape inside the opaque base64 cursor token.
type cursorPayload struct {
	TS         string `json:"ts"`
	Source     string `json:"src"`
	ResourceID string `json:"rid"`
}

// --- handler ---

// List handles GET /v1/admin/audit-log.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}

	q := r.URL.Query()
	limit := defaultListLimit
	if s := q.Get("limit"); s != "" {
		if n, perr := strconv.Atoi(s); perr == nil && n > 0 {
			if n > maxListLimit {
				n = maxListLimit
			}
			limit = n
		}
	}

	params := dbx.ListAdminAuditLogParams{
		TenantID:    pgtype.UUID{Bytes: tenantID, Valid: true},
		Limit:       int32(limit + 1),
		ActorFilter: q.Get("actor"),
		EventFilter: q.Get("event_type"),
	}
	if s := q.Get("since"); s != "" {
		t, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid since: "+perr.Error())
			return
		}
		params.Since = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if s := q.Get("until"); s != "" {
		t, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid until: "+perr.Error())
			return
		}
		params.Until = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if c := q.Get("cursor"); c != "" {
		payload, perr := decodeCursor(c)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor: "+perr.Error())
			return
		}
		t, terr := time.Parse(time.RFC3339Nano, payload.TS)
		if terr != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor ts: "+terr.Error())
			return
		}
		params.CursorTs = pgtype.Timestamptz{Time: t, Valid: true}
		params.CursorSource = payload.Source
		params.CursorResourceID = payload.ResourceID
	}

	var rows []dbx.AdminAuditLogV
	err = h.inTx(r.Context(), func(ctx context.Context, dbq *dbx.Queries) error {
		got, qErr := dbq.ListAdminAuditLog(ctx, params)
		rows = got
		return qErr
	})
	if err != nil {
		httperr.WriteInternal(w, r, "list audit log", err)
		return
	}

	out := make([]Row, 0, len(rows))
	var nextCursor string
	if len(rows) > limit {
		last := rows[limit-1]
		nextCursor = encodeCursor(cursorPayload{
			TS:         last.Ts.Time.Format(time.RFC3339Nano),
			Source:     last.SourceTable,
			ResourceID: last.ResourceID,
		})
		rows = rows[:limit]
	}
	for _, row := range rows {
		out = append(out, Row{
			TS:           row.Ts.Time,
			SourceTable:  row.SourceTable,
			EventType:    row.EventType,
			Actor:        row.Actor,
			ResourceType: row.ResourceType,
			ResourceID:   row.ResourceID,
			Summary:      json.RawMessage(row.Summary),
		})
	}
	writeJSON(w, http.StatusOK, ListResponse{Rows: out, NextCursor: nextCursor})
}

// --- helpers ---

func (h *Handler) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func encodeCursor(p cursorPayload) string {
	b, _ := json.Marshal(p)
	return base64.URLEncoding.EncodeToString(b)
}

func decodeCursor(c string) (cursorPayload, error) {
	raw, err := base64.URLEncoding.DecodeString(c)
	if err != nil {
		return cursorPayload{}, err
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return cursorPayload{}, err
	}
	return p, nil
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "admin credential required")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
