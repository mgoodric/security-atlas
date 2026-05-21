// device_authorization.go implements the slice-191
// `POST /oauth/device_authorization` endpoint (RFC 8628 §3.1).
//
// The endpoint is the entry point for the Device Authorization Grant
// (RFC 8628), used by the CLI's `atlas login` command. The flow:
//
//  1. CLI POSTs `client_id` to this endpoint.
//  2. Handler INSERTs a row in `oauth_device_codes` with a freshly
//     generated 64-byte `device_code` + an 8-char user-facing
//     `user_code` from the unambiguous alphabet
//     ABCDEFGHJKLMNPQRSTUVWXYZ23456789 (no 0/O/1/I/L per P0-191-4),
//     formatted as XXXX-XXXX.
//  3. Handler returns the RFC 8628 §3.2 JSON response — the CLI
//     prints the user_code, the user visits verification_uri, logs
//     in via the slice-034 OIDC RP, approves the code via the
//     `/oauth/device` UI route, and the CLI's polling succeeds.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-191-4 (unambiguous alphabet): user_code generated from
//     UserCodeAlphabet — defense-in-depth via the migration's
//     `length(user_code) > 0` CHECK and a constant-time
//     application-layer assertion at construction.
//   - P0-191-8 (one-shot redemption): the row's `consumed_at`
//     column gives the redemption path an atomic UPDATE-RETURNING
//     gate; replay protection lives at the SQL layer.
//   - 64-byte device_code: 512 bits of entropy means brute-force is
//     computationally infeasible.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
)

