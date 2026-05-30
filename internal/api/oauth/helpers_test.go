// helpers_test.go — slice 314 unit coverage for the OAuth AS pure
// helper functions reachable without Postgres.
//
// Covers: requestIP (token.go — audit-log IP parsing, RFC-agnostic
// forensic helper), DeviceAuthorizationEndpoint.generateDeviceCode +
// generateUserCode (RFC 8628 §6.1 secret entropy + unambiguous
// alphabet P0-191-4), readRandom (entropy source contract),
// buildAtlasClaimsForUser (RFC 6749 §4.1.3 user-mode claim
// projection), and DBUserResolver.ResolveForOAuth's nil-pool
// defensive default.

package oauth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
)

// TestRequestIP_Branches covers requestIP's three shapes: a
// host:port RemoteAddr yields the bare host; a bare host (no port)
// yields the host; an unparseable address yields nil (NULL on the
// INET audit column); a nil request yields nil.
func TestRequestIP_Branches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		remoteAddr string
		want       any
	}{
		{"host-port-v4", "192.0.2.10:54321", "192.0.2.10"},
		{"bare-host-v4", "192.0.2.10", "192.0.2.10"},
		{"unparseable", "not-an-ip", nil},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
			req.RemoteAddr = c.remoteAddr
			got := oauth.ExportRequestIP(req)
			if got != c.want {
				t.Errorf("requestIP(%q) = %v, want %v", c.remoteAddr, got, c.want)
			}
		})
	}
	// nil request → nil.
	if got := oauth.ExportRequestIP(nil); got != nil {
		t.Errorf("requestIP(nil) = %v, want nil", got)
	}
}

// newDeviceAuthEndpointForGen builds a DeviceAuthorizationEndpoint with
// a deterministic entropy source so the generators are testable in
// isolation (the DB-backed ServeHTTP path is gated behind a client
// lookup).
func newDeviceAuthEndpointForGen(t *testing.T, randBytes func(int) ([]byte, error)) *oauth.DeviceAuthorizationEndpoint {
	t.Helper()
	return oauth.NewDeviceAuthorizationEndpoint(
		oauthclient.New(nil),
		oauth.NewDeviceCodeStore(nil),
		oauth.DeviceAuthorizationConfig{
			Issuer:      testIssuer,
			Now:         pinnedNow,
			RandomBytes: randBytes,
		},
	)
}

// TestGenerateDeviceCode_ShapeAndEntropy covers RFC 8628 §6.1: the
// device_code is the base64url-no-padding encoding of 64 random bytes
// (512 bits). With a fixed entropy source the output is deterministic
// and free of base64 padding/standard-alphabet characters.
func TestGenerateDeviceCode_ShapeAndEntropy(t *testing.T) {
	t.Parallel()
	fixed := func(n int) ([]byte, error) {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i)
		}
		return b, nil
	}
	ep := newDeviceAuthEndpointForGen(t, fixed)
	dc, err := oauth.ExportGenerateDeviceCode(ep)
	if err != nil {
		t.Fatalf("generateDeviceCode: %v", err)
	}
	// 64 bytes → ceil(64*4/3) = 86 base64url chars (no padding).
	if len(dc) != 86 {
		t.Errorf("device_code length = %d, want 86 (64 bytes base64url)", len(dc))
	}
	if strings.ContainsAny(dc, "=+/") {
		t.Errorf("device_code contains non-base64url chars: %q", dc)
	}
}

// TestGenerateDeviceCode_PropagatesEntropyError covers the error
// branch: a failing entropy source surfaces an error (the handler maps
// this to a 500 rather than minting a low-entropy code).
func TestGenerateDeviceCode_PropagatesEntropyError(t *testing.T) {
	t.Parallel()
	boom := func(int) ([]byte, error) { return nil, context.DeadlineExceeded }
	ep := newDeviceAuthEndpointForGen(t, boom)
	if _, err := oauth.ExportGenerateDeviceCode(ep); err == nil {
		t.Fatal("expected error from failing entropy source")
	}
}

// TestGenerateUserCode_AlphabetAndShape covers RFC 8628 §6.1 +
// P0-191-4: the user_code is 8 chars from the unambiguous alphabet
// (no 0/O/1/I/L), formatted XXXX-XXXX, total length 9 including the
// hyphen. The generator uses crypto/rand directly (not the injected
// source), so we assert the structural contract over many draws.
func TestGenerateUserCode_AlphabetAndShape(t *testing.T) {
	t.Parallel()
	ep := newDeviceAuthEndpointForGen(t, nil) // generateUserCode ignores injected rand
	for i := 0; i < 64; i++ {
		uc, err := oauth.ExportGenerateUserCode(ep)
		if err != nil {
			t.Fatalf("generateUserCode: %v", err)
		}
		if len(uc) != 9 {
			t.Fatalf("user_code = %q, want length 9 (XXXX-XXXX)", uc)
		}
		if uc[4] != '-' {
			t.Fatalf("user_code = %q, want hyphen at index 4", uc)
		}
		body := strings.ReplaceAll(uc, "-", "")
		for _, ch := range body {
			if !strings.ContainsRune(oauth.ExportUserCodeAlphabet, ch) {
				t.Fatalf("user_code %q contains char %q outside the unambiguous alphabet", uc, ch)
			}
		}
		// Defense-in-depth on P0-191-4: the slice-191 alphabet
		// (ABCDEFGHJKLMNPQRSTUVWXYZ23456789) drops the glyphs that
		// collide visually — 0, O, 1, I — while KEEPING L. Assert the
		// dropped glyphs never appear.
		if strings.ContainsAny(body, "01OI") {
			t.Fatalf("user_code %q contains an excluded glyph (0/1/O/I)", uc)
		}
	}
}

