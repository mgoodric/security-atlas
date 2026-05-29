// Slice 108: GET /v1/me/preferences + PATCH /v1/me/preferences. Per-event-per-channel
// notification matrix over user_notification_preferences. Defaults to enabled for
// every (event, channel) cell when no row exists.
package me

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/userprefs"
)

// PreferencesHandler bundles the GET/PATCH /v1/me/preferences routes.
type PreferencesHandler struct {
	prefs *userprefs.Store
	// pool is reused only for me_audit_log writes (Upsert + audit write are not
	// transactional together — the audit row is best-effort, mirroring the
	// profile handler).
	pool *pgxpool.Pool
}

// NewPreferences constructs a PreferencesHandler.
func NewPreferences(store *userprefs.Store, pool *pgxpool.Pool) *PreferencesHandler {
	return &PreferencesHandler{prefs: store, pool: pool}
}

// ----- wire shapes -----

// preferencesWire mirrors userprefs.Preferences in JSON. Map-of-maps; outer key =
// event, inner key = channel, bool = enabled. Returned by GET; accepted (partial)
// by PATCH.
type preferencesWire map[string]map[string]bool

// ----- handlers -----

// GetPreferences handles GET /v1/me/preferences. Returns the full matrix with
// defaults filled in for missing cells. AC-4.
func (h *PreferencesHandler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	userUUID, err := uuid.Parse(cred.UserID)
	if err != nil {
		httpresp.WriteError(w, http.StatusNotFound, "no preferences for this credential")
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context invalid")
		return
	}
	out, err := h.prefs.Get(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "get preferences", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"preferences": preferencesWire(out)})
}

// PatchPreferences handles PATCH /v1/me/preferences. Accepts a partial matrix;
// merges per-cell. Returns 400 on unknown event or channel keys (ISC-A4 — no
// silent ignore). AC-5.
func (h *PreferencesHandler) PatchPreferences(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	userUUID, err := uuid.Parse(cred.UserID)
	if err != nil {
		httpresp.WriteError(w, http.StatusNotFound, "no preferences for this credential")
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context invalid")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*1024))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}
	if len(body) == 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "empty body")
		return
	}
	// Accept the matrix either as a bare object (preferences map at top level) or
	// wrapped under {"preferences": ...}. The wrapped shape mirrors the GET response.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var matrix userprefs.Preferences
	if inner, ok := raw["preferences"]; ok {
		if err := json.Unmarshal(inner, &matrix); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "preferences must be an object")
			return
		}
	} else {
		if err := json.Unmarshal(body, &matrix); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "body must be a preferences matrix")
			return
		}
	}
	if len(matrix) == 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "preferences body must contain at least one event")
		return
	}
	// Snapshot prior state for audit-log diff (before-image).
	before, err := h.prefs.Get(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "load preferences before patch", err)
		return
	}
	if err := h.prefs.Upsert(ctx, tenantUUID, userUUID, matrix); err != nil {
		if errors.Is(err, userprefs.ErrUnknownEvent) {
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, userprefs.ErrUnknownChannel) {
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httperr.WriteInternal(w, r, "upsert preferences", err)
		return
	}
	after, err := h.prefs.Get(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "load preferences after patch", err)
		return
	}
	// Audit-log: only the cells the patch touched.
	auditBefore := map[string]any{}
	auditAfter := map[string]any{}
	for ev, channels := range matrix {
		for ch := range channels {
			auditBefore[ev+"."+ch] = before[ev][ch]
			auditAfter[ev+"."+ch] = after[ev][ch]
		}
	}
	if !mapsEqual(auditBefore, auditAfter) {
		// Best-effort audit write — see ProfileHandler.writeAuditLog for the
		// non-fatal failure rationale.
		ah := &ProfileHandler{pool: h.pool}
		_ = ah.writeAuditLog(ctx, tenantUUID, userUUID, "preferences.update", auditBefore, auditAfter)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"preferences": preferencesWire(after)})
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
