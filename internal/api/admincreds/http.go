// Package admincreds is the HTTP surface for /v1/admin/credentials.
//
// It wraps the DB-backed apikeystore (slice 034) — issue, list, rotate,
// revoke — with JSON request/response shapes. The bearer plaintext is
// returned exactly once in the issue/rotate response and never persisted.
//
// Auth: every handler requires an admin credential. The slice-014 IsAdmin
// flag on credstore.Credential gates access; slice 035 will graduate this
// to OPA-driven RBAC.
package admincreds

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the HTTP routes.
type Handler struct {
	store *apikeystore.Store
}

// New constructs a Handler.
func New(store *apikeystore.Store) *Handler { return &Handler{store: store} }

// IssueRequest is the JSON body for POST /v1/admin/credentials.
//
// Slice 051: the TenantID field is retained for back-compat error
// messaging only. Tenant is derived strictly from the calling credential
// (slice 033 D1 — "tenancy.Middleware sets app.current_tenant strictly
// from cred.TenantID; no handler-level overrides"). Any non-empty value
// supplied in this field causes Issue to return HTTP 400 with a
// descriptive error so legacy clients discover the contract change
// instead of seeing a JSON-decode failure or, worse, silent acceptance.
type IssueRequest struct {
	TenantID       string   `json:"tenant_id,omitempty"`
	ScopePredicate string   `json:"scope_predicate"`
	AllowedKinds   []string `json:"allowed_kinds"`
	TTLSeconds     int64    `json:"ttl_seconds"`
	IsAdmin        bool     `json:"is_admin"`
	IsApprover     bool     `json:"is_approver"`
	OwnerRoles     []string `json:"owner_roles"`
}

// IssueResponse carries the bearer plaintext (returned exactly once).
type IssueResponse struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	BearerToken string    `json:"bearer_token"`
	Last4       string    `json:"last4"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   string    `json:"expires_at,omitempty"`
}

// Issue handles POST /v1/admin/credentials.
//
// Slice 051: tenant is derived strictly from the calling credential.
// slice-033's tenancymw.Middleware already set app.current_tenant from
// cred.TenantID before this handler runs; we trust that GUC and pass
// cred.TenantID through to the store. The IssueRequest.TenantID field
// is rejected with a 400 if supplied, surfacing the contract change to
// legacy callers.
func (h *Handler) Issue(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var req IssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.TenantID != "" {
		writeError(w, http.StatusBadRequest, "tenant_id is not accepted; tenant is derived from the calling credential")
		return
	}
	// requireAdmin returning true guarantees the credential is in context.
	cred, _ := authctx.CredentialFromContext(r.Context())
	issued, plain, err := h.store.Issue(r.Context(), cred.TenantID, apikeystore.IssueInput{
		ScopePredicate: req.ScopePredicate,
		AllowedKinds:   req.AllowedKinds,
		TTL:            time.Duration(req.TTLSeconds) * time.Second,
		IsAdmin:        req.IsAdmin,
		IsApprover:     req.IsApprover,
		OwnerRoles:     req.OwnerRoles,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue failed: "+err.Error())
		return
	}
	resp := IssueResponse{
		ID:          issued.ID,
		TenantID:    issued.TenantID,
		BearerToken: plain,
		Last4:       issued.Last4,
		IssuedAt:    issued.IssuedAt,
	}
	if !issued.IssuedAt.IsZero() && issued.TTL > 0 {
		resp.ExpiresAt = issued.IssuedAt.Add(issued.TTL).Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// ListItem is one row in the credentials list — never includes bearer_token.
type ListItem struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	ScopePredicate string     `json:"scope_predicate"`
	AllowedKinds   []string   `json:"allowed_kinds"`
	Last4          string     `json:"last4"`
	IssuedAt       time.Time  `json:"issued_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	IsAdmin        bool       `json:"is_admin"`
	IsApprover     bool       `json:"is_approver"`
	OwnerRoles     []string   `json:"owner_roles"`
	RotatedFrom    string     `json:"rotated_from,omitempty"`
}

// ListResponse is the GET /v1/admin/credentials shape.
type ListResponse struct {
	Items []ListItem `json:"items"`
}

// List handles GET /v1/admin/credentials.
//
// Slice 051: tenant is derived strictly from the calling credential.
// The previously-accepted ?tenant_id query parameter is rejected with
// 400 — see the Issue docstring for the rationale.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if r.URL.Query().Get("tenant_id") != "" {
		writeError(w, http.StatusBadRequest, "tenant_id query parameter is not accepted; tenant is derived from the calling credential")
		return
	}
	// requireAdmin returning true guarantees the credential is in context.
	cred, _ := authctx.CredentialFromContext(r.Context())
	creds, err := h.store.List(r.Context(), cred.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed: "+err.Error())
		return
	}
	items := make([]ListItem, 0, len(creds))
	for _, c := range creds {
		item := ListItem{
			ID:             c.ID,
			TenantID:       c.TenantID,
			ScopePredicate: c.ScopePredicate,
			AllowedKinds:   c.Kinds,
			Last4:          c.Last4,
			IssuedAt:       c.IssuedAt,
			IsAdmin:        c.IsAdmin,
			IsApprover:     c.IsApprover,
			OwnerRoles:     c.OwnerRoles,
			RotatedFrom:    c.RotatedFrom,
		}
		if !c.LastUsedAt.IsZero() {
			lu := c.LastUsedAt
			item.LastUsedAt = &lu
		}
		items = append(items, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ListResponse{Items: items})
}

// RotateResponse carries the successor's bearer plaintext + the
// predecessor's retirement deadline.
type RotateResponse struct {
	ID                   string    `json:"id"`
	BearerToken          string    `json:"bearer_token"`
	Last4                string    `json:"last4"`
	PredecessorExpiresAt time.Time `json:"predecessor_expires_at"`
}

// Rotate handles POST /v1/admin/credentials/:id/rotate.
func (h *Handler) Rotate(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	id := chi.URLParam(r, "id")
	uid, err := parseCredID(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, terr := tenancy.TenantFromContext(ctx); terr != nil {
		writeError(w, http.StatusBadRequest, "invalid tenant in credential")
		return
	}
	successor, plain, predExp, err := h.store.Rotate(ctx, cred.TenantID, uid)
	if err != nil {
		if errors.Is(err, apikeystore.ErrUnknownKey) {
			writeError(w, http.StatusNotFound, "unknown credential id")
			return
		}
		writeError(w, http.StatusInternalServerError, "rotate failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RotateResponse{
		ID:                   successor.ID,
		BearerToken:          plain,
		Last4:                successor.Last4,
		PredecessorExpiresAt: predExp,
	})
}

// Revoke handles POST /v1/admin/credentials/:id/revoke.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	id := chi.URLParam(r, "id")
	uid, err := parseCredID(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, terr := tenancy.TenantFromContext(ctx); terr != nil {
		writeError(w, http.StatusBadRequest, "invalid tenant in credential")
		return
	}
	if err := h.store.Revoke(ctx, cred.TenantID, uid); err != nil {
		if errors.Is(err, apikeystore.ErrUnknownKey) {
			writeError(w, http.StatusNotFound, "unknown credential id")
			return
		}
		writeError(w, http.StatusInternalServerError, "revoke failed: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func parseCredID(s string) (uuid.UUID, error) {
	s = strings.TrimPrefix(s, "key_")
	return uuid.Parse(s)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
