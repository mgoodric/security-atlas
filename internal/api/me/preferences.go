// Slice 108: GET /v1/me/preferences + PATCH /v1/me/preferences. Per-event-per-channel
// notification matrix over user_notification_preferences. Defaults to enabled for
// every (event, channel) cell when no row exists. #2145 adds the caller's dismissed
// onboarding hints (user_onboarding_hint_dismissals) to the same resource.
package me

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
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
	userUUID, ok := credstore.CallerUserID(cred)
	if !ok {
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
	hints, err := h.prefs.DismissedHints(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "get dismissed hints", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"preferences":     preferencesWire(out),
		"dismissed_hints": hints,
	})
}

// patchBody is the wrapped PATCH shape. Every field is optional, but at least
// one must change something. #2145 added the two hint fields: dismissed_hints
// maps a module to dismissed (true) or shown again (false); reset_hints clears
// every dismissal ("Show onboarding hints again").
type patchBody struct {
	Preferences    userprefs.Preferences `json:"preferences"`
	DismissedHints map[string]bool       `json:"dismissed_hints"`
	ResetHints     bool                  `json:"reset_hints"`
}

// parsePatchBody accepts the wrapped shape, or the slice 108 bare matrix: an
// object whose top-level keys are events.
func parsePatchBody(body []byte) (patchBody, string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return patchBody{}, "invalid JSON body"
	}
	_, wrappedPrefs := raw["preferences"]
	_, wrappedHints := raw["dismissed_hints"]
	_, wrappedReset := raw["reset_hints"]
	var out patchBody
	if wrappedPrefs || wrappedHints || wrappedReset {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&out); err != nil {
			return patchBody{}, "body must be {preferences, dismissed_hints, reset_hints}"
		}
	} else if err := json.Unmarshal(body, &out.Preferences); err != nil {
		return patchBody{}, "body must be a preferences matrix"
	}
	if len(out.Preferences) == 0 && len(out.DismissedHints) == 0 && !out.ResetHints {
		return patchBody{}, "preferences body must contain at least one event or hint"
	}
	return out, ""
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
	userUUID, ok := credstore.CallerUserID(cred)
	if !ok {
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
	in, msg := parsePatchBody(body)
	if msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	hintChange := userprefs.HintChange{Reset: in.ResetHints, Dismissed: in.DismissedHints}
	if err := hintChange.Validate(); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Snapshot prior state for audit-log diff (before-image).
	before, err := h.prefs.Get(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "load preferences before patch", err)
		return
	}
	hintsBefore, err := h.prefs.DismissedHints(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "load dismissed hints before patch", err)
		return
	}
	if len(in.Preferences) > 0 {
		if err := h.prefs.Upsert(ctx, tenantUUID, userUUID, in.Preferences); err != nil {
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
	}
	if err := h.prefs.ApplyHints(ctx, tenantUUID, userUUID, hintChange); err != nil {
		httperr.WriteInternal(w, r, "apply hint dismissals", err)
		return
	}
	after, err := h.prefs.Get(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "load preferences after patch", err)
		return
	}
	hintsAfter, err := h.prefs.DismissedHints(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "load dismissed hints after patch", err)
		return
	}
	// Audit-log: only the cells the patch touched, and only the hints whose
	// state changed. One row per PATCH, none when nothing changed.
	auditBefore := map[string]any{}
	auditAfter := map[string]any{}
	for ev, channels := range in.Preferences {
		for ch := range channels {
			auditBefore[ev+"."+ch] = before[ev][ch]
			auditAfter[ev+"."+ch] = after[ev][ch]
		}
	}
	for _, m := range userprefs.HintModules {
		was, now := slices.Contains(hintsBefore, m), slices.Contains(hintsAfter, m)
		if was != now {
			auditBefore["hint_dismissed."+m] = was
			auditAfter["hint_dismissed."+m] = now
		}
	}
	if !mapsEqual(auditBefore, auditAfter) {
		// Best-effort audit write — see ProfileHandler.writeAuditLog for the
		// non-fatal failure rationale.
		ah := &ProfileHandler{pool: h.pool}
		_ = ah.writeAuditLog(ctx, tenantUUID, userUUID, "preferences.update", auditBefore, auditAfter)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"preferences":     preferencesWire(after),
		"dismissed_hints": hintsAfter,
	})
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
