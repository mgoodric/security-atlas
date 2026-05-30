// Slice 108: GET /v1/me/sessions + DELETE /v1/me/sessions/{id} + DELETE /v1/me/sessions.
//
// Reads + revokes the caller's slice-034 sessions rows. The is_current flag uses the
// atlas_session cookie when present (slice 034 OIDC flow set it on login); when the
// caller is on a bearer-only path (BFF holding sa_session_token), no row is flagged
// current — that's honest, not a bug. A spillover slice files BFF cookie forwarding.
//
// Cross-user revoke is OUT OF SCOPE for this slice. The SQL WHERE clause's user_id
// guard ensures a cross-user id never touches another user's row; we return 404 (not
// 403) to avoid the existence oracle.
package me

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
)

// SessionsHandler bundles the /v1/me/sessions routes.
type SessionsHandler struct {
	sessions *sessions.Store
	pool     *pgxpool.Pool
}

// NewSessions constructs a SessionsHandler.
func NewSessions(store *sessions.Store, pool *pgxpool.Pool) *SessionsHandler {
	return &SessionsHandler{sessions: store, pool: pool}
}

// ----- wire shapes -----

// sessionWire is the JSON shape returned on `/v1/me/sessions`.
//
// Slice 162: extended with `user_agent`, `ip_address`, `geo_country`, `geo_city`.
// All four are pointer-string + `omitempty` so a row that lacks them (pre-migration
// rows, sessions created by background flows with no http.Request in scope, or
// the not-yet-populated geo columns) renders as missing-field on the wire rather
// than `"": null`. The frontend (slice 162 session-line helper) treats missing
// and empty identically — honest empty, no fabricated placeholder text (P0-162-1).
type sessionWire struct {
	ID         string  `json:"id"`
	Last4      string  `json:"last4"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	IsCurrent  bool    `json:"is_current"`
	UserAgent  string  `json:"user_agent,omitempty"`
	IPAddress  string  `json:"ip_address,omitempty"`
	GeoCountry string  `json:"geo_country,omitempty"`
	GeoCity    string  `json:"geo_city,omitempty"`
}

func sessionWireFrom(s sessions.Session, currentID string) sessionWire {
	w := sessionWire{
		ID:         s.ID,
		Last4:      last4OfSessionID(s.ID),
		CreatedAt:  s.IssuedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		IsCurrent:  s.ID == currentID && currentID != "",
		UserAgent:  s.UserAgent,
		IPAddress:  s.IPAddress,
		GeoCountry: s.GeoCountry,
		GeoCity:    s.GeoCity,
	}
	if !s.LastSeenAt.IsZero() {
		ts := s.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		w.LastUsedAt = &ts
	}
	return w
}

// last4OfSessionID extracts the last four chars of the session id for a human-readable
// fingerprint on the wire. Session IDs are URL-safe-base64 43 chars; the trailing
// four characters are random enough to disambiguate "this device" from "another
// device" in the UI without leaking the full id (which is the cookie value).
func last4OfSessionID(id string) string {
	if len(id) <= 4 {
		return id
	}
	return id[len(id)-4:]
}

// ----- handlers -----

// ListSessions handles GET /v1/me/sessions. Returns the caller's currently-valid
// sessions; flags is_current=true on the row whose id matches the atlas_session
// cookie (when the cookie is present). AC-7.
func (h *SessionsHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	userUUID, err := uuid.Parse(cred.UserID)
	if err != nil {
		// No real users row → no sessions surface.
		httpresp.WriteJSON(w, http.StatusOK, map[string]any{"sessions": []sessionWire{}, "count": 0})
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context invalid")
		return
	}
	rows, err := h.sessions.ListForUser(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "list sessions", err)
		return
	}
	currentID := currentSessionIDFromRequest(r)
	out := make([]sessionWire, len(rows))
	for i, s := range rows {
		out[i] = sessionWireFrom(s, currentID)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"sessions": out,
		"count":    len(out),
	})

}

// RevokeSession handles DELETE /v1/me/sessions/{id}. Returns 204 on success, 404
// when the id is unknown OR belongs to another user (avoids existence oracle). AC-8.
func (h *SessionsHandler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "session id required")
		return
	}
	userUUID, err := uuid.Parse(cred.UserID)
	if err != nil {
		httpresp.WriteError(w, http.StatusNotFound, "session not found")
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context invalid")
		return
	}
	updated, err := h.sessions.RevokeForUser(ctx, tenantUUID, userUUID, id)
	if err != nil {
		httperr.WriteInternal(w, r, "revoke session", err)
		return
	}
	if !updated {
		httpresp.WriteError(w, http.StatusNotFound, "session not found")
		return
	}
	auditBefore := map[string]any{"session_id": id, "revoked": false}
	auditAfter := map[string]any{"session_id": id, "revoked": true}
	ah := &ProfileHandler{pool: h.pool}
	_ = ah.writeAuditLog(ctx, tenantUUID, userUUID, "session.revoke", auditBefore, auditAfter)
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOtherSessions handles DELETE /v1/me/sessions. Revokes every valid session
// for the caller except the one the request is on (identified by the atlas_session
// cookie). AC-9.
func (h *SessionsHandler) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	userUUID, err := uuid.Parse(cred.UserID)
	if err != nil {
		httpresp.WriteError(w, http.StatusNotFound, "no sessions for this credential")
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context invalid")
		return
	}
	currentID := currentSessionIDFromRequest(r)
	n, err := h.sessions.RevokeOthersForUser(ctx, tenantUUID, userUUID, currentID)
	if err != nil {
		httperr.WriteInternal(w, r, "revoke other sessions", err)
		return
	}
	if n > 0 {
		auditBefore := map[string]any{"keep_session_id": currentID, "revoked_count": 0}
		auditAfter := map[string]any{"keep_session_id": currentID, "revoked_count": n}
		ah := &ProfileHandler{pool: h.pool}
		_ = ah.writeAuditLog(ctx, tenantUUID, userUUID, "session.revoke", auditBefore, auditAfter)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"revoked_count": n})
}

// currentSessionIDFromRequest reads the slice 034 atlas_session cookie if present.
// Returns "" when the cookie is absent (bearer-only request paths). The empty
// string is a sentinel: ListSessions flags no row as current; RevokeOtherSessions
// revokes ALL the user's sessions (no "keep" target).
func currentSessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(sessions.CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
