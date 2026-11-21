//go:build integration

package policies_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// TestImportHTTP_LiveRoleAdminOwnTenantOnly (#2665): a user who holds the
// live admin role in user_roles, without the credential admin flag, may
// import policies, as OPA admits it. The grant is per tenant: the same user
// naming a tenant where they hold no live role is refused, and nothing is
// written there.
func TestImportHTTP_LiveRoleAdminOwnTenantOnly(t *testing.T) {
	s := setupWithLiveRoles(t)
	userID, bearer := s.issueRoledUserBearer(t, "live-admin@2665.test", []string{"admin"})

	resp := postImport(t, s, bearer, false, http.StatusOK, policyImportFile("Live Admin Policy", "1.0", ""))
	if !resp.Committed || resp.Imported != 1 {
		t.Fatalf("live-role admin import: resp = %+v", resp)
	}
	if n := tenantPolicyCount(t, s); n != 1 {
		t.Fatalf("own tenant policy count = %d, want 1", n)
	}

	// A second tenant where the user holds no live role. The token's role
	// claim says admin there; a role claim is not a grant, so the import is
	// refused.
	other := freshTenant(t, s.admin)
	claims := testjwt.OwnerFor(uuid.MustParse(other), []string{"admin"})
	claims.Subject = userID.String()
	otherBearer := s.apiServer.IssueTestJWT(t, claims)
	postImport(t, s, otherBearer, false, http.StatusForbidden, policyImportFile("Cross Tenant Policy", "1.0", ""))

	var n int
	if err := s.admin.QueryRow(context.Background(), `SELECT count(*) FROM policies WHERE tenant_id = $1`, other).Scan(&n); err != nil {
		t.Fatalf("count other tenant: %v", err)
	}
	if n != 0 {
		t.Fatalf("other tenant policy count = %d, want 0", n)
	}
}
