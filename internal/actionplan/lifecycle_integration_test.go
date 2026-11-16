//go:build integration

// #2146: the declared action-plan lifecycle agrees with
// action_plans_status_transition_guard on every from -> to pair, against a
// real Postgres. Every action-plan move but verify is the same status
// PATCH, so the trigger (which covers every state) is the check; the store's
// AllowedTransition is held to the table by TestLifecycle_MatchesAllowedTransition.

package actionplan_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/lifecycle/lifecycletest"
)

func TestLifecycle_TriggerPairs(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	verifier := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	paths := map[string][]string{
		actionplan.StatusInProgress: {actionplan.StatusInProgress},
		actionplan.StatusBlocked:    {actionplan.StatusInProgress, actionplan.StatusBlocked},
		actionplan.StatusCompleted:  {actionplan.StatusInProgress, actionplan.StatusCompleted},
		actionplan.StatusVerified:   {actionplan.StatusInProgress, actionplan.StatusCompleted, actionplan.StatusVerified},
	}
	lifecycletest.TriggerPairs(t, actionplan.Lifecycle,
		func(t *testing.T, state string) string {
			p, err := store.Create(ctx, validCreate(owner))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			for _, s := range paths[state] {
				s := s
				var err error
				if s == actionplan.StatusVerified {
					// #2108: verified is reached through Verify, not PATCH.
					_, err = store.Verify(ctx, p.ID, approval.Approver{UserID: verifier.String()})
				} else {
					_, err = store.Update(ctx, p.ID, actionplan.UpdateInput{Status: &s, Actor: owner})
				}
				if err != nil {
					t.Fatalf("seed %s: -> %s: %v", state, s, err)
				}
			}
			return p.ID.String()
		},
		func(t *testing.T, id, to string) error {
			_, err := admin.Exec(context.Background(),
				`UPDATE action_plans SET status = $2 WHERE id = $1`, id, to)
			return err
		},
		func(t *testing.T, id string) string {
			p, err := store.Get(ctx, uuid.MustParse(id))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return p.Status
		})
}
