// Pure-Go unit tests for the slice-478 assign surface (slice-353 Q-2 fast
// loop: validators / cursor codec / limit clamp — no Postgres, no build tag).
package adminusers

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// TestActorFromContext_StripsUserPrefix is the regression test for the
// "actor user_id not on context" bug: the real auth substrate mints the atlas
// JWT Subject as "user:<uuid>" (internal/api/auth/http.go), and jwtmw bridges
// the same value into the credential's UserID. actorFromContext must strip the
// prefix before parsing, or every real-auth caller of the self-assign path
// resolves to uuid.Nil and gets a 500. The prior integration tests masked this
// by minting a BARE-UUID subject.
func TestActorFromContext_StripsUserPrefix(t *testing.T) {
	t.Parallel()
	id := uuid.New()

	cases := []struct {
		name string
		ctx  func() context.Context
	}{
		{
			name: "jwt subject with user: prefix (real auth path)",
			ctx: func() context.Context {
				claims := &jwt.AtlasClaims{
					RegisteredClaims: jwt.RegisteredClaims{Subject: "user:" + id.String()},
				}
				return jwtmw.WithClaimsForTest(context.Background(), claims)
			},
		},
		{
			name: "bare-uuid jwt subject (legacy/test path) still works",
			ctx: func() context.Context {
				claims := &jwt.AtlasClaims{
					RegisteredClaims: jwt.RegisteredClaims{Subject: id.String()},
				}
				return jwtmw.WithClaimsForTest(context.Background(), claims)
			},
		},
		{
			name: "credential fallback with user: prefix",
			ctx: func() context.Context {
				return authctx.WithCredential(context.Background(),
					credstore.Credential{UserID: "user:" + id.String()})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := actorFromContext(tc.ctx()); got != id {
				t.Errorf("actorFromContext = %s, want %s", got, id)
			}
		})
	}
}

// TestActorFromContext_NilWhenAbsent confirms the genuine no-actor case still
// resolves to uuid.Nil (the self-assign 500 guard depends on this).
func TestActorFromContext_NilWhenAbsent(t *testing.T) {
	t.Parallel()
	if got := actorFromContext(context.Background()); got != uuid.Nil {
		t.Errorf("actorFromContext(empty) = %s, want uuid.Nil", got)
	}
}

func TestValidateAssign(t *testing.T) {
	t.Parallel()
	validTenant := uuid.New().String()
	validUser := uuid.New().String()

	cases := []struct {
		name    string
		req     AssignRequest
		wantErr bool
		wantN   int // expected role count when no error
	}{
		{
			name:    "happy path single role",
			req:     AssignRequest{UserID: validUser, TenantID: validTenant, Roles: []string{"admin"}},
			wantErr: false, wantN: 1,
		},
		{
			name:    "self-assign needs no user_id",
			req:     AssignRequest{TenantID: validTenant, Roles: []string{"viewer"}, SelfAssign: true},
			wantErr: false, wantN: 1,
		},
		{
			name:    "dedupes roles",
			req:     AssignRequest{UserID: validUser, TenantID: validTenant, Roles: []string{"admin", "admin", "viewer"}},
			wantErr: false, wantN: 2,
		},
		{
			name:    "missing tenant",
			req:     AssignRequest{UserID: validUser, Roles: []string{"admin"}},
			wantErr: true,
		},
		{
			name:    "bad tenant uuid",
			req:     AssignRequest{UserID: validUser, TenantID: "not-a-uuid", Roles: []string{"admin"}},
			wantErr: true,
		},
		{
			name:    "missing user_id without self-assign",
			req:     AssignRequest{TenantID: validTenant, Roles: []string{"admin"}},
			wantErr: true,
		},
		{
			name:    "no roles",
			req:     AssignRequest{UserID: validUser, TenantID: validTenant, Roles: []string{}},
			wantErr: true,
		},
		{
			name:    "unknown role rejected",
			req:     AssignRequest{UserID: validUser, TenantID: validTenant, Roles: []string{"super-admin"}},
			wantErr: true,
		},
		{
			name:    "unknown role mixed with valid rejected",
			req:     AssignRequest{UserID: validUser, TenantID: validTenant, Roles: []string{"admin", "root"}},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, roles, err := validateAssign(tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (roles=%v)", roles)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(roles) != tc.wantN {
				t.Errorf("role count = %d; want %d (roles=%v)", len(roles), tc.wantN, roles)
			}
		})
	}
}

