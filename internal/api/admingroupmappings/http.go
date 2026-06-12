// Package admingroupmappings is the admin HTTP surface for managing IdP
// group-to-role mappings (slice 509): /v1/admin/group-role-mappings.
//
// These routes live on the /v1 tree and require an ADMIN atlas credential
// (cred.IsAdmin) — editing a mapping is a privilege-granting action (AC-8 /
// STRIDE-T). A non-admin caller gets 403. The handler validates that a mapping
// only ever targets an EXISTING canonical atlas role (P0-509-4) before the
// write; a non-existent role is a 400, never an auto-created role.
//
// The store runs every operation under the caller's tenant RLS context, set by
// the jwtmw middleware (invariant #6): an admin of tenant A can never CRUD
// tenant B's mappings.
package admingroupmappings

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
)

const maxBody = 16 * 1024

// Handler owns the admin group-role-mapping routes.
type Handler struct {
	store *grouprole.Store
}

// New constructs a Handler over the mapping store.
func New(store *grouprole.Store) *Handler { return &Handler{store: store} }

// CreateRequest is the POST body. idp_config_id is optional: omit (or null) for
// the SCIM / IdP-config-agnostic source; set it to scope the mapping to a
// specific OIDC config (AC-6 multi-IdP).
type CreateRequest struct {
	IDPConfigID string `json:"idp_config_id,omitempty"`
	GroupRef    string `json:"group_ref"`
	Role        string `json:"role"`
}

// MappingResponse is one mapping in API responses.
type MappingResponse struct {
	ID          string  `json:"id"`
	IDPConfigID *string `json:"idp_config_id"`
	GroupRef    string  `json:"group_ref"`
	Role        string  `json:"role"`
}

// ListResponse is the GET shape.
type ListResponse struct {
	Items []MappingResponse `json:"items"`
}

// Create handles POST /v1/admin/group-role-mappings (AC-8).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	cred, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	var req CreateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.GroupRef == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "group_ref is required")
		return
	}
	// P0-509-4: reject a mapping to a non-existent role BEFORE the write.
	if err := grouprole.ValidateMappingRole(req.Role); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "role must be an existing atlas role")
		return
	}

	in := grouprole.CreateMappingInput{GroupRef: req.GroupRef, Role: req.Role}
	if req.IDPConfigID != "" {
		cfg, err := uuid.Parse(req.IDPConfigID)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "idp_config_id must be a UUID")
			return
		}
		in.IDPConfigID = &cfg
	}
	if u, err := parseUserID(cred.UserID); err == nil {
		in.CreatedBy = &u
	}

	m, err := h.store.Create(r.Context(), in)
	if err != nil {
		httperr.WriteInternal(w, r, "create group-role mapping", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, toResponse(m))
}

// List handles GET /v1/admin/group-role-mappings (AC-8).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	mappings, err := h.store.List(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list group-role mappings", err)
		return
	}
	items := make([]MappingResponse, 0, len(mappings))
	for _, m := range mappings {
		items = append(items, toResponse(m))
	}
	httpresp.WriteJSON(w, http.StatusOK, ListResponse{Items: items})
}

// Delete handles DELETE /v1/admin/group-role-mappings/{id} (AC-8).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		if errors.Is(err, grouprole.ErrMappingNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "unknown mapping id")
			return
		}
		httperr.WriteInternal(w, r, "delete group-role mapping", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

type adminCred struct {
	TenantID string
	UserID   string
}

// requireAdmin enforces the admin gate (AC-8). A missing credential is 401; a
// non-admin credential is 403. Authority is enforced server-side, never in the
// UI.
func requireAdmin(w http.ResponseWriter, r *http.Request) (adminCred, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return adminCred{}, false
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return adminCred{}, false
	}
	return adminCred{TenantID: cred.TenantID, UserID: cred.UserID}, true
}

func parseUserID(s string) (uuid.UUID, error) {
	if len(s) > 5 && s[:5] == "user:" {
		s = s[5:]
	}
	return uuid.Parse(s)
}

func toResponse(m grouprole.Mapping) MappingResponse {
	out := MappingResponse{
		ID:       m.ID.String(),
		GroupRef: m.GroupRef,
		Role:     m.Role,
	}
	if m.IDPConfigID != nil {
		s := m.IDPConfigID.String()
		out.IDPConfigID = &s
	}
	return out
}
