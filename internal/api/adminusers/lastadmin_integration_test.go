//go:build integration

// #2261 — the admin API refuses any change that would leave a tenant with no
// active admin, under a lock, so two concurrent revokes cannot both pass.
//
// The concurrent case holds one revoke at a fixed point (after its check,
// before its commit) and observes the other waiting on the lock before
// releasing the first, so the interleaving is fixed rather than left to
// timing (the #2204 pausedAssigner pattern).

package adminusers_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminusers"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// lastAdminRouter wires h for a within-tenant tenant-admin caller.
func lastAdminRouter(h *adminusers.Handler, tenant, actor uuid.UUID) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := jwtmw.WithClaimsForTest(req.Context(), &jwt.AtlasClaims{
				RegisteredClaims: jwt.RegisteredClaims{Subject: actor.String()},
				CurrentTenantID:  tenant,
				AvailableTenants: []uuid.UUID{tenant},
			})
			ctx = authctx.WithCredential(ctx, credstore.Credential{
				ID:       "jwt:test",
				TenantID: tenant.String(),
				UserID:   "user:" + actor.String(),
				IsAdmin:  true,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/admin/users/revoke", h.Revoke)
	r.Patch("/v1/admin/users/{id}/roles", h.PatchRoles)
	return r
}

// seedMember2261 seeds a users row with status and, when role is non-empty,
// a user_roles row keyed by its bare id.
func seedMember2261(t *testing.T, tenant uuid.UUID, status, role string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status)
		 VALUES ($1, $2, $3, 'Last Admin Member', $4)`,
		id, tenant, "test-2261-"+id.String()[:8]+"@example.com", status); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if role != "" {
		if _, err := adminPool.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-seeder')`,
			tenant, id.String(), role); err != nil {
			t.Fatalf("seed role: %v", err)
		}
	}
	return id
}

func activeAdmins2261(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(DISTINCT u.id) FROM user_roles ur
		   JOIN users u ON u.tenant_id = ur.tenant_id AND u.id::text = regexp_replace(ur.user_id, '^user:', '')
		  WHERE ur.tenant_id = $1 AND ur.role = 'admin' AND u.status = 'active' AND u.active`,
		tenant).Scan(&n); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	return n
}

func revokeAudits2261(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'user_tenant_revoke'`,
		tenant).Scan(&n); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	return n
}

func revokeBody(user, tenant uuid.UUID, removeMembership bool) map[string]any {
	return map[string]any{
		"user_id":           user.String(),
		"tenant_id":         tenant.String(),
		"remove_membership": removeMembership,
	}
}

// Revoking, or disabling, the tenant's only active admin is refused with 409,
// and the refused attempt leaves roles, status and the audit log untouched.
func TestRevoke_LastAdminRefused_2261(t *testing.T) {
	skipIfNoAdminPool(t)
	for _, removeMembership := range []bool{false, true} {
		tenant := seedTenant478(t, "test-2261-last-"+uuid.NewString()[:8])
		actor := seedMember2261(t, tenant, "active", "")
		admin := seedMember2261(t, tenant, "active", "admin")
		h := lastAdminRouter(adminusers.New(appPool), tenant, actor)

		rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/revoke", revokeBody(admin, tenant, removeMembership))
		if rr.Code != http.StatusConflict {
			t.Fatalf("remove_membership=%v: status = %d, want 409; body = %s", removeMembership, rr.Code, rr.Body.String())
		}
		if got := activeAdmins2261(t, tenant); got != 1 {
			t.Errorf("remove_membership=%v: active admins = %d, want 1", removeMembership, got)
		}
		if got := revokeAudits2261(t, tenant); got != 0 {
			t.Errorf("remove_membership=%v: refused revoke wrote %d audit rows", removeMembership, got)
		}
	}
}

