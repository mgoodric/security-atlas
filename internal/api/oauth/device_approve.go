// device_approve.go implements the slice-191 internal approval
// endpoints `POST /oauth/device_authorization/approve` and
// `POST /oauth/device_authorization/deny`.
//
// These endpoints are NOT in RFC 8628 — they are the atlas-internal
// hooks the slice-191 device approval UI (web/app/oauth/device/page.tsx)
// posts to after the OIDC-authenticated user approves or denies a
// device code. The approve handler writes the OIDC session's
// identity snapshot onto the oauth_device_codes row; the redemption
// path (handleDeviceCode in device_code_grant.go) then mints a JWT
// inheriting that snapshot.
//
// AUTHENTICATION:
//
// Both endpoints require an authenticated OIDC session — same
// authentication discipline as every other `/v1/*` route except the
// snapshot is taken from the slice-034 session (the JWT middleware
// would NOT yet have fired because the user authenticates via the
// legacy OIDC RP, and the OAuth AS mint hasn't happened yet — the
// device code IS the OAuth AS mint trigger).
//
// In v1 (slice 191) we accept the slice-034 bearer token (issued by
// the OIDC RP after login) presented as `Authorization: Bearer ...`
// — the platform's bearer middleware has already authenticated the
// session, set the credential on the request context, and we read
// it back here. v2+ may migrate this surface to a session cookie
// when the frontend BFF gains the matching cookie path.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - Snapshot-at-approval: the approving user's identity is
//     captured at the moment of approval, NOT at the moment of
//     redemption. A later mutation (e.g., admin removes user from
//     tenant) cannot retroactively change the JWT's scope. This is
//     the standard OAuth eventual-eviction shape (slice 190 R2).
//   - P0-188-4 (no super_admin elevation): the snapshot copies
//     super_admin from the verified identity; it cannot be set
//     true synthetically.
package oauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
)

// DeviceApprovalEndpoint owns the dependencies for the approve and
// deny handlers. Construction goes through NewDeviceApprovalEndpoint
// because both handlers share the same store + clock.
type DeviceApprovalEndpoint struct {
	codes *DeviceCodeStore
	now   func() time.Time
}

// DeviceApprovalConfig is the constructor bag.
type DeviceApprovalConfig struct {
	Now func() time.Time
}

// NewDeviceApprovalEndpoint constructs the approval endpoint.
func NewDeviceApprovalEndpoint(codes *DeviceCodeStore, cfg DeviceApprovalConfig) *DeviceApprovalEndpoint {
	if codes == nil {
		panic("oauth: NewDeviceApprovalEndpoint: codes is nil")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &DeviceApprovalEndpoint{codes: codes, now: nowFn}
}

// approveRequest is the JSON body shape the slice-191 approval UI
// posts. The OIDC session identifies the approver server-side; the
// body only carries the user_code to be approved.
type approveRequest struct {
	UserCode string `json:"user_code"`
}

// approveResponse acknowledges a successful approval with a small
// shape so the frontend can render a "device approved" view.
type approveResponse struct {
	Status   string `json:"status"`
	UserCode string `json:"user_code"`
}

// ServeApprove handles `POST /oauth/device_authorization/approve`.
func (e *DeviceApprovalEndpoint) ServeApprove(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token",
			"authentication required to approve a device code")
		return
	}
	body, ok := decodeApproveRequest(w, r)
	if !ok {
		return
	}
	if body.UserCode == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"user_code is required")
		return
	}

	// Build the snapshot from the credential carried on the
	// request context. The credential's UserID is the
	// atlas-internal user UUID; idp_issuer + idp_subject are not
	// yet exposed on credstore.Credential, so v1 records a
	// fixed marker (MachineIDPIssuer) and an empty subject. v2+
	// will surface real OIDC metadata when the slice-034
	// session model is extended.
	in := ApproveInput{
		UserCode:        body.UserCode,
		UserID:          cred.UserID,
		IDPIssuer:       MachineIDPIssuer,
		IDPSubject:      "",
		CurrentTenantID: cred.TenantID,
		SuperAdmin:      cred.IsAdmin,
	}
	// AvailableTenants + Roles for v1: the slice-034 credential is
	// single-tenant, so we mirror that into the snapshot as a
	// single-element list. v2+ multi-tenant credentials will fan
	// these out from the real grants table.
	if cred.TenantID != "" {
		in.AvailableTenants = []string{cred.TenantID}
		roles := map[string][]string{cred.TenantID: cred.OwnerRoles}
		if rolesJSON, err := json.Marshal(roles); err == nil {
			in.RolesJSON = rolesJSON
		}
	}

	if err := e.codes.Approve(r.Context(), in, e.now()); err != nil {
		switch {
		case errors.Is(err, ErrDeviceCodeNotFound):
			writeOAuthError(w, http.StatusNotFound, "invalid_request",
				"device code not found")
			return
		case errors.Is(err, ErrDeviceCodeExpired):
			writeOAuthError(w, http.StatusBadRequest, "expired_token",
				"the device code has expired")
			return
		case errors.Is(err, ErrDeviceCodeConsumed):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"the device code has already been used or denied")
			return
		default:
			writeOAuthError(w, http.StatusInternalServerError, "server_error",
				"failed to approve device code")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(approveResponse{
		Status:   "approved",
		UserCode: body.UserCode,
	})
}

// ServeDeny handles `POST /oauth/device_authorization/deny`. Sets
// the row's consumed_at so the CLI's subsequent poll receives
// invalid_grant.
func (e *DeviceApprovalEndpoint) ServeDeny(w http.ResponseWriter, r *http.Request) {
	if _, ok := authctx.CredentialFromContext(r.Context()); !ok {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token",
			"authentication required to deny a device code")
		return
	}
	body, ok := decodeApproveRequest(w, r)
	if !ok {
		return
	}
	if body.UserCode == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"user_code is required")
		return
	}
	if err := e.codes.Deny(r.Context(), body.UserCode, e.now()); err != nil {
		if errors.Is(err, ErrDeviceCodeNotFound) {
			writeOAuthError(w, http.StatusNotFound, "invalid_request",
				"device code not found")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to deny device code")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(approveResponse{
		Status:   "denied",
		UserCode: body.UserCode,
	})
}

// decodeApproveRequest decodes the JSON body and writes the error
// response on failure. Returns (body, true) on success; (zero,
// false) when the response has already been written.
func decodeApproveRequest(w http.ResponseWriter, r *http.Request) (approveRequest, bool) {
	var body approveRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"request body must be valid JSON")
		return approveRequest{}, false
	}
	return body, true
}
