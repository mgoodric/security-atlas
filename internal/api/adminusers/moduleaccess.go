package adminusers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// SetModuleAccess wires a member's module access (#2172): the two
// /v1/admin/users/{id}/module-access routes, and module_access on the
// list and get responses. Returns the handler so it composes with New(...).
func (h *Handler) SetModuleAccess(s *moduleaccess.Store) *Handler {
	h.access = s
	return h
}

// ModuleAccessResponse is the GET / PUT /v1/admin/users/{id}/module-access
// shape. Modules is null for "all" (a wildcard that includes modules
// switched on later), else the optional-module keys the member may work
// in, in registry order.
type ModuleAccessResponse struct {
	UserID  string   `json:"user_id"`
	Modules []string `json:"modules"`
}

// GetModuleAccess handles GET /v1/admin/users/{id}/module-access. Tenant
// admin only; the member must belong to the caller's tenant (404).
func (h *Handler) GetModuleAccess(w http.ResponseWriter, r *http.Request) {
	tenantID, target, ok := h.moduleAccessTarget(w, r)
	if !ok {
		return
	}
	modules, err := h.access.Get(r.Context(), tenantID, target.String())
	if err != nil {
		httperr.WriteInternal(w, r, "get module access", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, ModuleAccessResponse{UserID: target.String(), Modules: modules})
}

// PutModuleAccess handles PUT /v1/admin/users/{id}/module-access with the
// body {"modules": null} for "all" or {"modules": ["policies", ...]} for an
// explicit set of optional modules (an empty list leaves core modules
// only). The field is required, so a typo cannot silently mean "all".
// Every change writes one access audit-log row in the same transaction.
//
// An admin cannot narrow their own access (#2166 story 18): module access
// ignores admins, but a narrowed value would bite the moment they gave up
// the admin role.
func (h *Handler) PutModuleAccess(w http.ResponseWriter, r *http.Request) {
	tenantID, target, ok := h.moduleAccessTarget(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&body); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	raw, present := body["modules"]
	if !present {
		httpresp.WriteError(w, http.StatusBadRequest, "modules is required: null for all modules, or a list of module keys")
		return
	}
	var keys []string
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		keys = []string{}
		if err := json.Unmarshal(raw, &keys); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "modules must be null or a list of module keys")
			return
		}
	}
	modules, err := moduleaccess.Normalize(keys)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	if self, isUser := credstore.CallerUserID(cred); isUser && self == target && modules != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "you cannot narrow your own module access")
		return
	}
	actor := cred.UserID
	if actor == "" {
		actor = cred.ID
	}
	if err := h.access.Set(r.Context(), tenantID, target.String(), modules, actor); err != nil {
		httperr.WriteInternal(w, r, "set module access", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, ModuleAccessResponse{UserID: target.String(), Modules: modules})
}

// moduleAccessTarget gates a module-access route on a tenant admin and
// resolves the target member, which must be a user of the caller's tenant
// (the tenant predicate and RLS both apply): another tenant's user, or no
// user, is 404.
func (h *Handler) moduleAccessTarget(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return uuid.Nil, uuid.Nil, false
	}
	if !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return uuid.Nil, uuid.Nil, false
	}
	if h.access == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "module access unavailable")
		return uuid.Nil, uuid.Nil, false
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return uuid.Nil, uuid.Nil, false
	}
	target, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid user id")
		return uuid.Nil, uuid.Nil, false
	}
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		_, qErr := q.GetAdminUser(ctx, dbx.GetAdminUserParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			ID:       pgtype.UUID{Bytes: target, Valid: true},
		})
		return qErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpresp.WriteError(w, http.StatusNotFound, "user not found")
		return uuid.Nil, uuid.Nil, false
	}
	if err != nil {
		httperr.WriteInternal(w, r, "get user", err)
		return uuid.Nil, uuid.Nil, false
	}
	return tenantID, target, true
}

// withModuleAccess fills module_access on list and get responses. A
// member with no stored row has "all" (null). Without a wired store the
// field is left null.
func (h *Handler) withModuleAccess(ctx context.Context, tenantID uuid.UUID, items []UserResponse) error {
	if h.access == nil || len(items) == 0 {
		return nil
	}
	all, err := h.access.List(ctx, tenantID)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].ModuleAccess = all[items[i].ID]
	}
	return nil
}
