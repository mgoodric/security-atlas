//go:build integration

// #2146: the declared exception lifecycle agrees with the store, pair by
// pair, against a real Postgres.

package exception_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/lifecycle/lifecycletest"
)

func TestLifecycle_EveryPair(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	approve := func(t *testing.T, id string) error {
		_, err := store.Approve(ctx, uuid.MustParse(id), approverPerson)
		return err
	}
	deny := func(t *testing.T, id string) error {
		_, err := store.Deny(ctx, uuid.MustParse(id), exception.Denier{Principal: "key_denier"}, "not this quarter")
		return err
	}
	activate := func(t *testing.T, id string) error {
		_, err := store.Activate(ctx, uuid.MustParse(id), "key_approver", time.Time{})
		return err
	}
	// expire backdates expires_at and runs the sweep, which expires only
	// active rows.
	expire := func(t *testing.T, id string) error {
		if _, err := admin.Exec(context.Background(),
			`UPDATE exceptions SET expires_at = now() - interval '1 hour' WHERE id = $1`, id); err != nil {
			t.Fatalf("backdate: %v", err)
		}
		_, err := exception.NewExpirer(admin, nil).SweepOnce(context.Background())
		return err
	}
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	lifecycletest.EveryPair(t, exception.Lifecycle, lifecycletest.Harness{
		Seed: func(t *testing.T, state string) string {
			created, err := store.Create(ctx, validCreate(ctrl, "key_requester"))
			must(t, err)
			id := created.ID.String()
			switch state {
			case exception.StateApproved:
				must(t, approve(t, id))
			case exception.StateDenied:
				must(t, deny(t, id))
			case exception.StateActive:
				must(t, approve(t, id))
				must(t, activate(t, id))
			case exception.StateExpired:
				must(t, approve(t, id))
				must(t, activate(t, id))
				must(t, expire(t, id))
			}
			return id
		},
		Moves: map[string]func(*testing.T, string) error{
			"approve":  approve,
			"deny":     deny,
			"activate": activate,
			"expire":   expire,
		},
		State: func(t *testing.T, id string) string {
			got, err := store.Get(ctx, uuid.MustParse(id))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return got.Status
		},
	})
}
