package jwtmw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/superadmin"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// superAdminRequest runs one request carrying a super_admin token for
// subject through the middleware with checker wired, and returns what
// the downstream handler saw.
func superAdminRequest(t *testing.T, subject uuid.UUID, checker superadmin.Checker) (status int, claims *jwt.AtlasClaims, credAdmin bool, tenant string) {
	t.Helper()
	signer := newSigner(t)
	now := time.Now()
	c := freshClaims(now)
	c.Subject = "user:" + subject.String()
	c.SuperAdmin = true
	tok := mintToken(t, signer, c)

	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              nowAt(now),
		SuperAdmins:      checker,
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims = jwtmw.FromContext(r.Context())
		if cred, ok := authctx.CredentialFromContext(r.Context()); ok {
			credAdmin = cred.IsAdmin
		}
		tenant, _ = tenancy.TenantFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/super-admins", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if claims != nil && tenant != c.CurrentTenantID.String() {
		t.Errorf("tenant = %q, want %q (the token's tenant must survive)", tenant, c.CurrentTenantID)
	}
	return w.Code, claims, credAdmin, tenant
}

// TestMiddleware_DemotedSuperAdmin_ClaimDropped covers #1850: a token
// minted while its subject was super_admin loses the claim on the next
// request once super_admins no longer holds the subject. The request
// still authenticates and keeps its tenant; only the claim goes.
func TestMiddleware_DemotedSuperAdmin_ClaimDropped(t *testing.T) {
	t.Parallel()
	demoted := uuid.New()
	empty := superadmin.CheckerFunc(func(context.Context, uuid.UUID) (bool, error) { return false, nil })

	status, claims, credAdmin, _ := superAdminRequest(t, demoted, empty)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (demote must not log the user out)", status)
	}
	if claims == nil {
		t.Fatal("FromContext returned nil")
	}
	if claims.SuperAdmin {
		t.Error("claims.SuperAdmin = true after demote, want false")
	}
	if credAdmin {
		t.Error("credential IsAdmin = true after demote, want false")
	}
}

// TestMiddleware_SuperAdmin_ClaimKeptWhileGranted: the re-check keeps
// the claim for a subject still in super_admins, and is keyed on the
// subject's UUID with the "user:" prefix stripped.
func TestMiddleware_SuperAdmin_ClaimKeptWhileGranted(t *testing.T) {
	t.Parallel()
	admin := uuid.New()
	var asked uuid.UUID
	checker := superadmin.CheckerFunc(func(_ context.Context, id uuid.UUID) (bool, error) {
		asked = id
		return id == admin, nil
	})

	status, claims, credAdmin, _ := superAdminRequest(t, admin, checker)
	if status != http.StatusOK || claims == nil {
		t.Fatalf("status = %d, claims = %v; want 200 with claims", status, claims)
	}
	if !claims.SuperAdmin || !credAdmin {
		t.Errorf("SuperAdmin = %v, IsAdmin = %v; want both true", claims.SuperAdmin, credAdmin)
	}
	if asked != admin {
		t.Errorf("checker asked about %v, want %v", asked, admin)
	}
}
