package posturetargets

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler serves the two posture-target routes.
type Handler struct {
	store *Store
}

// New constructs a Handler.
func New(store *Store) *Handler { return &Handler{store: store} }

// hasTargetAdmin reports whether cred may set a posture target: admin or
// grc_engineer. It reads the credential as the risk-taxonomy guard does:
// the platform-admin and approver flags (a browser session's super-admin
// sets both), then the tenant role list a JWT carries in OwnerRoles.
// IsApprover alone is kept on purpose: for a bearer credential
// authz.derivedRolesFor reads it as grc_engineer, so OPA admits that
// credential's PUT, and refusing it here would make the two layers
// disagree.
func hasTargetAdmin(c credstore.Credential) bool {
	if c.IsAdmin || c.IsApprover {
		return true
	}
	for _, role := range c.OwnerRoles {
		if role == "admin" || role == "grc_engineer" {
			return true
		}
	}
	return false
}

// Get handles GET /v1/framework-scopes/posture-targets/{framework_version_id}.
// 404 when no target is set.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !hasTenant(w, r) {
		return
	}
	versionID, ok := parseVersion(w, r)
	if !ok {
		return
	}
	t, err := h.store.Get(r.Context(), versionID)
	if err != nil {
		writeErr(w, r, "get posture target", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, t)
}

// Set handles PUT /v1/framework-scopes/posture-targets/{framework_version_id}.
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	if !hasTenant(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	if !hasTargetAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required to set a posture target")
		return
	}
	versionID, ok := parseVersion(w, r)
	if !ok {
		return
	}
	var in Input
	dec := json.NewDecoder(io.LimitReader(r.Body, 4*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	target, warning, critical, err := in.Validate()
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The signed-in person when there is one, else the credential id, as
	// the period-auditor writes record the actor.
	actor := cred.UserID
	if actor == "" {
		actor = cred.ID
	}
	t, err := h.store.Set(r.Context(), versionID, in.Direction, target, warning, critical, actor)
	if err != nil {
		writeErr(w, r, "set posture target", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, t)
}

func hasTenant(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return false
	}
	return true
}

func parseVersion(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "framework_version_id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version_id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func writeErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, ErrNoTarget):
		httpresp.WriteError(w, http.StatusNotFound, "no posture target set for this framework version")
	case errors.Is(err, ErrVersionNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "framework version not found")
	case errors.Is(err, ErrScopeNotSet):
		httpresp.WriteError(w, http.StatusConflict, "activate a framework scope for this version before setting a posture target")
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}
