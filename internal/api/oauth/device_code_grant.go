// device_code_grant.go implements the RFC 8628 §3.4 device-code
// redemption path on the slice-188 `/oauth/token` endpoint.
//
// The CLI's `atlas login` polls `/oauth/token` with
// `grant_type=urn:ietf:params:oauth:grant-type:device_code` until
// the user approves the code in the browser. This file owns:
//
//   - The polling-rate enforcement (RFC 8628 §3.5 `slow_down`): each
//     device_code's poll interval is enforced with a per-code
//     last-poll timestamp + a 5-second floor (DevicePollInterval).
//   - The redemption logic: one-shot via
//     DeviceCodeStore.Consume (P0-191-8).
//   - The error mapping per RFC 8628 §3.5: `authorization_pending`,
//     `slow_down`, `access_denied`, `expired_token`, `invalid_grant`.
//   - The JWT mint with the approving user's identity snapshot.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-191-8 (one-shot redemption): DeviceCodeStore.Consume runs
//     an UPDATE ... RETURNING with `consumed_at IS NULL` in the
//     WHERE clause — the SQL layer guarantees only one transaction
//     wins.
//   - P0-188-4 (no super_admin elevation): the approving user's
//     `super_admin` flag is copied from the approval snapshot, not
//     synthesized at redemption time.
package oauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
)

// AttachDeviceCodeStore wires the slice-191 device-code grant
// handler onto the TokenEndpoint. Called from cmd/atlas/main.go at
// startup AFTER NewTokenEndpoint. The store is also passed to the
// device-authorization endpoint so both share the same DB-backed
// projection.
func (t *TokenEndpoint) AttachDeviceCodeStore(codes *DeviceCodeStore) {
	t.deviceCodes = codes
	if t.devicePoll == nil {
		t.devicePoll = newDevicePollTracker(t.now)
	}
}

// handleDeviceCode redeems an oauth_device_codes row, validates the
// approval snapshot, and mints a JWT scoped to the approving user.
//
// RFC 8628 §3.5 error mapping:
//
//   - device_code unknown / consumed / client_id mismatch -> invalid_grant
//   - approved_at IS NULL -> authorization_pending
//   - expires_at <= now -> expired_token
//   - poll interval violation -> slow_down (429 + Retry-After header)
func (t *TokenEndpoint) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if t.deviceCodes == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error",
			"device_code grant not configured")
		return
	}
	deviceCode := r.FormValue("device_code")
	clientID := r.FormValue("client_id")
	if deviceCode == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"device_code and client_id are required")
		return
	}

	// RFC 8628 §3.5 `slow_down`: enforce the 5-second minimum
	// between polls keyed on device_code. The first poll always
	// passes; subsequent polls within the interval window get 429 +
	// `slow_down` so the CLI can back off.
	now := t.now()
	if !t.devicePoll.Allow(deviceCode, now) {
		w.Header().Set("Retry-After", "5")
		writeOAuthError(w, http.StatusTooManyRequests, "slow_down",
			"polling too frequently; back off to the indicated interval")
		return
	}

	// Atomic consume — only one redemption wins per device_code.
	row, err := t.deviceCodes.Consume(r.Context(), deviceCode, now)
	if err != nil {
		switch {
		case errors.Is(err, ErrDeviceCodePending):
			writeOAuthError(w, http.StatusBadRequest, "authorization_pending",
				"the user has not yet approved the device code")
			return
		case errors.Is(err, ErrDeviceCodeExpired):
			writeOAuthError(w, http.StatusBadRequest, "expired_token",
				"the device code has expired")
			return
		case errors.Is(err, ErrDeviceCodeConsumed),
			errors.Is(err, ErrDeviceCodeNotFound):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"the device code is invalid")
			return
		default:
			writeOAuthError(w, http.StatusInternalServerError, "server_error",
				"device code redemption failed")
			return
		}
	}

	// P0-191-1 implicit / RFC 8628 §3.4: the client_id MUST match
	// the value used at initiation. The row tracks the initiating
	// client_id; a mismatch here means the CLI is using the wrong
	// credentials — invalid_grant per RFC 6749 §5.2.
	if row.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
			"the device code was issued to a different client")
		return
	}

	claims, err := buildDeviceCodeClaims(t.issuer, row, now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to build claims from approval snapshot")
		return
	}
	tok, signErr := t.signer.Sign(r.Context(), claims)
	if signErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to sign token")
		return
	}

	writeTokenResponse(w, tok)
}

