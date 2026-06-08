// Slice 543: per-user master opt-in toggles for the Slack + generic-webhook
// notification delivery channels.
//
//	GET/PUT /v1/me/slack-channel
//	GET/PUT /v1/me/webhook-channel
//
// Default is OPTED-OUT (P0-543-3): a user with no opt-in row reads
// enabled=false. Each toggle ONLY affects the caller's own delivery — the
// tenant + user come from the authenticated context, so there is no path to
// configure another user (threat-model E) and no user-controlled target
// (P0-543-2 — the channel target is operator-configured env, not stored
// per-user). These mirror the slice-445 EmailChannelHandler shape.
package me

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// channelOptInWire is the JSON shape for GET and PUT on a channel toggle.
type channelOptInWire struct {
	Enabled bool `json:"enabled"`
}

// ChannelOptInHandler bundles GET/PUT for one channel's opt-in toggle. It
// is parameterized over the channel's get/set funcs so Slack + webhook
// share one handler implementation (no duplication).
type ChannelOptInHandler struct {
	label string
	get   func(ctx context.Context, tenantID, userID uuid.UUID) (bool, error)
	set   func(ctx context.Context, tenantID, userID uuid.UUID, enabled bool) error
}

// NewChannelOptIn constructs a handler from a channel's get/set funcs.
// label names the channel for error strings (e.g. "slack", "webhook").
func NewChannelOptIn(
	label string,
	get func(ctx context.Context, tenantID, userID uuid.UUID) (bool, error),
	set func(ctx context.Context, tenantID, userID uuid.UUID, enabled bool) error,
) *ChannelOptInHandler {
	return &ChannelOptInHandler{label: label, get: get, set: set}
}

// Get returns the caller's master opt-in state; default false (P0-543-3).
func (h *ChannelOptInHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	tenantUUID, userUUID, ok := parseCredIDs(w, cred)
	if !ok {
		return
	}
	enabled, err := h.get(ctx, tenantUUID, userUUID)
	if err != nil {
		httperr.WriteInternal(w, r, "get "+h.label+"-channel opt-in", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, channelOptInWire{Enabled: enabled})
}

// Put sets the caller's master opt-in. Tenant + user come from the
// authenticated context only (P0-543-2 / E).
func (h *ChannelOptInHandler) Put(w http.ResponseWriter, r *http.Request) {
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
	var in channelOptInWire
	if err := json.Unmarshal(body, &in); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.set(ctx, tenantUUID, userUUID, in.Enabled); err != nil {
		httperr.WriteInternal(w, r, "set "+h.label+"-channel opt-in", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, channelOptInWire{Enabled: in.Enabled})
}