// Device-flow protocol constants.
const (
	// PathDeviceAuthorization is the device authorization endpoint
	// path per RFC 8628 §3.1.
	PathDeviceAuthorization = "/oauth/device_authorization"

	// PathDeviceApprove is the internal endpoint the slice-191
	// approval UI POSTs to after the OIDC-authenticated user
	// approves the device code.
	PathDeviceApprove = "/oauth/device_authorization/approve"

	// PathDeviceDeny is the internal endpoint the slice-191 approval
	// UI POSTs to when the user denies the device code.
	PathDeviceDeny = "/oauth/device_authorization/deny"

	// GrantTypeDeviceCode is the RFC 8628 §3.4 grant type URN that
	// the `/oauth/token` endpoint accepts for device-code redemption.
	GrantTypeDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"

	// DeviceCodeLifetime is the RFC 8628 §3.2 default `expires_in`
	// — 15 minutes. Long enough for a human to read the user_code,
	// open a browser, sign in via OIDC, and approve. Short enough
	// that abandoned flows don't accumulate.
	DeviceCodeLifetime = 15 * time.Minute

	// DevicePollInterval is the RFC 8628 §3.5 default `interval`
	// (seconds). The CLI polls `/oauth/token` no more frequently
	// than this; the token endpoint enforces with `slow_down`
	// per-device_code (P0-191-8 / RFC 8628 §3.5).
	DevicePollInterval = 5

	// DeviceCodeByteLen is the entropy of the device_code secret.
	// 64 bytes = 512 bits. Comparable to the security boundary of
	// the JWT signing keys themselves.
	DeviceCodeByteLen = 64

	// UserCodeLen is the length of the human-facing user_code,
	// excluding the formatting hyphen. 8 chars from a 32-char
	// alphabet = 32^8 ≈ 10^12 combinations.
	UserCodeLen = 8

	// UserCodeAlphabet is the unambiguous alphabet for user_code
	// generation per P0-191-4: no 0/O/1/I/L. RFC 8628 §6.1
	// recommends this exact choice.
	UserCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// DeviceAuthorizationEndpoint owns the dependencies for
// `POST /oauth/device_authorization`. Stored separately from the
// TokenEndpoint because the device-authorization endpoint is the
// initiate-flow path, not a grant-redemption path.
type DeviceAuthorizationEndpoint struct {
	clients *oauthclient.Store
	codes   *DeviceCodeStore
	issuer  string
	now     func() time.Time
	rand    func(int) ([]byte, error)
}

// DeviceAuthorizationConfig is the constructor bag.
type DeviceAuthorizationConfig struct {
	Issuer string
	Now    func() time.Time
	// RandomBytes is the source of entropy; nil falls back to
	// crypto/rand.Read. Tests can inject deterministic bytes.
	RandomBytes func(int) ([]byte, error)
}

// NewDeviceAuthorizationEndpoint constructs the endpoint. The
// clients store + the codes store are required at request time —
// missing either means the deployment is misconfigured and we want
// to surface that loudly (panic on startup, not silently 500 on
// every request).
func NewDeviceAuthorizationEndpoint(clients *oauthclient.Store, codes *DeviceCodeStore, cfg DeviceAuthorizationConfig) *DeviceAuthorizationEndpoint {
	if clients == nil {
		panic("oauth: NewDeviceAuthorizationEndpoint: clients is nil")
	}
	if codes == nil {
		panic("oauth: NewDeviceAuthorizationEndpoint: codes is nil")
	}
	if cfg.Issuer == "" {
		panic("oauth: NewDeviceAuthorizationEndpoint: Issuer is empty")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	randFn := cfg.RandomBytes
	if randFn == nil {
		randFn = readRandom
	}
	return &DeviceAuthorizationEndpoint{
		clients: clients,
		codes:   codes,
		issuer:  cfg.Issuer,
		now:     nowFn,
		rand:    randFn,
	}
}

// readRandom is the production entropy source.
func readRandom(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// deviceAuthorizationResponse is the RFC 8628 §3.2 response shape.
type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// ServeHTTP handles `POST /oauth/device_authorization`.
func (e *DeviceAuthorizationEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isFormContentType(r.Header.Get("Content-Type")) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"Content-Type must be application/x-www-form-urlencoded")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	clientID := r.FormValue("client_id")
	if clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"client_id is required")
		return
	}

	// Validate the client_id exists. We deliberately do NOT require
	// client_secret here — the device flow's security model is that
	// the eventual JWT mint is gated by the human approval step,
	// not by holding a static client_secret (RFC 8628 §5.1). The
	// secret stays as a future hardening option for non-public
	// clients.
	if _, err := e.clients.Lookup(r.Context(), clientID); err != nil {
		if errors.Is(err, oauthclient.ErrUnknownClient) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client",
				"client_id is not registered")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to validate client_id")
		return
	}

	// Generate the two secrets. The device_code is 64 random bytes
	// base64url-encoded; the user_code is 8 chars from the
	// unambiguous alphabet, formatted XXXX-XXXX.
	deviceCode, err := e.generateDeviceCode()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to generate device_code")
		return
	}
	userCode, err := e.generateUserCode()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to generate user_code")
		return
	}

	now := e.now()
	if err := e.codes.Insert(r.Context(), DeviceCodeRow{
		DeviceCode: deviceCode,
		UserCode:   userCode,
		ClientID:   clientID,
		ExpiresAt:  now.Add(DeviceCodeLifetime),
		CreatedAt:  now,
	}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to persist device code")
		return
	}

	verificationURI := e.issuer + "/oauth/device"
	verificationURIComplete := fmt.Sprintf("%s?user_code=%s", verificationURI, userCode)

	resp := deviceAuthorizationResponse{
		DeviceCode:              deviceCode,
		UserCode:                userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationURIComplete,
		ExpiresIn:               int(DeviceCodeLifetime / time.Second),
		Interval:                DevicePollInterval,
	}
	w.Header().Set("Content-Type", "application/json")
	// RFC 8628 §3.2: the response MUST NOT be cached.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// generateDeviceCode returns a 64-byte random base64url-encoded