// buildDeviceCodeClaims projects the approval snapshot stored on
// the DeviceCodeRow into a jwt.AtlasClaims. Mirrors
// buildAtlasClaimsForUser in pkce.go; the difference is the
// approval-snapshot fields use the device-code-table column names.
func buildDeviceCodeClaims(issuer string, row *DeviceCodeRow, now time.Time) (jwt.AtlasClaims, error) {
	if row.ApprovedByUserID == nil {
		return jwt.AtlasClaims{}, errors.New("approval snapshot missing user_id")
	}
	userID, err := uuid.Parse(*row.ApprovedByUserID)
	if err != nil {
		return jwt.AtlasClaims{}, err
	}
	var idpIssuer string
	if row.ApprovedByIDPIssuer != nil {
		idpIssuer = *row.ApprovedByIDPIssuer
	}
	var currentTenant uuid.UUID
	if row.ApprovedByCurrentTenantID != nil {
		t, perr := uuid.Parse(*row.ApprovedByCurrentTenantID)
		if perr == nil {
			currentTenant = t
		}
	}
	available := make([]uuid.UUID, 0, len(row.ApprovedByAvailableTenants))
	for _, raw := range row.ApprovedByAvailableTenants {
		u, perr := uuid.Parse(raw)
		if perr == nil {
			available = append(available, u)
		}
	}
	roles := map[uuid.UUID][]string{}
	if len(row.ApprovedByRoles) > 0 {
		var raw map[string][]string
		if jerr := json.Unmarshal(row.ApprovedByRoles, &raw); jerr == nil {
			for k, v := range raw {
				if u, perr := uuid.Parse(k); perr == nil {
					roles[u] = v
				}
			}
		}
	}
	superAdmin := false
	if row.ApprovedBySuperAdmin != nil {
		superAdmin = *row.ApprovedBySuperAdmin
	}
	return jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   "user:" + userID.String(),
			Audience:  []string{issuer},
			ExpiresAt: now.Add(AccessTokenLifetime).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        idpIssuer,
		CurrentTenantID:  currentTenant,
		AvailableTenants: available,
		Roles:            roles,
		SuperAdmin:       superAdmin,
	}, nil
}

// devicePollTracker enforces the RFC 8628 §3.5 5-second poll floor
// per device_code. Bounded by entries-expire-with-device-code-TTL.
type devicePollTracker struct {
	mu        sync.Mutex
	lastPoll  map[string]time.Time
	now       func() time.Time
	minWindow time.Duration
}

func newDevicePollTracker(now func() time.Time) *devicePollTracker {
	if now == nil {
		now = time.Now
	}
	return &devicePollTracker{
		lastPoll:  make(map[string]time.Time),
		now:       now,
		minWindow: time.Duration(DevicePollInterval) * time.Second,
	}
}

// Allow returns true iff at least DevicePollInterval seconds have
// elapsed since the last successful poll for device_code, OR no
// prior poll has been recorded. First poll always passes (RFC 8628
// §3.5: the CLI's first poll is allowed immediately).
func (p *devicePollTracker) Allow(deviceCode string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if last, ok := p.lastPoll[deviceCode]; ok {
		if now.Sub(last) < p.minWindow {
			return false
		}
	}
	p.lastPoll[deviceCode] = now
	return true
}
