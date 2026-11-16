//go:build integration

// #2447: the multi-tenant row with real JWTs and real user_roles. One
// person is admin in tenant A and viewer in tenant B. Their tenant-B
// token, even one whose B role claim still says admin, is refused every
// gate in routes2428; their tenant-A token passes every gate.
//
// Run with:  go test -tags=integration -race -run 2447 ./internal/authz/...

package authz_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// mintIn signs a normal sign-in JWT acting in current, listing both
// tenants with the given role claims.
func (s stack2428) mintIn(t *testing.T, current string, user uuid.UUID, claims map[uuid.UUID][]string) string {
	t.Helper()
	now := time.Now()
	var avail []uuid.UUID
	for tid := range claims {
		avail = append(avail, tid)
	}
	tok, err := s.signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer2428,
			Subject:   "user:" + user.String(),
			Audience:  []string{audience2428},
			ExpiresAt: now.Add(time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "local",
		CurrentTenantID:  uuid.MustParse(current),
		AvailableTenants: avail,
		Roles:            claims,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func TestTenantGates2447_AdminInAViewerInBRealJWT(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, adminPool)
	tenantB := freshTenant(t, adminPool)
	s := newStack2428(t, appPool)

	user := seedUser2428(t, adminPool, tenantA, authz.RoleAdmin)
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'viewer', 'test-2447')`,
		tenantB, user.String()); err != nil {
		t.Fatalf("seed tenant B viewer: %v", err)
	}
	a, b := uuid.MustParse(tenantA), uuid.MustParse(tenantB)

	inA := s.mintIn(t, tenantA, user, map[uuid.UUID][]string{a: {"admin"}, b: {"viewer"}})
	inB := s.mintIn(t, tenantB, user, map[uuid.UUID][]string{a: {"admin"}, b: {"viewer"}})
	// A stale claim saying admin in B: the live B row is viewer.
	inBStale := s.mintIn(t, tenantB, user, map[uuid.UUID][]string{a: {"admin"}, b: {"admin"}})

	for _, rt := range routes2428 {
		if code, _ := s.do(inA, rt.method, rt.path); code != http.StatusNoContent {
			t.Errorf("tenant-A token: %s %s = %d, want 204", rt.method, rt.path, code)
		}
		if code, _ := s.do(inB, rt.method, rt.path); code != http.StatusForbidden {
			t.Errorf("tenant-B token: %s %s = %d, want 403", rt.method, rt.path, code)
		}
		if code, _ := s.do(inBStale, rt.method, rt.path); code != http.StatusForbidden {
			t.Errorf("tenant-B token claiming admin in B: %s %s = %d, want 403", rt.method, rt.path, code)
		}
	}
}
