//go:build integration

// #2146: the declared audit-period lifecycle agrees with the store, pair by
// pair, against a real Postgres.

package period_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/lifecycle/lifecycletest"
)

func TestLifecycle_EveryPair(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	store := period.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	freeze := func(t *testing.T, id string) error {
		_, err := store.Freeze(ctx, uuid.MustParse(id), "key_freezer", time.Now().UTC())
		return err
	}
	lifecycletest.EveryPair(t, period.Lifecycle, lifecycletest.Harness{
		Seed: func(t *testing.T, state string) string {
			p, err := store.Create(ctx, period.CreateInput{
				Name:               "Lifecycle " + uuid.NewString(),
				FrameworkVersionID: fwID,
				PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
				PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
				CreatedBy:          "key_creator",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			id := p.ID.String()
			if state == string(period.StatusFrozen) {
				if err := freeze(t, id); err != nil {
					t.Fatalf("seed frozen: %v", err)
				}
			}
			return id
		},
		Moves: map[string]func(*testing.T, string) error{"freeze": freeze},
		State: func(t *testing.T, id string) string {
			p, err := store.Get(ctx, uuid.MustParse(id))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return string(p.Status)
		},
	})
}