// TestReadRandom_Contract covers the production entropy source: it
// returns exactly the requested number of bytes.
func TestReadRandom_Contract(t *testing.T) {
	t.Parallel()
	b, err := oauth.ExportReadRandom(48)
	if err != nil {
		t.Fatalf("readRandom: %v", err)
	}
	if len(b) != 48 {
		t.Errorf("readRandom(48) returned %d bytes, want 48", len(b))
	}
}

// TestBuildAtlasClaimsForUser_HappyPath covers RFC 6749 §4.1.3: the
// user-mode JWT projection sets sub=`user:<uuid>`, issuer + audience to
// the configured issuer, copies the auth code's tenant scope + roles,
// and copies super_admin verbatim.
func TestBuildAtlasClaimsForUser_HappyPath(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	tenant := uuid.New()
	ac := oauthcode.AuthCode{
		UserID:           userID,
		IDPIssuer:        "https://idp.example.test",
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
		SuperAdmin:       true,
	}
	roles := map[uuid.UUID][]string{tenant: {"owner"}}
	claims := oauth.ExportBuildAtlasClaimsForUser(testIssuer, ac, roles, pinnedNow())

	if claims.Subject != "user:"+userID.String() {
		t.Errorf("sub = %q, want user:%s", claims.Subject, userID)
	}
	if claims.Issuer != testIssuer {
		t.Errorf("iss = %q, want %s", claims.Issuer, testIssuer)
	}
	if claims.CurrentTenantID != tenant {
		t.Errorf("current_tenant = %v, want %v", claims.CurrentTenantID, tenant)
	}
	if !claims.SuperAdmin {
		t.Error("super_admin not copied from a true auth code")
	}
	if got := claims.Roles[tenant]; len(got) != 1 || got[0] != "owner" {
		t.Errorf("roles[%v] = %v, want [owner]", tenant, got)
	}
}

// TestBuildAtlasClaimsForUser_NilDefaults covers the defensive
// defaults: a nil roles map and nil available_tenants project to
// non-nil empty containers (so the JWT serializes `{}`/`[]` rather
// than `null`, keeping the wire shape stable for verifiers).
func TestBuildAtlasClaimsForUser_NilDefaults(t *testing.T) {
	t.Parallel()
	ac := oauthcode.AuthCode{
		UserID:           uuid.New(),
		AvailableTenants: nil,
	}
	claims := oauth.ExportBuildAtlasClaimsForUser(testIssuer, ac, nil, pinnedNow())
	if claims.Roles == nil {
		t.Error("roles is nil; want non-nil empty map")
	}
	if claims.AvailableTenants == nil {
		t.Error("available_tenants is nil; want non-nil empty slice")
	}
}

// TestDefaultLoginRedirect covers the slice-189 login-bounce URL
// builder: when the authorize handler finds no active session it
// redirects to /auth/oidc/login carrying the tenant_id and the
// original authorize URL as return_to so the flow resumes post-login.
func TestDefaultLoginRedirect(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	returnTo := "/oauth/authorize?client_id=cli&response_type=code"
	got := oauth.ExportDefaultLoginRedirect(returnTo, tenant)

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if u.Path != "/auth/oidc/login" {
		t.Errorf("path = %q, want /auth/oidc/login", u.Path)
	}
	if got := u.Query().Get("tenant_id"); got != tenant.String() {
		t.Errorf("tenant_id = %q, want %q", got, tenant)
	}
	if got := u.Query().Get("return_to"); got != returnTo {
		t.Errorf("return_to = %q, want %q", got, returnTo)
	}
}

// TestDBUserResolver_NilPoolDefault covers the slice-192
// DBUserResolver's defensive early return: with a nil pool (no DB), it
// returns the single-tenant default identity rather than panicking.
// This guards the wiring path where the resolver is constructed before
// the pool is ready.
func TestDBUserResolver_NilPoolDefault(t *testing.T) {
	t.Parallel()
	r := oauth.NewDBUserResolver(nil)
	userID := uuid.New()
	tenant := uuid.New()
	id, err := r.ResolveForOAuth(context.Background(), userID, tenant)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}
	if id.UserID != userID || id.CurrentTenantID != tenant {
		t.Errorf("identity = %+v, want user=%v tenant=%v", id, userID, tenant)
	}
	if len(id.AvailableTenants) != 1 || id.AvailableTenants[0] != tenant {
		t.Errorf("available_tenants = %v, want [%v]", id.AvailableTenants, tenant)
	}
	if id.SuperAdmin {
		t.Error("super_admin true from nil-pool default; want false")
	}
}
