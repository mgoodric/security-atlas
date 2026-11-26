package risks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// auditEntryWire is one row of GET /v1/risks/{id}/audit-log (#2104).
// Action is placement_changed, category_changed or fields_changed, one of
// the #2105 treatment and acceptance transitions, or closed or reopened
// (#2107); a deleted row outlives its risk, so this route never shows it.
// Before
// and After hold the changed values (for fields_changed, only the fields
// that changed, keyed by their wire names).
type auditEntryWire struct {
	ID        string          `json:"id"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	CreatedAt time.Time       `json:"created_at"`
	// ParentRiskID is set only on a child_linked / child_unlinked row read
	// from a parent's trail (#2961): the listed risk was linked under, or
	// unlinked from, this parent. Before and After are the parent row's.
	ParentRiskID *string `json:"parent_risk_id,omitempty"`
}

// ListAuditLog handles GET /v1/risks/{id}/audit-log (#2104): the risk's
// change history, newest first. Tenant-scoped by RLS; a caller who can
// read the risk can read its history (the OPA `risks` resource). A risk
// with no changes, or an id with no rows, returns an empty list: the trail
// has no FK to risks and outlives a deleted risk.
//
// ?limit= caps the rows (#2311): default 200, at most 500; a value outside
// 1..500 is a 400 rather than silently clamped. The response's truncated
// is true when older rows exist past the ones returned, so a UI can say it
// shows only the latest N. There is no cursor yet: a reader that needs the
// whole trail of a risk with more than 500 changes has the export.
func (h *Handler) ListAuditLog(w http.ResponseWriter, r *http.Request) {
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
	limit, msg := parseAuditLimit(r.URL.Query().Get("limit"))
	if msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	entries, truncated, err := h.store.ListPlacementAudit(ctx, id, limit)
	if err != nil {
		httperr.WriteInternal(w, r, "list risk audit log", err)
		return
	}
	out := make([]auditEntryWire, len(entries))
	for i, e := range entries {
		out[i] = auditEntryWire{
			ID:        e.ID.String(),
			Actor:     e.Actor,
			Action:    e.ActionType,
			Before:    jsonRaw(e.BeforeState),
			After:     jsonRaw(e.AfterState),
			CreatedAt: e.CreatedAt,
		}
		if e.ParentRiskID != nil {
			p := e.ParentRiskID.String()
			out[i].ParentRiskID = &p
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"entries": out, "count": len(out), "limit": limit, "truncated": truncated,
	})
}

// Audit-log page bounds for GET /v1/risks/{id}/audit-log (#2311).
const (
	defaultAuditLimit = 200
	maxAuditLimit     = 500
)

// parseAuditLimit reads ?limit=. Empty is the default; a non-integer or a
// value outside 1..maxAuditLimit returns the 400 message.
func parseAuditLimit(v string) (int, string) {
	if v == "" {
		return defaultAuditLimit, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxAuditLimit {
		return 0, fmt.Sprintf("limit must be an integer from 1 to %d", maxAuditLimit)
	}
	return n, ""
}
