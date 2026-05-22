// pkce.go — RFC 7636 PKCE helpers + supporting primitives the slice
// 189 authorize + token endpoints share.
//
// The single PKCE method supported is S256 (per P0-189-1):
//
//   code_challenge = base64url-without-padding(sha256(code_verifier))
//
// Comparison MUST be constant-time to prevent timing oracles on the
// stored challenge.

package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// computePKCEChallengeS256 computes the RFC 7636 §4.2 S256 challenge
// from a code verifier. Returns the URL-safe base64 (no padding)
// SHA-256 hash of the UTF-8 verifier bytes.
func computePKCEChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// constantTimeEqualString wraps subtle.ConstantTimeCompare with a
// string-friendly signature. Returns true iff the two strings have
// equal length AND equal byte content.
func constantTimeEqualString(a, b string) bool {
	if len(a) != len(b) {
		// Length comparison is necessarily not constant-time across
		// inputs of differing length, but the lengths of PKCE
		// challenges are fixed (43 chars for S256). This guard
		// catches malformed inputs before subtle.ConstantTimeCompare,
		// which requires equal-length inputs.
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// buildAtlasClaimsForUser builds the JWT claim set for a user-mode
// token minted from an authorization_code redemption. Mirrors the
// shape slice 187 + slice 188 already produce; the difference is
// that user-mode claims have a `sub` of `user:<uuid>` (vs slice 188's
// `oauth_client:<id>`) and carry the user's tenant scope.
func buildAtlasClaimsForUser(issuer string, ac oauthcode.AuthCode, roles map[uuid.UUID][]string, now time.Time) jwt.AtlasClaims {
	if roles == nil {
		roles = map[uuid.UUID][]string{}
	}
	available := ac.AvailableTenants
	if available == nil {
		available = []uuid.UUID{}
	}
	return jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   "user:" + ac.UserID.String(),
			Audience:  []string{issuer},
			ExpiresAt: now.Add(AccessTokenLifetime).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        ac.IDPIssuer,
		CurrentTenantID:  ac.CurrentTenantID,
		AvailableTenants: available,
		Roles:            roles,
		SuperAdmin:       ac.SuperAdmin,
	}
}

// writeAuthCodeAudit writes one row to oauth_token_exchanges
// capturing the authorization_code redemption. Per D2 we reuse the
// slice-188 audit table; from_tenant_id is NULL (initial mint, no
// prior tenant), to_tenant_id is the user's current tenant.
//
// Best-effort: a failure here does NOT block the token response —
// same discipline as the token-exchange path.
func (t *TokenEndpoint) writeAuthCodeAudit(r interface{ Context() context.Context }, ac oauthcode.AuthCode) {
	if t.auditPool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tenantCtx, err := tenancy.WithTenant(ctx, ac.CurrentTenantID.String())
	if err != nil {
		return
	}
	tx, err := t.auditPool.BeginTx(tenantCtx, pgx.TxOptions{})
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()

	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		return
	}
	ctx = tenantCtx

	// Marshal a stable forensic identifier — we don't have the JWT's
	// jti yet (we're about to mint it), so we use the auth code as the
	// jti surrogate. The redeemed code is one-shot anyway, so it's a
	// unique forensic key.
	jti := ac.Code
	if len(jti) > 64 {
		jti = jti[:64] // safety: the audit column is unbounded, but keep it tidy
	}

	const q = `
		INSERT INTO oauth_token_exchanges (
			id, tenant_id, subject_token_jti, from_tenant_id, to_tenant_id,
			subject_token_iss, subject_token_sub, ip_address
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	if _, err := tx.Exec(ctx, q,
		uuid.New(),
		ac.CurrentTenantID,
		jti,
		nil, // from_tenant_id NULL — initial mint, no prior tenant
		ac.CurrentTenantID,
		ac.IDPIssuer,
		"user:"+ac.UserID.String(),
		nil, // ip_address NULL in best-effort audit — slice 190 audit will tighten
	); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// (rolesJSONFromBytes removed — unused; the inline deserialization in
// handleAuthorizationCode is the single use site.)