// Revoking one of two active admins succeeds. The second admin's role row
// carries the "user:<uuid>" principal; it still counts by its bare id.
func TestRevoke_OneOfTwoAdminsSucceeds_2261(t *testing.T) {
	skipIfNoAdminPool(t)
	for _, prefixed := range []bool{false, true} {
		tenant := seedTenant478(t, "test-2261-two-"+uuid.NewString()[:8])
		actor := seedMember2261(t, tenant, "active", "")
		a := seedMember2261(t, tenant, "active", "admin")
		b := seedMember2261(t, tenant, "active", "")
		bPrincipal := b.String()
		if prefixed {
			bPrincipal = "user:" + b.String()
		}
		if _, err := adminPool.Exec(context.Background(),
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'admin', 'test-seeder')`,
			tenant, bPrincipal); err != nil {
			t.Fatalf("seed b admin: %v", err)
		}
		h := lastAdminRouter(adminusers.New(appPool), tenant, actor)

		rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/revoke", revokeBody(a, tenant, false))
		if rr.Code != http.StatusNoContent {
			t.Fatalf("prefixed=%v: status = %d, want 204; body = %s", prefixed, rr.Code, rr.Body.String())
		}
		if got := activeAdmins2261(t, tenant); got != 1 {
			t.Errorf("prefixed=%v: active admins = %d, want 1", prefixed, got)
		}
	}
}

// A second admin whose membership is disabled is not an active admin, so it
// does not let the only active admin be revoked.
func TestRevoke_DisabledSecondAdminDoesNotCount_2261(t *testing.T) {
	skipIfNoAdminPool(t)
	tenant := seedTenant478(t, "test-2261-disabled")
	actor := seedMember2261(t, tenant, "active", "")
	admin := seedMember2261(t, tenant, "active", "admin")
	_ = seedMember2261(t, tenant, "disabled", "admin")
	h := lastAdminRouter(adminusers.New(appPool), tenant, actor)

	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/revoke", revokeBody(admin, tenant, false))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rr.Code, rr.Body.String())
	}
}

// PATCH .../roles that drops admin from the only active admin is refused,
// and the role set is unchanged.
func TestPatchRoles_LastAdminRefused_2261(t *testing.T) {
	skipIfNoAdminPool(t)
	tenant := seedTenant478(t, "test-2261-patch")
	actor := seedMember2261(t, tenant, "active", "")
	admin := seedMember2261(t, tenant, "active", "admin")
	h := lastAdminRouter(adminusers.New(appPool), tenant, actor)

	rr := doJSON(t, h, http.MethodPatch, "/v1/admin/users/"+admin.String()+"/roles",
		map[string]any{"roles": []string{"viewer"}})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rr.Code, rr.Body.String())
	}
	if got := countUserRoles(t, tenant, admin); got != 1 {
		t.Errorf("role rows = %d, want the original 1", got)
	}
	if got := activeAdmins2261(t, tenant); got != 1 {
		t.Errorf("active admins = %d, want 1", got)
	}
}

// pausedRevoker is a handler whose Revoke stops once, after its last-admin
// check and before its commit, until release is called. release is
// idempotent and runs at cleanup, so a failed test never strands the
// transaction.
func pausedRevoker(t *testing.T) (h *adminusers.Handler, paused <-chan struct{}, release func()) {
	p := make(chan struct{})
	r := make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	release = func() { releaseOnce.Do(func() { close(r) }) }
	t.Cleanup(release)
	h = adminusers.New(appPool).SetBeforeRevokeCommitForTest(func() {
		pauseOnce.Do(func() {
			close(p)
			<-r
		})
	})
	return h, p, release
}

// waitRosterLockWaiterOr waits until a backend is blocked on the admin-roster
// lock statement, or until done fires (the side that should wait finished
// without waiting). It reports whether it saw the waiter. It polls through
// the app pool: the handlers' sessions run as atlas_app, and a role can read
// the query text of its own sessions in pg_stat_activity, which atlas_migrate
// cannot.
func waitRosterLockWaiterOr(t *testing.T, done <-chan int) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(done) > 0 {
			return false
		}
		var n int
		if err := appPool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE wait_event_type = 'Lock'
			    AND query ILIKE '%FOR NO KEY UPDATE%'
			    AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if n > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no backend waiting on the admin-roster lock within 15s")
	return false
}

// A tenant row the transaction cannot see means the roster lock cannot be
// taken. The revoke fails closed with 500 instead of running unlocked, and
// the role is untouched.
func TestRevoke_FailsClosedWithoutTenantRow_2261(t *testing.T) {
	skipIfNoAdminPool(t)
	tenant := uuid.New() // no tenants row
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = adminPool.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1`, tenant)
		_, _ = adminPool.Exec(ctx, `DELETE FROM me_audit_log WHERE tenant_id = $1`, tenant)
		_, _ = adminPool.Exec(ctx, `DELETE FROM users WHERE tenant_id = $1`, tenant)
	})
	actor := seedMember2261(t, tenant, "active", "")
	a := seedMember2261(t, tenant, "active", "admin")
	_ = seedMember2261(t, tenant, "active", "admin")
	h := lastAdminRouter(adminusers.New(appPool), tenant, actor)

	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/revoke", revokeBody(a, tenant, false))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rr.Code, rr.Body.String())
	}
	if got := countUserRoles(t, tenant, a); got != 1 {
		t.Errorf("role rows = %d, want the original 1", got)
	}
	if got := revokeAudits2261(t, tenant); got != 0 {
		t.Errorf("failed revoke wrote %d audit rows", got)
	}
}

// Two revokes of the tenant's last two admins. The first has checked (one
// admin would remain) and not committed; the second must wait for it, then
// see only one admin and refuse. Without the lock both see two admins and
// both commit, leaving none.
func TestRevoke_ConcurrentLastTwoAdminsLeaveOne_2261(t *testing.T) {
	skipIfNoAdminPool(t)
	tenant := seedTenant478(t, "test-2261-race")
	actor := seedMember2261(t, tenant, "active", "")
	a := seedMember2261(t, tenant, "active", "admin")
	b := seedMember2261(t, tenant, "active", "admin")

	paused, holdAt, release := pausedRevoker(t)
	first := make(chan int, 1)
	go func() {
		first <- doJSON(t, lastAdminRouter(paused, tenant, actor), http.MethodPost,
			"/v1/admin/users/revoke", revokeBody(a, tenant, false)).Code
	}()
	<-holdAt

	second := make(chan int, 1)
	go func() {
		second <- doJSON(t, lastAdminRouter(adminusers.New(appPool), tenant, actor), http.MethodPost,
			"/v1/admin/users/revoke", revokeBody(b, tenant, false)).Code
	}()
	if !waitRosterLockWaiterOr(t, second) {
		t.Error("the second revoke did not wait for the first")
	}
	release()

	if got := <-first; got != http.StatusNoContent {
		t.Errorf("first revoke = %d, want 204", got)
	}
	if got := <-second; got != http.StatusConflict {
		t.Errorf("second revoke = %d, want 409", got)
	}
	if got := activeAdmins2261(t, tenant); got != 1 {
		t.Fatalf("active admins = %d, want 1", got)
	}
	if got := revokeAudits2261(t, tenant); got != 1 {
		t.Errorf("revoke audit rows = %d, want 1 (the refused one writes nothing)", got)
	}
}
