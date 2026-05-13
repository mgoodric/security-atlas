// Package auditnotes serves the slice-025 HTTP API for the auditor's
// private testing-notes workspace.
//
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	POST /v1/audit-notes                          create a note (AC-4)
//	GET  /v1/audit-notes?audit_period_id=<UUID>   list author's notes in a period (AC-4)
//
// All handlers run with the tenant set by upstream auth middleware
// (slice 033 tenancy.Middleware). The store opens its own transaction
// per call and applies the tenant GUC; the query layer also enforces
// `author_user_id = caller.UserID` so an auditor cannot read another
// auditor's notes (P0-2).
//
// Authorization: the upstream slice-035 authz middleware enforces that
// the caller carries the `auditor` role and that the request's
// audit_period_id matches one of the caller's assigned periods. This
// handler also performs defense-in-depth checks (rejects empty
// UserID; rejects empty body; rejects unknown scope_type) so a
// downstream bug in OPA wiring still produces a clean 4xx rather than
// a 500.
package auditnotes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the two routes over a single notes.Store.
type Handler struct {
	store *notes.Store
}

// New constructs a Handler.
func New(store *notes.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type createReq struct {
	AuditPeriodID string `json:"audit_period_id"`
	ScopeType     string `json:"scope_type"`
	ScopeID       string `json:"scope_id,omitempty"`
	Body          string `json:"body"`
}

type noteWire struct {
	ID            string `json:"id"`
	AuditPeriodID string `json:"audit_period_id"`
	AuthorUserID  string `json:"author_user_id"`
	ScopeType     string `json:"scope_type"`
	ScopeID       string `json:"scope_id,omitempty"`
	Body          string `json:"body"`
	Visibility    string `json:"visibility"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func noteWireFrom(n notes.Note) noteWire {
	return noteWire{
		ID:            n.ID.String(),
		AuditPeriodID: n.AuditPeriodID.String(),
		AuthorUserID:  n.AuthorUserID,
		ScopeType:     n.ScopeType,
		ScopeID:       n.ScopeID,
		Body:          n.Body,
		Visibility:    n.Visibility,
		CreatedAt:     n.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		UpdatedAt:     n.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

// ----- handlers -----

// Create handles POST /v1/audit-notes.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	authorID := cred.UserID
	if authorID == "" {
		writeError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}

	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	periodID, err := uuid.Parse(req.AuditPeriodID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
		return
	}
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "body must be non-empty")
		return
	}

	n, err := h.store.Create(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  authorID,
		ScopeType:     req.ScopeType,
		ScopeID:       req.ScopeID,
		Body:          req.Body,
	})
	if err != nil {
		if errors.Is(err, notes.ErrInvalidScopeType) {
			writeError(w, http.StatusBadRequest, "scope_type must be one of control|finding|sample|period")
			return
		}
		writeServerErr(w, "create audit note", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"audit_note": noteWireFrom(n)})
}

// List handles GET /v1/audit-notes?audit_period_id=<UUID>.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	authorID := cred.UserID
	if authorID == "" {
		writeError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}
	raw := r.URL.Query().Get("audit_period_id")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "audit_period_id query parameter is required")
		return
	}
	periodID, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
		return
	}

	rows, err := h.store.ListForAuthorAndPeriod(ctx, periodID, authorID)
	if err != nil {
		writeServerErr(w, "list audit notes", err)
		return
	}
	out := make([]noteWire, len(rows))
	for i, n := range rows {
		out[i] = noteWireFrom(n)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"audit_notes": out,
		"count":       len(out),
	})
}

// ----- helpers -----

func (h *Handler) authnContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeServerErr(w http.ResponseWriter, op string, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": op + ": " + err.Error(),
	})
}
