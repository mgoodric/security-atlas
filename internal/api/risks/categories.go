package risks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// categoryWire is one of the tenant's risk categories on the wire (#1809).
// RiskCount and RuleCount are its usage (#1810): the risks that reference
// it, and the aggregation rules that have produced a meta-risk in it.
type categoryWire struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Archived    bool      `json:"archived"`
	IsDefault   bool      `json:"is_default"`
	RiskCount   int64     `json:"risk_count"`
	RuleCount   int64     `json:"rule_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func categoryWireFrom(c risk.Category) categoryWire {
	return categoryWire{
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

// ListCategories handles GET /v1/risks/categories (#1809, #1810): the
// tenant's own risk categories with usage counts, ordered by name. Archived
// categories are listed only with ?include_archived=true.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	includeArchived := false
	switch r.URL.Query().Get("include_archived") {
	case "", "false":
	case "true":
		includeArchived = true
	default:
		httpresp.WriteError(w, http.StatusBadRequest, "include_archived must be true or false")
		return
	}
	rows, err := h.store.ListCategoriesWithUsage(ctx, includeArchived)
	if err != nil {
		httperr.WriteInternal(w, r, "list risk categories", err)
		return
	}
	out := make([]categoryWire, len(rows))
	for i, c := range rows {
		out[i] = categoryWireFrom(c.Category)
		out[i].RiskCount = c.RiskCount
		out[i].RuleCount = c.RuleCount
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"categories": out, "count": len(out)})
}

type createCategoryReq struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateCategory handles POST /v1/risks/categories (#1810).
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if !requireTaxonomyAdmin(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createCategoryReq
	if !decodeStrict(w, r, &req) {
		return
	}
	created, err := h.store.CreateCategory(ctx, risk.NewCategory{
		Slug: req.Slug, Name: req.Name, Description: req.Description,
	}, actorFrom(r))
	if err != nil {
		writeCategoryErr(w, r, "create risk category", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"category": categoryWireFrom(created)})
}

// renameCategoryReq carries only name and description: decodeStrict
// refuses any other field, so a request that tries to change the slug is a
// 400 rather than a silent no-op.
type renameCategoryReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// RenameCategory handles PATCH /v1/risks/categories/{ref} (#1810). ref is
// the category's slug or id. The slug never changes.
func (h *Handler) RenameCategory(w http.ResponseWriter, r *http.Request) {
	if !requireTaxonomyAdmin(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req renameCategoryReq
	if !decodeStrict(w, r, &req) {
		return
	}
	if req.Name == nil && req.Description == nil {
		httpresp.WriteError(w, http.StatusBadRequest, "name or description is required")
		return
	}
	updated, err := h.store.RenameCategory(ctx, chi.URLParam(r, "ref"), risk.CategoryText{
		Name: req.Name, Description: req.Description,
	}, actorFrom(r))
	if err != nil {
		writeCategoryErr(w, r, "rename risk category", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"category": categoryWireFrom(updated)})
}

// ArchiveCategory handles POST /v1/risks/categories/{ref}/archive (#1810).
func (h *Handler) ArchiveCategory(w http.ResponseWriter, r *http.Request) {
	h.categoryTransition(w, r, "archive risk category", h.store.ArchiveCategory)
}

// UnarchiveCategory handles POST /v1/risks/categories/{ref}/unarchive (#1810).
func (h *Handler) UnarchiveCategory(w http.ResponseWriter, r *http.Request) {
	h.categoryTransition(w, r, "unarchive risk category", h.store.UnarchiveCategory)
}

// SetDefaultCategory handles POST /v1/risks/categories/{ref}/set-default
// (#1810).
func (h *Handler) SetDefaultCategory(w http.ResponseWriter, r *http.Request) {
	h.categoryTransition(w, r, "set default risk category", h.store.SetDefaultCategory)
}

func (h *Handler) categoryTransition(
	w http.ResponseWriter, r *http.Request, op string,
	apply func(ctx context.Context, ref, actor string) (risk.Category, error),
) {
	if !requireTaxonomyAdmin(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	updated, err := apply(ctx, chi.URLParam(r, "ref"), actorFrom(r))
	if err != nil {
		writeCategoryErr(w, r, op, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"category": categoryWireFrom(updated)})
}

// DeleteCategory handles DELETE /v1/risks/categories/{ref} (#1810). A
// category that risks or aggregation rules use is a 409 with the counts.
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	if !requireTaxonomyAdmin(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if err := h.store.DeleteCategory(ctx, chi.URLParam(r, "ref"), actorFrom(r)); err != nil {
		writeCategoryErr(w, r, "delete risk category", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeCategoryErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	var (
		inUse   *risk.CategoryInUseError
		invalid *risk.InvalidCategoryError
	)
	switch {
	case errors.As(err, &inUse):
		httpresp.WriteJSON(w, http.StatusConflict, map[string]any{
			"error":      err.Error(),
			"risk_count": inUse.Risks,
			"rule_count": inUse.Rules,
		})
	case errors.As(err, &invalid):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, risk.ErrCategoryNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "risk category not found")
	case errors.Is(err, risk.ErrCategoryConflict),
		errors.Is(err, risk.ErrCategoryIsDefault),
		errors.Is(err, risk.ErrCategoryArchived):
		httpresp.WriteError(w, http.StatusConflict, err.Error())
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}

// decodeStrict decodes a JSON body, refusing unknown fields. On failure it
// writes a 400 and returns false.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
