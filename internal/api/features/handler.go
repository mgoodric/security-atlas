// Package features is the HTTP surface for /v1/admin/features.
//
// Two routes:
//
//	GET   /v1/admin/features          -- list every flag for the tenant
//	                                     (override merged with seed defaults)
//	PATCH /v1/admin/features/{key}    -- toggle a flag (enabled bool)
//
// Both require an admin credential (slice 014 cred.IsAdmin). The slice
// 035 OPA RBAC middleware also covers the path under resource.type
// "admin" -> admin.rego allows; this handler's requireAdmin check is
// defense-in-depth so the gate stays correct even without OPA wired.
//
// Anti-criterion P0: non-admin callers get 403 on BOTH list and toggle.
// The flag state itself could expose attack-surface signal (which
// capabilities are wired in this tenant), so reads are gated too.
package features

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// Handler bundles the HTTP routes.
type Handler struct {
	store *featureflag.Store
}

// New constructs a Handler.
func New(store *featureflag.Store) *Handler { return &Handler{store: store} }

// ListItem is one entry in the GET /v1/admin/features response.
type ListItem struct {
	Key           string     `json:"key"`
	Enabled       bool       `json:"enabled"`
	Description   string     `json:"description"`
	Category      string     `json:"category"`
	LastChangedBy string     `json:"last_changed_by,omitempty"`
	LastChangedAt *time.Time `json:"last_changed_at,omitempty"`
	HasOverride   bool       `json:"has_override"`
}

// ListResponse is the GET /v1/admin/features shape.
type ListResponse struct {
	Items []ListItem `json:"items"`
}

// List handles GET /v1/admin/features. Admin-only.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	flags, err := h.store.List(r.Context())
	if err != nil {
		// Store.List logs and falls back internally; reaching here is
		// the rare class (tenant-context error etc.).
		writeError(w, http.StatusInternalServerError, "list failed: "+err.Error())
		return
	}
	items := make([]ListItem, 0, len(flags))
	for _, f := range flags {
		item := ListItem{
			Key:           f.Key,
			Enabled:       f.Enabled,
			Description:   f.Description,
			Category:      f.Category,
			LastChangedBy: f.LastChangedBy,
			HasOverride:   f.HasOverride,
		}
		if !f.LastChangedAt.IsZero() {
			lc := f.LastChangedAt
			item.LastChangedAt = &lc
		}
		items = append(items, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ListResponse{Items: items})
}

// PatchRequest is the JSON body for PATCH /v1/admin/features/{key}.
type PatchRequest struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// PatchResponse mirrors a single ListItem after the toggle.
type PatchResponse struct {
	Key         string `json:"key"`
	Enabled     bool   `json:"enabled"`
	HasOverride bool   `json:"has_override"`
}

// Patch handles PATCH /v1/admin/features/{key}. Admin-only.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	key := chi.URLParam(r, "key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "flag key is required")
		return
	}

	var req PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	actor := cred.UserID
	if actor == "" {
		actor = cred.ID
	}

	flag, err := h.store.Set(r.Context(), key, req.Enabled, actor, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, featureflag.ErrNotFound):
			writeError(w, http.StatusNotFound, "unknown flag key")
		case errors.Is(err, featureflag.ErrSpineForbidden):
			writeError(w, http.StatusBadRequest, "spine-forbidden flag cannot be toggled")
		case errors.Is(err, featureflag.ErrEmptyActor):
			writeError(w, http.StatusBadRequest, "actor identity missing")
		default:
			writeError(w, http.StatusInternalServerError, "toggle failed: "+err.Error())
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PatchResponse{
		Key:         flag.Key,
		Enabled:     flag.Enabled,
		HasOverride: flag.HasOverride,
	})
}

// --- helpers ---

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
