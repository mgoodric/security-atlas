package risks

import (
	"net/http"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// categoryWire is one of the tenant's risk categories on the wire (#1809).
type categoryWire struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Archived    bool      `json:"archived"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListCategories handles GET /v1/risks/categories (#1809): the tenant's
// own risk categories, archived included, ordered by name. Read-only; the
// management verbs arrive with a later step of #1745.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	rows, err := h.store.ListCategories(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "list risk categories", err)
		return
	}
	out := make([]categoryWire, len(rows))
	for i, c := range rows {
		out[i] = categoryWire{
			ID:          c.ID.String(),
			Slug:        c.Slug,
			Name:        c.Name,
			Description: c.Description,
			Archived:    c.Archived,
			IsDefault:   c.IsDefault,
			CreatedAt:   c.CreatedAt,
			UpdatedAt:   c.UpdatedAt,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"categories": out, "count": len(out)})
}

// GetReviewCadence handles GET /v1/risks/review-cadence (#2059): the
// organisation risk review cadence, so the new-risk form can show the
// review date a risk will get before it is created. Set via
// PATCH /v1/tenants/{id} risk_review_cadence_days.
func (h *Handler) GetReviewCadence(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	days, err := h.store.ReviewCadenceDays(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "read risk review cadence", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"risk_review_cadence_days": days})
}