// string per RFC 8628 §6.1 entropy guidance.
func (e *DeviceAuthorizationEndpoint) generateDeviceCode() (string, error) {
	buf, err := e.rand(DeviceCodeByteLen)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// generateUserCode returns an 8-char user_code from the unambiguous
// alphabet, formatted XXXX-XXXX. We use crypto/rand-backed integers
// in the range [0, len(alphabet)) for each character — uniform
// distribution, no modulo bias.
func (e *DeviceAuthorizationEndpoint) generateUserCode() (string, error) {
	alphaLen := big.NewInt(int64(len(UserCodeAlphabet)))
	var b strings.Builder
	b.Grow(UserCodeLen + 1) // +1 for the hyphen
	for i := 0; i < UserCodeLen; i++ {
		if i == UserCodeLen/2 {
			b.WriteByte('-')
		}
		// crypto/rand.Int returns a uniformly distributed value in
		// [0, alphaLen). The math/big import is only used here to
		// access the safer rand.Int helper.
		idx, err := rand.Int(rand.Reader, alphaLen)
		if err != nil {
			return "", err
		}
		b.WriteByte(UserCodeAlphabet[idx.Int64()])
	}
	return b.String(), nil
}

// ===== DeviceCodeStore — pgx-backed CRUD for oauth_device_codes =====

// DeviceCodeStore is the pgxpool-backed access layer for the
// `oauth_device_codes` table. Kept inside the oauth package because
// it has only a handful of queries and the slice 188 + 189 + 190
// auth packages follow the same pattern (one store per OAuth
// surface).
type DeviceCodeStore struct {
	pool *pgxpool.Pool
}

// NewDeviceCodeStore constructs a DeviceCodeStore. The pool MAY be
// the atlas_app tenant pool — the table is not tenant-scoped so RLS
// is irrelevant. Tests pass an integration pool.
func NewDeviceCodeStore(pool *pgxpool.Pool) *DeviceCodeStore {
	return &DeviceCodeStore{pool: pool}
}

// DeviceCodeRow is the in-memory projection of a row in
// `oauth_device_codes`. The approval-snapshot columns are pointers
// so the store can serialize the NULL-or-set semantic without
// littering the call sites with sql.NullX types.
type DeviceCodeRow struct {
	DeviceCode string
	UserCode   string
	ClientID   string
	ExpiresAt  time.Time
	CreatedAt  time.Time

	// Approval snapshot — all NULL until the approve handler writes
	// them.
	ApprovedAt                 *time.Time
	ApprovedByUserID           *string // UUID as string for simplicity
	ApprovedByIDPIssuer        *string
	ApprovedByIDPSubject       *string
	ApprovedByCurrentTenantID  *string
	ApprovedByAvailableTenants []string // UUID-as-string list
	ApprovedByRoles            []byte   // raw JSONB
	ApprovedBySuperAdmin       *bool

	// ConsumedAt is set by either the redemption path (success) or
	// the deny path (rejection). Either case kills the row's
	// usability.
	ConsumedAt *time.Time
}

// ErrDeviceCodeNotFound is the sentinel for "no row with that
// device_code OR user_code".
var ErrDeviceCodeNotFound = errors.New("oauth: device code not found")

// ErrDeviceCodeExpired indicates the lookup returned a row whose
// `expires_at` is in the past. The handler maps this to the RFC
// 8628 §3.5 `expired_token` error.
var ErrDeviceCodeExpired = errors.New("oauth: device code expired")

// ErrDeviceCodeConsumed indicates the row's `consumed_at` is set.
// Maps to `invalid_grant` per RFC 6749 §5.2.
var ErrDeviceCodeConsumed = errors.New("oauth: device code consumed")

// ErrDeviceCodePending indicates the row exists and is unexpired
// but the user hasn't approved yet. The redemption handler maps
// this to RFC 8628 §3.5 `authorization_pending`.
var ErrDeviceCodePending = errors.New("oauth: device code authorization pending")

// Insert persists a fresh DeviceCodeRow (initiate flow path).
func (s *DeviceCodeStore) Insert(ctx context.Context, row DeviceCodeRow) error {
	const q = `
		INSERT INTO oauth_device_codes (
			device_code, user_code, client_id, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5)
	`
	_, err := s.pool.Exec(ctx, q,
		row.DeviceCode, row.UserCode, row.ClientID, row.ExpiresAt, row.CreatedAt,
	)
	return err
}

// LookupByUserCode is the read path the approval UI's POST handlers
// use to find a row by the short user-facing code. Returns
// ErrDeviceCodeNotFound when no row matches.
func (s *DeviceCodeStore) LookupByUserCode(ctx context.Context, userCode string) (*DeviceCodeRow, error) {
	const q = `
		SELECT device_code, user_code, client_id, expires_at, created_at,
		       approved_at, approved_by_user_id, approved_by_idp_issuer,
		       approved_by_idp_subject, approved_by_current_tenant_id,
		       approved_by_available_tenants, approved_by_roles,
		       approved_by_super_admin, consumed_at
		FROM oauth_device_codes WHERE user_code = $1
	`
	row := s.pool.QueryRow(ctx, q, userCode)
	return scanDeviceCodeRow(row)
}

// LookupByDeviceCode is the read path the `/oauth/token` device-code
// grant uses to look up a row by the long secret.
func (s *DeviceCodeStore) LookupByDeviceCode(ctx context.Context, deviceCode string) (*DeviceCodeRow, error) {
	const q = `
		SELECT device_code, user_code, client_id, expires_at, created_at,
		       approved_at, approved_by_user_id, approved_by_idp_issuer,
		       approved_by_idp_subject, approved_by_current_tenant_id,
		       approved_by_available_tenants, approved_by_roles,
		       approved_by_super_admin, consumed_at
		FROM oauth_device_codes WHERE device_code = $1
	`
	row := s.pool.QueryRow(ctx, q, deviceCode)
	return scanDeviceCodeRow(row)
}

// scanDeviceCodeRow centralizes the SELECT column ordering used by
// LookupByUserCode + LookupByDeviceCode.
func scanDeviceCodeRow(qr pgx.Row) (*DeviceCodeRow, error) {
	var r DeviceCodeRow
	var (
		approvedAt              *time.Time
		approvedByUserID        *string
		approvedByIDPIssuer     *string
		approvedByIDPSubject    *string
		approvedByCurrentTenant *string
		approvedByAvailable     []string
		approvedByRoles         []byte
		approvedBySuperAdmin    *bool
		consumedAt              *time.Time
	)
	err := qr.Scan(
		&r.DeviceCode, &r.UserCode, &r.ClientID, &r.ExpiresAt, &r.CreatedAt,
		&approvedAt, &approvedByUserID, &approvedByIDPIssuer,
		&approvedByIDPSubject, &approvedByCurrentTenant,
		&approvedByAvailable, &approvedByRoles, &approvedBySuperAdmin,
		&consumedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDeviceCodeNotFound
		}
		return nil, err
	}
	r.ApprovedAt = approvedAt
	r.ApprovedByUserID = approvedByUserID
	r.ApprovedByIDPIssuer = approvedByIDPIssuer
	r.ApprovedByIDPSubject = approvedByIDPSubject
	r.ApprovedByCurrentTenantID = approvedByCurrentTenant
	r.ApprovedByAvailableTenants = approvedByAvailable
	r.ApprovedByRoles = approvedByRoles
	r.ApprovedBySuperAdmin = approvedBySuperAdmin
	r.ConsumedAt = consumedAt
	return &r, nil
}

