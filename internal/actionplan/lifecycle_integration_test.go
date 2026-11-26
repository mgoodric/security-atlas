//go:build integration

// #2146: the declared action-plan lifecycle agrees with
// action_plans_status_transition_guard on every from -> to pair, against a
// real Postgres. Every action-plan move but verify is the same status
// PATCH, so the trigger (which covers every state) is the check; the store's
// AllowedTransition is held to the table by TestLifecycle_MatchesAllowedTransition.

package actionplan_test

import (
	"context"
	"regexp"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/lifecycle"
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
			// #2109: verified -> completed is the system's move, which the
			// trigger admits only as the reconcile function's owner with
			// its marker set. The migrate pool is that owner; the marker
			// is the per-call nonce action_plans_reconcile_verification
			// writes and names in the GUC (#2458).
			// TestReconcile_TriggerRefusesUnverifyOutsideTheFunction proves
			// the app role cannot do the same.
			tx, err := admin.Begin(context.Background())
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			nonce := uuid.New()
			if _, err := tx.Exec(context.Background(),
				`INSERT INTO action_plan_system_move_nonces (nonce) VALUES ($1)`, nonce); err != nil {
				return err
			}
			if _, err := tx.Exec(context.Background(),
				`SELECT set_config('atlas.action_plan_system_move', $1, true)`, nonce.String()); err != nil {
				return err
			}
			if _, err := tx.Exec(context.Background(),
				`UPDATE action_plans SET status = $2 WHERE id = $1`, id, to); err != nil {
				return err
			}
			if _, err := tx.Exec(context.Background(),
				`DELETE FROM action_plan_system_move_nonces WHERE nonce = $1`, nonce); err != nil {
				return err
			}
			return tx.Commit(context.Background())
		},
		func(t *testing.T, id string) string {
			p, err := store.Get(ctx, uuid.MustParse(id))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return p.Status
		})
}

// TestLifecycle_SystemMovesRefusedToAPerson (#2147): every move the table
// marks actor=system is refused when a person asks for it through the
// store's status update, and the plan stays where it was. The trigger
// refuses the same move to the app role outside the reconcile function
// (TestReconcile_TriggerRefusesUnverifyOutsideTheFunction).
func TestLifecycle_SystemMovesRefusedToAPerson(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	verifier := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	reach := map[string]func(t *testing.T, id uuid.UUID){
		actionplan.StatusVerified: func(t *testing.T, id uuid.UUID) {
			for _, s := range []string{actionplan.StatusInProgress, actionplan.StatusCompleted} {
				s := s
				if _, err := store.Update(ctx, id, actionplan.UpdateInput{Status: &s, Actor: owner}); err != nil {
					t.Fatalf("-> %s: %v", s, err)
				}
			}
			if _, err := store.Verify(ctx, id, approval.Approver{UserID: verifier.String()}); err != nil {
				t.Fatalf("verify: %v", err)
			}
		},
	}
	var system int
	for _, tr := range actionplan.Lifecycle.Transitions {
		if tr.Actor != lifecycle.ActorSystem {
			continue
		}
		system++
		if len(tr.Routes) != 0 || len(tr.Roles) != 0 {
			t.Errorf("%s -> %s is the system's but names routes %v, roles %v", tr.From, tr.To, tr.Routes, tr.Roles)
		}
		p, err := store.Create(ctx, validCreate(owner))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if reach[tr.From] == nil {
			t.Fatalf("no way to seed a %s plan", tr.From)
		}
		reach[tr.From](t, p.ID)
		to := tr.To
		for _, actor := range []uuid.UUID{owner, verifier} {
			if _, err := store.Update(ctx, p.ID, actionplan.UpdateInput{Status: &to, Actor: actor}); err == nil {
				t.Errorf("%s -> %s by a person succeeded", tr.From, tr.To)
			}
		}
		got, err := store.Get(ctx, p.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status != tr.From {
			t.Errorf("%s -> %s refused, but the plan is %s", tr.From, tr.To, got.Status)
		}
	}
	if system == 0 {
		t.Fatal("the action-plan table marks no move as the system's")
	}
}

// TestLifecycle_StatesAreTheStatusCheck (#2147): the table's states are
// exactly the values the action_plans.status CHECK admits, so a status
// added to the CHECK fails here until the table draws it.
func TestLifecycle_StatesAreTheStatusCheck(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	var def string
	if err := admin.QueryRow(context.Background(), `
		SELECT pg_get_constraintdef(c.oid)
		  FROM pg_constraint c
		 WHERE c.conrelid = 'action_plans'::regclass
		   AND c.contype = 'c'
		   AND pg_get_constraintdef(c.oid) LIKE '%status%'
		   AND pg_get_constraintdef(c.oid) LIKE '%ARRAY%'`).Scan(&def); err != nil {
		t.Fatalf("read the status CHECK: %v", err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'::text`).FindAllStringSubmatch(def, -1) {
		got = append(got, m[1])
	}
	want := slices.Clone(actionplan.Lifecycle.States)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("status CHECK admits %v (%s), the table declares %v", got, def, want)
	}
}
