//go:build integration

package decision_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// #2175: the overdue-decision notifier follows the Risk switch, its
// Decision log feature switch and the decision maker's module access. A
// decision skipped while switched off gets no dedup marker, so it is
// notified once switched back on.
func TestOverdue_NotifierFollowsModuleSwitchesAndAccess(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM member_module_access WHERE tenant_id = $1`,
			`DELETE FROM feature_flags WHERE tenant_id = $1`,
			`DELETE FROM user_roles WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(context.Background(), stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	ctx := ctxFor(t, tenant)
	store := decision.NewStore(app)

	maker := uuid.NewString()
	narrowed := uuid.NewString()
	for _, u := range []string{maker, narrowed} {
		if _, err := admin.Exec(context.Background(),
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'grc_engineer', 'test')`,
			tenant, u); err != nil {
			t.Fatalf("grant role: %v", err)
		}
	}
	access := moduleaccess.NewStore(app)
	if err := access.Set(ctx, uuid.MustParse(tenant), narrowed, []string{"policies"}, "test"); err != nil {
		t.Fatalf("narrow access: %v", err)
	}
	flags := featureflag.NewStore(app)

	past := time.Now().UTC().AddDate(0, 0, -5)
	for _, m := range []string{maker, narrowed} {
		in := validCreate(m)
		in.RevisitBy = &past
		if _, err := store.Create(ctx, in); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	notifier := decision.NewNotifier(admin, nil).WithModules(moduleaccess.NewViews(access, flags))
	sent := func(recipient string) int {
		t.Helper()
		var n int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM notifications WHERE tenant_id = $1 AND type = $2 AND recipient_user_id = $3`,
			tenant, decision.NotificationTypeOverdue, recipient).Scan(&n); err != nil {
			t.Fatalf("count notifications: %v", err)
		}
		return n
	}
	sweep := func() {
		t.Helper()
		if _, err := notifier.SweepOnce(context.Background()); err != nil {
			t.Fatalf("SweepOnce: %v", err)
		}
	}

	// Risk off, then only the Decision log feature off: nothing is sent.
	for _, key := range []string{"risk.enabled", "decisions.log"} {
		if _, err := flags.Set(ctx, key, false, "test", "#2175"); err != nil {
			t.Fatalf("switch %s off: %v", key, err)
		}
		sweep()
		if got := sent(maker); got != 0 {
			t.Fatalf("%s off: decision maker got %d notifications; want 0", key, got)
		}
		if _, err := flags.Set(ctx, key, true, "test", "#2175"); err != nil {
			t.Fatalf("switch %s on: %v", key, err)
		}
	}

	// Both on: the decision maker is notified once; the one without Risk
	// access is not.
	sweep()
	sweep()
	if got := sent(maker); got != 1 {
		t.Errorf("switched on: decision maker got %d notifications; want 1", got)
	}
	if got := sent(narrowed); got != 0 {
		t.Errorf("no Risk access: got %d notifications; want 0", got)
	}
}