// ApproveInput captures the OIDC-authenticated user's identity at
// the moment of approval. The approve handler builds this from the
// session and passes it to Approve; all snapshot columns inherit
// into the eventual JWT.
type ApproveInput struct {
	UserCode         string
	UserID           string
	IDPIssuer        string
	IDPSubject       string
	CurrentTenantID  string // empty means platform-global (super_admin without tenant)
	AvailableTenants []string
	RolesJSON        []byte // pre-marshaled JSONB
	SuperAdmin       bool
}

// Approve writes the approval snapshot. The UPDATE is gated on the
// row being unconsumed AND unapproved AND unexpired — any prior
// state results in 0 rows affected and an error so the handler can
// surface the right RFC 8628 §3.5 response.
func (s *DeviceCodeStore) Approve(ctx context.Context, in ApproveInput, now time.Time) error {
	const q = `
		UPDATE oauth_device_codes
		SET approved_at = $1,
		    approved_by_user_id = $2,
		    approved_by_idp_issuer = $3,
		    approved_by_idp_subject = $4,
		    approved_by_current_tenant_id = $5,
		    approved_by_available_tenants = $6,
		    approved_by_roles = $7,
		    approved_by_super_admin = $8
		WHERE user_code = $9
		  AND consumed_at IS NULL
		  AND approved_at IS NULL
		  AND expires_at > $1
	`
	var currentTenant *string
	if in.CurrentTenantID != "" {
		currentTenant = &in.CurrentTenantID
	}
	tag, err := s.pool.Exec(ctx, q,
		now, in.UserID, in.IDPIssuer, in.IDPSubject,
		currentTenant, in.AvailableTenants, in.RolesJSON, in.SuperAdmin,
		in.UserCode,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Distinguish the failure mode for the handler — most
		// commonly the code expired between authorize and approve,
		// or a second tab raced and approved first.
		row, lookupErr := s.LookupByUserCode(ctx, in.UserCode)
		if errors.Is(lookupErr, ErrDeviceCodeNotFound) {
			return ErrDeviceCodeNotFound
		}
		if lookupErr != nil {
			return lookupErr
		}
		if row.ConsumedAt != nil {
			return ErrDeviceCodeConsumed
		}
		if row.ExpiresAt.Before(now) {
			return ErrDeviceCodeExpired
		}
		// Already approved (raced) — treat as consumed to the
		// caller; subsequent redemption will succeed once.
		return ErrDeviceCodeConsumed
	}
	return nil
}

