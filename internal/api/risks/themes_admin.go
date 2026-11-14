package risks

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// #1811: organisation theme management under /v1/themes. The list stays
// GET /v1/themes (internal/api/themes); these are the writes, under the
// same admin / grc_engineer guard as category management (#1810).

// orgThemeWire is one of the organisation's own themes on the wire.
type orgThemeWire struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Source      string    `json:"source"` // always "tenant": built-ins cannot be written
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func orgThemeWireFrom(t risk.OrgTheme) orgThemeWire {
	return orgThemeWire{
		ID:          t.ID.String(),
		Name:        t.Name,
		Description: t.Description,
		Source:      string(risk.ThemeSourceTenant),
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

type createThemeReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateTheme handles POST /v1/themes (#1811).
func (h *Handler) CreateTheme(w http.ResponseWriter, r *http.Request) {
	if !requireTaxonomyAdmin(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createThemeReq
	if !decodeStrict(w, r, &req) {
		return
	}
	created, err := h.store.CreateTheme(ctx, risk.NewTheme{Name: req.Name, Description: req.Description}, actorFrom(r))
	if err != nil {
		writeThemeErr(w, r, "create theme", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"theme": orgThemeWireFrom(created)})
}

type renameThemeReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// RenameTheme handles PATCH /v1/themes/{ref} (#1811). ref is the theme's
// name or id. A new name is rewritten onto every risk and aggregation rule
// that carried the old one; the response says how many.
func (h *Handler) RenameTheme(w http.ResponseWriter, r *http.Request) {
	if !requireTaxonomyAdmin(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req renameThemeReq
	if !decodeStrict(w, r, &req) {
		return
	}
	if req.Name == nil && req.Description == nil {
		httpresp.WriteError(w, http.StatusBadRequest, "name or description is required")
		return
	}
	res, err := h.store.RenameTheme(ctx, chi.URLParam(r, "ref"), risk.ThemeText{
		Name: req.Name, Description: req.Description,
	}, actorFrom(r))
	if err != nil {
		writeThemeErr(w, r, "rename theme", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"theme":         orgThemeWireFrom(res.OrgTheme),
		"risks_updated": res.RisksUpdated,
		"rules_updated": res.RulesUpdated,
	})
}

// DeleteTheme handles DELETE /v1/themes/{ref} (#1811). A theme that risks
// or aggregation rules use is a 409 with the counts.
func (h *Handler) DeleteTheme(w http.ResponseWriter, r *http.Request) {
	if !requireTaxonomyAdmin(w, r) {
		return
	}
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if err := h.store.DeleteTheme(ctx, chi.URLParam(r, "ref"), actorFrom(r)); err != nil {
		writeThemeErr(w, r, "delete theme", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeThemeErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	var (
		inUse   *risk.ThemeInUseError
		invalid *risk.InvalidThemeError
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
	case errors.Is(err, risk.ErrThemeNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "theme not found")
	case errors.Is(err, risk.ErrThemeConflict), errors.Is(err, risk.ErrThemeBuiltIn):
		httpresp.WriteError(w, http.StatusConflict, err.Error())
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}
