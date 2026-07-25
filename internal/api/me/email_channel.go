// Slice 445: GET /v1/me/email-channel + PUT /v1/me/email-channel.
//
// The per-user master opt-in toggle for the email notification delivery
// channel (AC-9). Default is OPTED-OUT (P0-445-7): a user with no opt-in
// row reads enabled=false. The toggle ONLY affects the caller's own
// delivery (no path lets a user configure another user — threat-model E).
package me

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/notify/email"
)

// EmailChannelHandler bundles the GET/PUT /v1/me/email-channel routes over
// the slice-445 email.Channel.
type EmailChannelHandler struct {
	ch *email.Channel
	// configured reports whether the OPERATOR has configured the SMTP
	// delivery target via env (slice 585). Captured at construction from the
	// email Config.Enabled() presence check; a boolean PRESENCE signal only,
	// never the SMTP host/credentials (P0-585 / threat-model S).
	configured bool
}

// NewEmailChannel constructs an EmailChannelHandler. configured is the
// operator-config-presence boolean (see field doc).
func NewEmailChannel(ch *email.Channel, configured bool) *EmailChannelHandler {
	return &EmailChannelHandler{ch: ch, configured: configured}
}

// emailChannelWire is the JSON shape for both GET and PUT. Slice 585 adds
// `configured` (operator-config presence; never the SMTP secret).
type emailChannelWire struct {
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
}

// Get handles GET /v1/me/email-channel. Returns the caller's master opt-in
// state; default false (P0-445-7).
func (h *EmailChannelHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	tenantUUID, userUUID, ok := parseCredIDs(w, cred)
	if !ok {
		return
	}
	enabled, err := h.ch.GetEmailOptIn(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "get email-channel opt-in", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, emailChannelWire{Enabled: enabled, Configured: h.configured})
}

// Put handles PUT /v1/me/email-channel. Sets the caller's master opt-in.
// The tenant + user are taken from the authenticated context only — there
// is no path to set another user's opt-in (P0-445-1 / E).
func (h *EmailChannelHandler) Put(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	tenantUUID, userUUID, ok := parseCredIDs(w, cred)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}
	var in emailChannelWire
	if err := json.Unmarshal(body, &in); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.ch.SetEmailOptIn(ctx, tenantUUID, userUUID, in.Enabled); err != nil {
		httperr.WriteInternal(w, r, "set email-channel opt-in", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, emailChannelWire{Enabled: in.Enabled, Configured: h.configured})
}

// parseCredIDs resolves the (tenant, user) UUIDs from the credential,
// writing the appropriate error response on failure.
func parseCredIDs(w http.ResponseWriter, cred credstore.Credential) (tenantUUID, userUUID uuid.UUID, ok bool) {
	u, err := uuid.Parse(cred.UserID)
	if err != nil {
		httpresp.WriteError(w, http.StatusNotFound, "no email channel for this credential")
		return uuid.Nil, uuid.Nil, false
	}
	tn, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context invalid")
		return uuid.Nil, uuid.Nil, false
	}
	return tn, u, true
}
