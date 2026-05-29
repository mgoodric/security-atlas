// Package themes serves slice 053's theme catalog + per-risk theme tagging.
//
// Routes (appended to the platform root router by httpserver.go):
//
//	GET    /v1/themes                       list visible vocab (10 defaults + tenant-private)
//	POST   /v1/risks/{id}/themes            assign one or more themes (replaces; idempotent)
//	DELETE /v1/risks/{id}/themes/{theme}    remove one theme; idempotent no-op when absent
//
// Tenant comes from `app.current_tenant` (slice-033 middleware). No app-
// level filters.
package themes

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type Handler struct {
	store *risk.Store
}

func New(store *risk.Store) *Handler { return &Handler{store: store} }

type themeWire struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "default" | "tenant"
}

type assignReq struct {
	Themes []string `json:"themes"`
}

// ListVisible handles GET /v1/themes.
func (h *Handler) ListVisible(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	rows, err := h.store.ListVisibleThemes(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list themes", err)
		return
	}
	out := make([]themeWire, len(rows))
	for i, t := range rows {
		out[i] = themeWire{Name: t.Name, Description: t.Description, Source: string(t.Source)}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"themes": out, "count": len(out)})
}

// AssignThemes handles POST /v1/risks/{id}/themes. Replaces the risk's
// themes with the supplied set (union with existing if you want additive
// semantics, but the slice ships replace-semantics: simpler, matches the
// AC-1 wording "accepts {themes:[string]} and persists them onto
// risks.themes"). Unknown themes -> 400. Cross-tenant risk -> 404.
func (h *Handler) AssignThemes(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	riskID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req assignReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Themes == nil {
		req.Themes = []string{}
	}
	// Slice 053's AssignThemes is a replace. To preserve existing themes
	// and add new ones, read first then merge.
	current, err := h.store.GetRiskThemes(r.Context(), riskID)
	if err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
			return
		}
		httperr.WriteInternal(w, r, "get risk themes", err)
		return
	}
	merged := mergeThemes(current, req.Themes)
	updated, err := h.store.AssignThemes(r.Context(), riskID, merged)
	if err != nil {
		switch {
		case errors.Is(err, risk.ErrUnknownTheme):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
		default:
			httperr.WriteInternal(w, r, "assign themes", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"risk": riskWireFrom(updated)})
}

// RemoveTheme handles DELETE /v1/risks/{id}/themes/{theme}. Idempotent:
// removing an absent theme returns 204 without error.
func (h *Handler) RemoveTheme(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	riskID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	theme := chi.URLParam(r, "theme")
	if theme == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "theme must be provided")
		return
	}
	current, err := h.store.GetRiskThemes(r.Context(), riskID)
	if err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
			return
		}
		httperr.WriteInternal(w, r, "get risk themes", err)
		return
	}
	remaining := removeTheme(current, theme)
	// Even if the theme wasn't present, the replace is a no-op write.
	// Calling AssignThemes still validates the remaining set against the
	// vocabulary — defaults to a no-op when remaining is unchanged.
	if _, err := h.store.AssignThemes(r.Context(), riskID, remaining); err != nil {
		switch {
		case errors.Is(err, risk.ErrUnknownTheme):
			// Should not happen — `remaining` is a subset of `current`,
			// which was already valid — but surface defensively.
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
		default:
			httperr.WriteInternal(w, r, "remove theme", err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- helpers -----

func mergeThemes(existing, additions []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	out := make([]string, 0, len(existing)+len(additions))
	for _, t := range existing {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	for _, t := range additions {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func removeTheme(in []string, theme string) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		if t == theme {
			continue
		}
		out = append(out, t)
	}
	return out
}

type riskWire struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Themes []string `json:"themes"`
	Level  string   `json:"level"`
}

func riskWireFrom(r risk.Risk) riskWire {
	return riskWire{
		ID:     r.ID.String(),
		Title:  r.Title,
		Themes: append([]string(nil), r.Themes...),
		Level:  string(r.Level),
	}
}