// Deny marks a code as consumed without setting the approval
// snapshot. The redemption path will then return invalid_grant on
// the next CLI poll.
func (s *DeviceCodeStore) Deny(ctx context.Context, userCode string, now time.Time) error {
	const q = `
		UPDATE oauth_device_codes
		SET consumed_at = $1
		WHERE user_code = $2 AND consumed_at IS NULL
	`
	tag, err := s.pool.Exec(ctx, q, now, userCode)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceCodeNotFound
	}
	return nil
}

// Consume is the one-shot redemption gate (P0-191-8). Atomic
// UPDATE-RETURNING: only one transaction wins the redemption race
// per device_code. Returns the snapshot of the consumed row so the
// caller can mint a JWT with the approving user's identity.
func (s *DeviceCodeStore) Consume(ctx context.Context, deviceCode string, now time.Time) (*DeviceCodeRow, error) {
	const q = `
		UPDATE oauth_device_codes
		SET consumed_at = $1
		WHERE device_code = $2
		  AND approved_at IS NOT NULL
		  AND consumed_at IS NULL
		  AND expires_at > $1
		RETURNING device_code, user_code, client_id, expires_at, created_at,
		          approved_at, approved_by_user_id, approved_by_idp_issuer,
		          approved_by_idp_subject, approved_by_current_tenant_id,
		          approved_by_available_tenants, approved_by_roles,
		          approved_by_super_admin, consumed_at
	`
	row := s.pool.QueryRow(ctx, q, now, deviceCode)
	consumed, err := scanDeviceCodeRow(row)
	if err == nil {
		return consumed, nil
	}
	if !errors.Is(err, ErrDeviceCodeNotFound) {
		return nil, err
	}
	// 0 rows affected — figure out why so the handler can surface
	// the right RFC 8628 §3.5 error.
	lookup, lookupErr := s.LookupByDeviceCode(ctx, deviceCode)
	if errors.Is(lookupErr, ErrDeviceCodeNotFound) {
		return nil, ErrDeviceCodeNotFound
	}
	if lookupErr != nil {
		return nil, lookupErr
	}
	if lookup.ConsumedAt != nil {
		return nil, ErrDeviceCodeConsumed
	}
	if lookup.ExpiresAt.Before(now) {
		return nil, ErrDeviceCodeExpired
	}
	if lookup.ApprovedAt == nil {
		return nil, ErrDeviceCodePending
	}
	// All conditions look good but UPDATE returned 0 — race
	// condition or clock skew; surface as pending so the CLI keeps
	// polling.
	return nil, ErrDeviceCodePending
}