func TestCrossTenantCursorRoundTrip(t *testing.T) {
	t.Parallel()
	tenantID := uuid.New().String()
	userID := uuid.New().String()
	cur := encodeCrossTenantCursor(tenantID, userID)
	gotT, gotU, err := decodeCrossTenantCursor(cur)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotT.String() != tenantID {
		t.Errorf("tenant = %s; want %s", gotT, tenantID)
	}
	if gotU.String() != userID {
		t.Errorf("user = %s; want %s", gotU, userID)
	}
}

func TestDecodeCrossTenantCursor(t *testing.T) {
	t.Parallel()
	t.Run("empty is first page", func(t *testing.T) {
		t.Parallel()
		tID, uID, err := decodeCrossTenantCursor("")
		if err != nil || tID != uuid.Nil || uID != uuid.Nil {
			t.Fatalf("empty cursor = (%v,%v,%v); want nil,nil,nil", tID, uID, err)
		}
	})
	t.Run("non-base64 rejected", func(t *testing.T) {
		t.Parallel()
		if _, _, err := decodeCrossTenantCursor("!!!not base64!!!"); err == nil {
			t.Error("expected error for non-base64 cursor")
		}
	})
	t.Run("malformed (no colon) rejected", func(t *testing.T) {
		t.Parallel()
		bad := encodeCrossTenantCursor("", "") // "":""  -> still has colon, but parts are bad uuids
		_ = bad
		// directly craft a base64 with no colon:
		if _, _, err := decodeCrossTenantCursor(encodeNoColon("noseparator")); err == nil {
			t.Error("expected error for cursor without separator")
		}
	})
	t.Run("bad tenant uuid rejected", func(t *testing.T) {
		t.Parallel()
		if _, _, err := decodeCrossTenantCursor(encodeRaw("bad:" + uuid.New().String())); err == nil {
			t.Error("expected error for bad tenant uuid in cursor")
		}
	})
}

func TestCrossTenantListLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int
	}{
		{"", defaultListLimit},
		{"0", defaultListLimit},
		{"-5", defaultListLimit},
		{"abc", defaultListLimit},
		{"10", 10},
		{"200", 200},
		{"500", maxListLimit},
	}
	for _, tc := range cases {
		if got := crossTenantListLimit(tc.in); got != tc.want {
			t.Errorf("limit(%q) = %d; want %d", tc.in, got, tc.want)
		}
	}
}

func TestNullableUUID(t *testing.T) {
	t.Parallel()
	if nullableUUID(uuid.Nil) != nil {
		t.Error("nil UUID should map to nil pointer (first-page branch)")
	}
	u := uuid.New()
	if got := nullableUUID(u); got == nil || *got != u {
		t.Errorf("non-nil UUID should round-trip; got %v", got)
	}
}

// LocalSyntheticIssuer must be non-empty so the slice-192 resolver's
// non-empty guard fires (P0-478-2 — the whole point of the synthetic key).
func TestLocalSyntheticIssuerNonEmpty(t *testing.T) {
	t.Parallel()
	if LocalSyntheticIssuer == "" {
		t.Fatal("LocalSyntheticIssuer must be non-empty to avoid empty-tuple over-match")
	}
}

// --- test-only base64 helpers (neutral, no vendor tokens — P0-478-6) ---

func encodeNoColon(s string) string { return encodeRaw(s) }

func encodeRaw(s string) string {
	return base64.URLEncoding.EncodeToString([]byte(s))
}
