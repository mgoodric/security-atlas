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
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
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
	// Module is the key of the module the flag belongs to (#2171), and
	// ModuleName its display name. ModuleSwitch is true for the module's
	// own switch and false for a feature inside it, whose display name is
	// FeatureName. The admin page groups by module, with features nested
	// under their module's switch.
	Module       string `json:"module,omitempty"`
	ModuleName   string `json:"module_name,omitempty"`
	ModuleSwitch bool   `json:"module_switch"`
	// ModuleCore marks a feature of a core module, which has no switch of
	// its own and is always on.
	ModuleCore  bool   `json:"module_core"`
	FeatureName string `json:"feature_name,omitempty"`
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
		httperr.WriteInternal(w, r, "list failed", err)
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
		if m, feat, ok := featureflag.ModuleForFlag(f.Key); ok {
			item.Module, item.ModuleName, item.ModuleCore = m.Key, m.Name, m.Core
			if feat != nil {
				item.FeatureName = feat.Name
			} else {
				item.ModuleSwitch = true
			}
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
		httpresp.WriteError(w, http.StatusBadRequest, "flag key is required")
		return
	}

	var req PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
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
			httpresp.WriteError(w, http.StatusNotFound, "unknown flag key")
		case errors.Is(err, featureflag.ErrSpineForbidden):
			httpresp.WriteError(w, http.StatusBadRequest, "spine-forbidden flag cannot be toggled")
		case errors.Is(err, featureflag.ErrEmptyActor):
			httpresp.WriteError(w, http.StatusBadRequest, "actor identity missing")
		default:
			httperr.WriteInternal(w, r, "toggle failed", err)
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

// Module states reported by GET /v1/me/modules.
const (
	// ModuleOn means the module is switched on for the caller's tenant.
	ModuleOn = "on"
	// ModuleSwitchedOff means the tenant switched the module off: its
	// routes answer 404 with the switched-off marker, and the web app
	// hides it.
	ModuleSwitchedOff = "switched_off"
)

// EffectiveModule is one optional module in the GET /v1/me/modules
// response: its state for the caller, and the web app pages it owns, so
// the web app gates nav entries and pages from the server's registry
// rather than a map of its own.
type EffectiveModule struct {
	Key      string             `json:"key"`
	Name     string             `json:"name"`
	State    string             `json:"state"`
	Pages    []string           `json:"pages"`
	Features []EffectiveFeature `json:"features"`
}

// EffectiveFeature is one feature inside an optional module. Its state is
// switched_off when its module is off or its own switch is off (#2171).
type EffectiveFeature struct {
	Key   string   `json:"key"`
	Name  string   `json:"name"`
	State string   `json:"state"`
	Pages []string `json:"pages"`
}

// ModulesResponse is the GET /v1/me/modules shape.
type ModulesResponse struct {
	Modules []EffectiveModule `json:"modules"`
}

// Modules handles GET /v1/me/modules: the caller's effective modules
// (#2166). Every signed-in role may read it for its own tenant; the flag
// reads are tenant-scoped under RLS (invariant #6). It reports each
// optional module in featureflag.Modules, never the full flag inventory
// or its audit metadata, which stay on the admin-only
// GET /v1/admin/features.
func (h *Handler) Modules(w http.ResponseWriter, r *http.Request) {
	if _, ok := authctx.CredentialFromContext(r.Context()); !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	optional := featureflag.OptionalModules()
	out := make([]EffectiveModule, 0, len(optional))
	for _, m := range optional {
		// Enabled() falls open to the Seed default on a DB error; only an
		// unknown FlagKey (a registry code bug) surfaces an error.
		on, err := featureflag.Enabled(r.Context(), h.store, m.FlagKey)
		if err != nil {
			httperr.WriteInternal(w, r, "feature flag read failed", err)
			return
		}
		em := EffectiveModule{Key: m.Key, Name: m.Name, State: stateOf(on), Pages: nonNil(m.Pages), Features: []EffectiveFeature{}}
		for _, f := range m.Features {
			fon, err := featureflag.Enabled(r.Context(), h.store, f.FlagKey)
			if err != nil {
				httperr.WriteInternal(w, r, "feature flag read failed", err)
				return
			}
			em.Features = append(em.Features, EffectiveFeature{Key: f.Key, Name: f.Name, State: stateOf(on && fon), Pages: nonNil(f.Pages)})
		}
		out = append(out, em)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ModulesResponse{Modules: out})
}

// --- helpers ---

func stateOf(on bool) string {
	if on {
		return ModuleOn
	}
	return ModuleSwitchedOff
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return false
	}
	return true
}
