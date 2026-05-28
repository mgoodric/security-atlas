// Package auditnotes serves the audit-notes HTTP API.
//
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	POST /v1/audit-notes                          create a note (auditor private OR shared)
//	GET  /v1/audit-notes?audit_period_id=...      list author's private notes in a period (slice 025 legacy)
//	GET  /v1/audit-notes/thread?audit_period_id=&scope_type=&scope_id=
//	                                              return the visible thread (slice 029 Audit Hub)
//
// History:
//   - Slice 025: POST + author-scoped GET. Visibility was pinned to
//     'auditor_only'; the column was reserved with a single allowed value.
//   - Slice 029: POST gains parent_note_id + visibility fields. GET
//     /v1/audit-notes/thread is new. Notification dispatch fires after
//     a successful insert (best-effort -- failures are logged but do
//     not fail the request, since the audit_note has already committed).
//
// All handlers run with the tenant set by upstream auth middleware
// (slice 033 tenancy.Middleware). Both notes.Store and notifications.Store
// open their own transaction per call and apply the tenant GUC.
//
// Authorization: upstream slice-035 authz middleware enforces role gates.
// Defense-in-depth here:
//   - empty UserID -> 401
//   - empty body -> 400
//   - unknown scope_type / visibility -> 400
//   - parent-note mismatch (different period/scope) -> 400
package auditnotes

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the routes over notes.Store + notifications.Store.
type Handler struct {
	store         *notes.Store
	notifications *notifications.Store
	logger        *slog.Logger
}

// New constructs a Handler. notifs may be nil during early bring-up;
// when nil, Create writes the audit_note but skips notification dispatch.
func New(store *notes.Store, notifs *notifications.Store, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, notifications: notifs, logger: logger}
}

// ----- wire shapes -----

type createReq struct {
	AuditPeriodID string  `json:"audit_period_id"`
	ScopeType     string  `json:"scope_type"`
	ScopeID       string  `json:"scope_id,omitempty"`
	Body          string  `json:"body"`
	Visibility    string  `json:"visibility,omitempty"`     // slice 029: 'auditor_only' | 'shared'
	ParentNoteID  *string `json:"parent_note_id,omitempty"` // slice 029: reply target
}

type noteWire struct {
	ID            string  `json:"id"`
	AuditPeriodID string  `json:"audit_period_id"`
	AuthorUserID  string  `json:"author_user_id"`
	ScopeType     string  `json:"scope_type"`
	ScopeID       string  `json:"scope_id,omitempty"`
	Body          string  `json:"body"`
	Visibility    string  `json:"visibility"`
	ParentNoteID  *string `json:"parent_note_id,omitempty"`
	Depth         int     `json:"depth,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func noteWireFrom(n notes.Note) noteWire {
	w := noteWire{
		ID:            n.ID.String(),
		AuditPeriodID: n.AuditPeriodID.String(),
		AuthorUserID:  n.AuthorUserID,
		ScopeType:     n.ScopeType,
		ScopeID:       n.ScopeID,
		Body:          n.Body,
		Visibility:    n.Visibility,
		Depth:         n.Depth,
		CreatedAt:     n.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		UpdatedAt:     n.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if n.ParentNoteID != nil {
		s := n.ParentNoteID.String()
		w.ParentNoteID = &s
	}
	return w
}

// ----- handlers -----

// Create handles POST /v1/audit-notes.
//
// Slice 029: accepts parent_note_id + visibility. Default visibility is
// 'shared' (Audit Hub semantics). Auditors writing private notes must
// pass 'auditor_only' explicitly. Successful creates dispatch in-app
// notifications to the distinct prior-thread authors when visibility is
// 'shared'.
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

	in := notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  authorID,
		ScopeType:     req.ScopeType,
		ScopeID:       req.ScopeID,
		Body:          req.Body,
		Visibility:    req.Visibility,
	}
	if req.ParentNoteID != nil && *req.ParentNoteID != "" {
		parent, err := uuid.Parse(*req.ParentNoteID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "parent_note_id must be a UUID")
			return
		}
		in.ParentNoteID = &parent
	}

	created, err := h.store.CreateV2(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, notes.ErrInvalidScopeType):
			writeError(w, http.StatusBadRequest, "scope_type must be one of control|finding|sample|period|walkthrough")
		case errors.Is(err, notes.ErrInvalidVisibility):
			writeError(w, http.StatusBadRequest, "visibility must be one of auditor_only|shared")
		case errors.Is(err, notes.ErrParentMismatch):
			writeError(w, http.StatusBadRequest, "parent_note_id is in a different scope or audit_period")
		case errors.Is(err, notes.ErrNotFound):
			writeError(w, http.StatusBadRequest, "parent_note_id not found or not visible to caller")
		default:
			writeServerErr(w, r, "create audit note", err)
		}
		return
	}

	// Best-effort notification dispatch. Failures here are logged but
	// do not fail the request -- the audit_note has already committed.
	if h.notifications != nil && len(created.NotifyRecipients) > 0 {
		payload := notifications.AuditNotePayload{
			AuditNoteID:   created.Note.ID.String(),
			AuditPeriodID: created.Note.AuditPeriodID.String(),
			ScopeType:     created.Note.ScopeType,
			ScopeID:       created.Note.ScopeID,
			AuthorUserID:  created.Note.AuthorUserID,
		}
		if _, derr := h.notifications.DispatchAuditNoteReply(ctx, created.NotifyRecipients, payload); derr != nil {
			h.logger.Warn("notification dispatch failed",
				"audit_note_id", created.Note.ID.String(),
				"recipients", created.NotifyRecipients,
				"err", derr,
			)
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{"audit_note": noteWireFrom(created.Note)})
}

// List handles GET /v1/audit-notes?audit_period_id=<UUID>.
//
// Returns the author's own notes within a period (slice 025 legacy
// behavior). For thread retrieval use GET /v1/audit-notes/thread.
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
		writeServerErr(w, r, "list audit notes", err)
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

// Thread handles GET /v1/audit-notes/thread.
//
// Query params:
//
//	audit_period_id (required, UUID)
//	scope_type      (required, one of control|finding|sample|period|walkthrough)
//	scope_id        (optional; empty means "no scope_id" / period-level)
//
// Returns the visible thread for the (scope_type, scope_id, period)
// anchor. Visibility filter: shared notes are visible to any tenant
// member; auditor_only notes are visible only to their author.
func (h *Handler) Thread(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	callerID := cred.UserID
	if callerID == "" {
		writeError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}

	q := r.URL.Query()
	rawPeriod := q.Get("audit_period_id")
	if rawPeriod == "" {
		writeError(w, http.StatusBadRequest, "audit_period_id query parameter is required")
		return
	}
	periodID, err := uuid.Parse(rawPeriod)
	if err != nil {
		writeError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
		return
	}
	scopeType := q.Get("scope_type")
	if scopeType == "" {
		writeError(w, http.StatusBadRequest, "scope_type query parameter is required")
		return
	}
	scopeID := q.Get("scope_id")

	rows, err := h.store.ListThreadForScope(ctx, periodID, scopeType, scopeID, callerID)
	if err != nil {
		if errors.Is(err, notes.ErrInvalidScopeType) {
			writeError(w, http.StatusBadRequest, "scope_type must be one of control|finding|sample|period|walkthrough")
			return
		}
		writeServerErr(w, r, "list audit-note thread", err)
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

func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	httperr.WriteInternal(w, r, op, err)
}
