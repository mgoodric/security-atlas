// Notifications endpoints under /v1/me. Part of the slice 029 Audit Hub
// activation: in-app notifications fire when an audit-note is created on
// a shared thread; recipients pull them via /v1/me/notifications.
//
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	GET   /v1/me/notifications                    caller's notifications (paged)
//	PATCH /v1/me/notifications/{id}/read          mark a notification read
//
// Both endpoints scope to caller.UserID at the query layer; RLS is the
// defense-in-depth layer. The upstream authz middleware (slice 035)
// allows any authenticated role to hit /v1/me; the query layer ensures
// the data is the caller's own.
package me

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/notifications"
)

// NotificationsHandler bundles the /v1/me/notifications routes over a
// single notifications.Store.
type NotificationsHandler struct {
	store *notifications.Store
}

// NewNotifications constructs a NotificationsHandler.
func NewNotifications(store *notifications.Store) *NotificationsHandler {
	return &NotificationsHandler{store: store}
}

// ----- wire shapes -----

type notificationWire struct {
	ID              string         `json:"id"`
	RecipientUserID string         `json:"recipient_user_id"`
	Type            string         `json:"type"`
	Payload         map[string]any `json:"payload"`
	CreatedAt       string         `json:"created_at"`
	ReadAt          *string        `json:"read_at,omitempty"`
}

func notificationWireFrom(n notifications.Notification) notificationWire {
	w := notificationWire{
		ID:              n.ID.String(),
		RecipientUserID: n.RecipientUserID,
		Type:            n.Type,
		Payload:         n.Payload,
		CreatedAt:       n.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if n.ReadAt != nil {
		s := n.ReadAt.UTC().Format(time.RFC3339Nano)
		w.ReadAt = &s
	}
	return w
}

// ----- handlers -----

// List handles GET /v1/me/notifications.
//
// Query params:
//
//	limit  (optional, default 50, max 200)
//	offset (optional, default 0)
//
// Returns the caller's notifications (unread first, then newest), plus
// the unread count in the same payload so the UI can render an unread
// badge without a second roundtrip.
func (h *NotificationsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if cred.UserID == "" {
		writeError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}

	// strconv.ParseInt with bitSize 32 yields an int64 already
	// constrained to int32 range -- the conversion to int32 is
	// provably safe (no taint warning from CodeQL).
	limit := int32(50)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if n > 200 {
			n = 200
		}
		limit = int32(n)
	}
	offset := int32(0)
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = int32(n)
	}

	rows, unread, err := h.store.ListForRecipient(ctx, cred.UserID, limit, offset)
	if err != nil {
		writeServerErr(w, "list notifications", err)
		return
	}
	out := make([]notificationWire, len(rows))
	for i, n := range rows {
		out[i] = notificationWireFrom(n)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": out,
		"count":         len(out),
		"unread_count":  unread,
	})
}

// MarkRead handles PATCH /v1/me/notifications/{id}/read.
//
// Idempotent: re-marking an already-read notification preserves the
// original read_at timestamp.
func (h *NotificationsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if cred.UserID == "" {
		writeError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}
	raw := chi.URLParam(r, "id")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "id path parameter is required")
		return
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	n, err := h.store.MarkRead(ctx, id, cred.UserID)
	if err != nil {
		if errors.Is(err, notifications.ErrNotFound) {
			writeError(w, http.StatusNotFound, "notification not found")
			return
		}
		writeServerErr(w, "mark notification read", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification": notificationWireFrom(n)})
}

// ----- helpers shared with audit_period.go -----
// authnContext, writeJSON, writeError, writeServerErr live in audit_period.go;
// we extract them into a shared helper if a third route lands.

// jsonEncoder kept here so the file is self-contained for unit tests
// that might pull this package independently.
var _ = json.Marshal
