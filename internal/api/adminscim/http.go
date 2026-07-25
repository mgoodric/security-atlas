// Package adminscim is the admin HTTP surface for managing SCIM provisioning
// credentials (slice 508): /v1/admin/scim-credentials.
//
// These routes live on the /v1 tree and require an ADMIN atlas credential
// (cred.IsAdmin) — issuing a SCIM token is an administrative act (AC-3). The
// SCIM token they mint is a DIFFERENT, narrower credential (internal/scim
// CredentialStore) that can ONLY drive /scim/v2 (P0-508-2); this admin surface
// is the issuance/revocation control plane for it.
//
// The bearer plaintext is returned exactly once at issue and never persisted.
package adminscim

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/scim"
)

// Handler owns the admin SCIM-credential routes.
type Handler struct {
	store *scim.CredentialStore
}

// New constructs a Handler.
func New(store *scim.CredentialStore) *Handler { return &Handler{store: store} }

// IssueRequest is the POST body. tenant is derived strictly from the calling
// credential (slice 033 D1) — no tenant_id field is accepted.
type IssueRequest struct {
	Description string `json:"description"`
}

// IssueResponse carries the bearer plaintext (returned exactly once).
type IssueResponse struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	BearerToken string    `json:"bearer_token"`
	Last4       string    `json:"last4"`
	Description string    `json:"description"`
	IssuedAt    time.Time `json:"issued_at"`
}

// Issue handles POST /v1/admin/scim-credentials.
func (h *Handler) Issue(w http.ResponseWriter, r *http.Request) {
	cred, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	var req IssueRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var issuedBy *uuid.UUID
	if u, err := parseUserID(cred.UserID); err == nil {
		issuedBy = &u
	}
	issued, plain, err := h.store.Issue(r.Context(), cred.TenantID, req.Description, issuedBy)
	if err != nil {
		httperr.WriteInternal(w, r, "issue scim credential", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, IssueResponse{
		ID:          issued.ID.String(),
		TenantID:    issued.TenantID.String(),
		BearerToken: plain,
		Last4:       issued.Last4,
		Description: issued.Description,
		IssuedAt:    issued.IssuedAt,
	})
}

// ListItem is one row in the list — never includes the bearer plaintext.
type ListItem struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Description string     `json:"description"`
	Last4       string     `json:"last4"`
	IssuedAt    time.Time  `json:"issued_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

// ListResponse is the GET shape.
type ListResponse struct {
	Items []ListItem `json:"items"`
}

// List handles GET /v1/admin/scim-credentials.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	cred, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	creds, err := h.store.List(r.Context(), cred.TenantID)
	if err != nil {
		httperr.WriteInternal(w, r, "list scim credentials", err)
		return
	}
	items := make([]ListItem, 0, len(creds))
	for _, c := range creds {
		item := ListItem{
			ID:          c.ID.String(),
			TenantID:    c.TenantID.String(),
			Description: c.Description,
			Last4:       c.Last4,
			IssuedAt:    c.IssuedAt,
		}
		if !c.LastUsedAt.IsZero() {
			lu := c.LastUsedAt
			item.LastUsedAt = &lu
		}
		items = append(items, item)
	}
	httpresp.WriteJSON(w, http.StatusOK, ListResponse{Items: items})
}

// Revoke handles DELETE /v1/admin/scim-credentials/{id} (AC-3).
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	cred, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	if err := h.store.Revoke(r.Context(), cred.TenantID, id); err != nil {
		if errors.Is(err, scim.ErrUnknownCredential) {
			httpresp.WriteError(w, http.StatusNotFound, "unknown credential id")
			return
		}
		httperr.WriteInternal(w, r, "revoke scim credential", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

type adminCred struct {
	TenantID string
	UserID   string
}

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

// parseUserID strips the "user:" prefix the atlas JWT subject carries before
// parsing (mirrors internal/api/adminusers.actorFromContext).
func parseUserID(s string) (uuid.UUID, error) {
	if len(s) > 5 && s[:5] == "user:" {
		s = s[5:]
	}
	return uuid.Parse(s)
}
