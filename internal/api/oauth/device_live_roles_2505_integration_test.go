//go:build integration

// #2505: a device approval mints the approver's live user_roles in the
// tenant, not the session's role claim. The claim outlives a revoke until
// the token expires; copying it into the device-code snapshot re-minted a
// revoked role with a fresh expiry.

package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestIntegrationDeviceApprove2505_MintsLiveRolesNotClaim(t *testing.T) {
	adminPool := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, adminPool, "user_roles")
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := adminPool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	user := uuid.New()
	exec(`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2505', 'active')`,
		user, tenant, "u2505-"+user.String()+"@example.invalid")
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user)
	})
	grant := func(role authz.Role) {
		exec(`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2505')`,
			tenant, user.String(), string(role))
	}
	revokeAll := func() {
		exec(`DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, user.String())
	}

	// The approver's session was minted while they were admin; the claim
	// still says so after every revoke below.
	approver := credstore.Credential{
		ID:         "jwt:" + uuid.NewString(),
		TenantID:   tenant,
		UserID:     "user:" + user.String(),
		OwnerRoles: []string{"admin"},
	}
	base := time.Now().UTC().Truncate(time.Second)
	h := newApprovalHarnessWithRoles(t, approver, func() time.Time { return base },
		authz.NewDBRolesResolver(dbtest.NewAppPool(t)))

	// approve approves a fresh device code and returns the roles the
	// snapshot records for the tenant.
	approve := func() []string {
		t.Helper()
		userCode, _ := h.insertCode(t, base.Add(15*time.Minute))
		if resp, body := h.postApprove(t, userCode); resp.StatusCode != http.StatusOK {
			t.Fatalf("approve status = %d, body = %s; want 200", resp.StatusCode, body)
		}
		row, err := h.devCodes.LookupByUserCode(ctx, userCode)
		if err != nil {
			t.Fatalf("LookupByUserCode: %v", err)
		}
		var roles map[string][]string
		if err := json.Unmarshal(row.ApprovedByRoles, &roles); err != nil {
			t.Fatalf("decode approved_by_roles %s: %v", row.ApprovedByRoles, err)
		}
		if len(roles) != 1 {
			t.Fatalf("approved_by_roles = %s; want one tenant", row.ApprovedByRoles)
		}
		got, ok := roles[tenant]
		if !ok {
			t.Fatalf("approved_by_roles = %s; want tenant %s", row.ApprovedByRoles, tenant)
		}
		return got
	}

	grant(authz.RoleAdmin)
	if got := approve(); !reflect.DeepEqual(got, []string{"admin"}) {
		t.Errorf("live admin: minted %v; want [admin]", got)
	}

	// Admin revoked, viewer granted, same session: the device token
	// carries viewer and not the stale admin claim.
	revokeAll()
	grant(authz.RoleViewer)
	if got := approve(); !reflect.DeepEqual(got, []string{"viewer"}) {
		t.Errorf("admin revoked, live viewer: minted %v; want [viewer]", got)
	}

	// Every role revoked: the device token carries none (#2401).
	revokeAll()
	if got := approve(); len(got) != 0 {
		t.Errorf("every role revoked: minted %v; want none", got)
	}
}
