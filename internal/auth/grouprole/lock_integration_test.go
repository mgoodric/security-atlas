//go:build integration

package grouprole_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
)

// holdRosterLock takes the tenant's admin-roster lock (the tenant row, FOR
// NO KEY UPDATE, as users.LockAdmins takes it) in a transaction the test
// controls, and returns a release func.
func holdRosterLock(t *testing.T, tenantID uuid.UUID) func() {
	t.Helper()
	ctx := context.Background()
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id = $1 FOR NO KEY UPDATE`, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("hold roster lock: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = tx.Rollback(ctx)
		}
	}
	t.Cleanup(release)
	return release
}

// rosterWaiters counts atlas_app backends blocked on the roster lock. It
// polls through the app pool: a role sees the query text of its own
// sessions in pg_stat_activity.
func rosterWaiters(t *testing.T) int {
	t.Helper()
	var n int
	if err := appPool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_stat_activity
		  WHERE wait_event_type = 'Lock'
		    AND query ILIKE '%FOR NO KEY UPDATE%'
		    AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
		t.Fatalf("poll pg_stat_activity: %v", err)
	}
	return n
}

func waitRosterWaiters(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if rosterWaiters(t) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d backends waiting on the admin-roster lock within 15s", want)
}

// #2273: a login whose derived plan revokes no admin role does not take the
// tenant's admin-roster lock, so it completes while another transaction
// holds that lock. Before #2273 every derivation took it and this one would
// block until the holder finished.
func TestDerive_NoAdminRevokeSkipsRosterLock_2273(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-nolock")
	idp := seedIDPConfig(t, tenant, "nolock-idp")
	seedMapping(t, tenant, idp, "Viewers", "viewer")
	seedMapping(t, tenant, idp, "Engineers", "grc_engineer")
	r := grouprole.NewResolver(appPool)
	user := seedMember(t, tenant, "active")
	if _, err := r.Derive(tenantCtx(t, tenant), grouprole.DeriveInput{
		UserID: user, IDPConfigID: idp, Groups: []string{"Viewers"}, Source: grouprole.SourceOIDC,
	}); err != nil {
		t.Fatalf("seed derive: %v", err)
	}

	release := holdRosterLock(t, tenant)
	defer release()

	// A grant and a non-admin revoke in one login, under a deadline well
	// short of any lock wait.
	ctx, cancel := context.WithTimeout(tenantCtx(t, tenant), 3*time.Second)
	defer cancel()
	res, err := r.Derive(ctx, grouprole.DeriveInput{
		UserID: user, IDPConfigID: idp, Groups: []string{"Engineers"}, Source: grouprole.SourceOIDC,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			t.Fatalf("login without an admin revoke waited on the admin-roster lock: %v", err)
		}
		t.Fatalf("derive: %v", err)
	}
	if len(res.Granted) != 1 || res.Granted[0] != "grc_engineer" || len(res.Revoked) != 1 || res.Revoked[0] != "viewer" {
		t.Fatalf("granted=%v revoked=%v, want [grc_engineer] / [viewer]", res.Granted, res.Revoked)
	}
}

// #2273: two concurrent logins with no admin revoke in the same tenant do
// not serialise on each other: both finish while a third transaction holds
// the roster lock.
func TestDerive_ConcurrentNonAdminLoginsDoNotSerialise_2273(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-parallel")
	idp := seedIDPConfig(t, tenant, "parallel-idp")
	seedMapping(t, tenant, idp, "Viewers", "viewer")
	r := grouprole.NewResolver(appPool)
	userA := seedMember(t, tenant, "active")
	userB := seedMember(t, tenant, "active")

	release := holdRosterLock(t, tenant)
	defer release()

	ctx, cancel := context.WithTimeout(tenantCtx(t, tenant), 5*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	for _, u := range []string{userA, userB} {
		go func(u string) {
			_, err := r.Derive(ctx, grouprole.DeriveInput{
				UserID: u, IDPConfigID: idp, Groups: []string{"Viewers"}, Source: grouprole.SourceOIDC,
			})
			errs <- err
		}(u)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent login: %v", err)
		}
	}
	for _, u := range []string{userA, userB} {
		if _, ok := rolesFor(t, tenant, u)["viewer"]; !ok {
			t.Fatalf("user %s missing viewer", u)
		}
	}
}

// #2273: the lock is still taken when the plan revokes admin, and the
// last-admin guard still holds when the tenant's only two admins lose their
// admin group at the same time. Both logins queue on the roster lock; when
// it is released exactly one revoke runs and the other is suppressed.
func TestDerive_ConcurrentAdminRevokesKeepOneAdmin_2273(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-twoadmin-race")
	idp := seedIDPConfig(t, tenant, "race-idp")
	seedMapping(t, tenant, idp, "Admins", "admin")
	r := grouprole.NewResolver(appPool)
	userA := seedMember(t, tenant, "active")
	userB := seedMember(t, tenant, "active")
	for _, u := range []string{userA, userB} {
		if _, err := r.Derive(tenantCtx(t, tenant), grouprole.DeriveInput{
			UserID: u, IDPConfigID: idp, Groups: []string{"Admins"}, Source: grouprole.SourceOIDC,
		}); err != nil {
			t.Fatalf("seed admin %s: %v", u, err)
		}
	}

	release := holdRosterLock(t, tenant)
	type outcome struct {
		res grouprole.DeriveResult
		err error
	}
	out := make(chan outcome, 2)
	for _, u := range []string{userA, userB} {
		go func(u string) {
			res, err := r.Derive(tenantCtx(t, tenant), grouprole.DeriveInput{
				UserID: u, IDPConfigID: idp, Groups: nil, Source: grouprole.SourceOIDC,
			})
			out <- outcome{res, err}
		}(u)
	}
	// Both admin-revoking logins must be queued on the roster lock.
	waitRosterWaiters(t, 2)
	release()

	revoked, suppressed := 0, 0
	for range 2 {
		o := <-out
		if o.err != nil {
			t.Fatalf("derive: %v", o.err)
		}
		revoked += len(o.res.Revoked)
		suppressed += len(o.res.SuppressedRevokes)
	}
	if revoked != 1 || suppressed != 1 {
		t.Fatalf("revoked=%d suppressed=%d, want 1 and 1", revoked, suppressed)
	}
	var admins int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM user_roles WHERE tenant_id = $1 AND role = 'admin'`, tenant).Scan(&admins); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if admins != 1 {
		t.Fatalf("tenant has %d admins after two concurrent revokes, want 1", admins)
	}
}
