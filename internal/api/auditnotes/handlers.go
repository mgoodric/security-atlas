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
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// threadReader is the per-route read seam the GET /v1/audit-notes/thread path
// reads through (slice 689, contract-tier rollout). It carries JUST the
// ListThreadForScope method that route needs — deliberately narrow (slice 409
// D1 / slice 411 D2 / slice 412 D2 sizing rule: a one-method seam over the
// wider notes.Store, NOT a mirror of its create/list-for-author surface). The
// /thread route has a real verbatim-passthrough BFF
// (web/app/api/audit/audit-notes/thread/route.ts), so it is the load-bearing
// read here. The contract-tier recorder (handler_contract_test.go) injects a
// fixed-row stub satisfying this seam so the thread wire shape records on the
// plain `go test ./...` unit surface with no Postgres pool (ADR-0007 /
// P0-409-1). The production *notes.Store satisfies it verbatim; the seam is
// unexported and New(*notes.Store, *notifications.Store, *slog.Logger) is
// unchanged (P0-409-2). The Create + legacy List handlers keep using the
// concrete h.store directly.
type threadReader interface {
	ListThreadForScope(ctx context.Context, periodID uuid.UUID, scopeType, scopeID, callerUserID string) ([]notes.Note, error)
}

// Handler bundles the routes over notes.Store + notifications.Store.
//
// reader is the slice-689 per-route read seam the Thread path reads through;
// New points it at store, so production behavior is identical.
type Handler struct {
	store         *notes.Store
	notifications *notifications.Store
	logger        *slog.Logger
	reader        threadReader
}

// New constructs a Handler. notifs may be nil during early bring-up;
// when nil, Create writes the audit_note but skips notification dispatch.
// The slice-689 per-route read seam (reader) is wired to the same store —
// the public signature is unchanged (P0-409-2).
func New(store *notes.Store, notifs *notifications.Store, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, notifications: notifs, logger: logger, reader: store}
}

// newHandlerWithReader constructs a Handler whose Thread path reads through an
// arbitrary read seam. It exists ONLY for the slice-689 contract recorder,
// which injects a fixed-row stub so the thread wire shape records with no
// Postgres pool. Unexported — not part of the public surface.
func newHandlerWithReader(reader threadReader) *Handler {
	return &Handler{reader: reader}
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
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	authorID := cred.UserID
	if authorID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}

	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	periodID, err := uuid.Parse(req.AuditPeriodID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
		return
	}
	if req.Body == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be non-empty")
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
			httpresp.WriteError(w, http.StatusBadRequest, "parent_note_id must be a UUID")
			return
		}
		in.ParentNoteID = &parent
	}

	created, err := h.store.CreateV2(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, notes.ErrInvalidScopeType):
			httpresp.WriteError(w, http.StatusBadRequest, "scope_type must be one of control|finding|sample|period|walkthrough")
		case errors.Is(err, notes.ErrInvalidVisibility):
			httpresp.WriteError(w, http.StatusBadRequest, "visibility must be one of auditor_only|shared")
		case errors.Is(err, notes.ErrParentMismatch):
			httpresp.WriteError(w, http.StatusBadRequest, "parent_note_id is in a different scope or audit_period")
		case errors.Is(err, notes.ErrNotFound):
			httpresp.WriteError(w, http.StatusBadRequest, "parent_note_id not found or not visible to caller")
		default:
			httperr.WriteInternal(w, r, "create audit note", err)
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

	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"audit_note": noteWireFrom(created.Note)})
}

// List handles GET /v1/audit-notes?audit_period_id=<UUID>.
//
// Returns the author's own notes within a period (slice 025 legacy
// behavior). For thread retrieval use GET /v1/audit-notes/thread.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	authorID := cred.UserID
	if authorID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}
	raw := r.URL.Query().Get("audit_period_id")
	if raw == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "audit_period_id query parameter is required")
		return
	}
	periodID, err := uuid.Parse(raw)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
		return
	}

	rows, err := h.store.ListForAuthorAndPeriod(ctx, periodID, authorID)
	if err != nil {
		httperr.WriteInternal(w, r, "list audit notes", err)
		return
	}
	out := make([]noteWire, len(rows))
	for i, n := range rows {
		out[i] = noteWireFrom(n)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
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
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	callerID := cred.UserID
	if callerID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}

	q := r.URL.Query()
	rawPeriod := q.Get("audit_period_id")
	if rawPeriod == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "audit_period_id query parameter is required")
		return
	}
	periodID, err := uuid.Parse(rawPeriod)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
		return
	}
	scopeType := q.Get("scope_type")
	if scopeType == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "scope_type query parameter is required")
		return
	}
	scopeID := q.Get("scope_id")

	rows, err := h.reader.ListThreadForScope(ctx, periodID, scopeType, scopeID, callerID)
	if err != nil {
		if errors.Is(err, notes.ErrInvalidScopeType) {
			httpresp.WriteError(w, http.StatusBadRequest, "scope_type must be one of control|finding|sample|period|walkthrough")
			return
		}
		httperr.WriteInternal(w, r, "list audit-note thread", err)
		return
	}
	out := make([]noteWire, len(rows))
	for i, n := range rows {
		out[i] = noteWireFrom(n)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
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
