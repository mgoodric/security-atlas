package risks

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// auditEntryWire is one row of GET /v1/risks/{id}/audit-log (#2104).
// Action is placement_changed, category_changed or fields_changed; Before
// and After hold the changed values (for fields_changed, only the fields
// that changed, keyed by their wire names).
type auditEntryWire struct {
	ID        string          `json:"id"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	CreatedAt time.Time       `json:"created_at"`
}

// ListAuditLog handles GET /v1/risks/{id}/audit-log (#2104): the risk's
// change history, newest first. Tenant-scoped by RLS; a caller who can
// read the risk can read its history (the OPA `risks` resource). A risk
// with no changes, or an id with no rows, returns an empty list: the trail
// has no FK to risks and outlives a deleted risk. One risk's trail is a
// handful of rows per edit, so the route returns it whole.
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
	entries, err := h.store.ListPlacementAudit(ctx, id)
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
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"entries": out, "count": len(out)})
}
