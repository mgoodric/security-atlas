//go:build integration

// #2146: the declared policy lifecycle agrees with the store, pair by pair,
// against a real Postgres.

package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/lifecycle/lifecycletest"
	"github.com/mgoodric/security-atlas/internal/policy"
)

func TestLifecycle_EveryPair(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	create := func(predecessor *uuid.UUID, version string) (string, error) {
		in := validCreate([]uuid.UUID{ctrl}, "key_creator")
		in.Title = "Lifecycle " + uuid.NewString()
		in.Version = version
		in.PredecessorID = predecessor
		p, err := store.Create(ctx, in)
		return p.ID.String(), err
	}
	submit := func(t *testing.T, id string) error {
		_, err := store.SubmitForReview(ctx, uuid.MustParse(id), submitterPerson)
		return err
	}
	approve := func(t *testing.T, id string) error {
		_, err := store.Approve(ctx, uuid.MustParse(id), approverPerson)
		return err
	}
	publish := func(t *testing.T, id string) error {
		_, err := store.Publish(ctx, uuid.MustParse(id), policy.PublishInput{NewVersion: "1.0.0", PublishedBy: "key_publisher"})
		return err
	}
	// supersede takes a next version of id through to published, which
	// supersedes id. A next version can be created only of a published
	// policy.
	supersede := func(t *testing.T, id string) error {
		pred := uuid.MustParse(id)
		next, err := create(&pred, "2.0.0")
		if err != nil {
			return err
		}
		for _, step := range []func(*testing.T, string) error{submit, approve, publish} {
			if err := step(t, next); err != nil {
				t.Fatalf("next version of %s: %v", id, err)
			}
		}
		return nil
	}

	lifecycletest.EveryPair(t, policy.Lifecycle, lifecycletest.Harness{
		Seed: func(t *testing.T, state string) string {
			id, err := create(nil, "1.0.0")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			steps := map[string][]func(*testing.T, string) error{
				policy.StateUnderReview: {submit},
				policy.StateApproved:    {submit, approve},
				policy.StatePublished:   {submit, approve, publish},
				policy.StateSuperseded:  {submit, approve, publish, supersede},
			}[state]
			for _, step := range steps {
				if err := step(t, id); err != nil {
					t.Fatalf("seed %s: %v", state, err)
				}
			}
			return id
		},
		Moves: map[string]func(*testing.T, string) error{
			"submit":    submit,
			"approve":   approve,
			"publish":   publish,
			"supersede": supersede,
		},
		State: func(t *testing.T, id string) string {
			p, err := store.Get(ctx, uuid.MustParse(id))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return p.Status
		},
	})
}
