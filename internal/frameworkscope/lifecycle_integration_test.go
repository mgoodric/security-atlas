//go:build integration

// #2146: the declared framework-scope lifecycle agrees with the store, and
// with framework_scopes_predicate_frozen, pair by pair, against a real
// Postgres.

package frameworkscope_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/lifecycle/lifecycletest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestLifecycle_EveryPair(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	submit := func(t *testing.T, id string) error {
		_, err := store.Submit(ctx, uuid.MustParse(id), "key_submitter", "key_submitter")
		return err
	}
	approve := func(t *testing.T, id string) error {
		_, err := store.Approve(ctx, frameworkscope.ApproveRequest{
			ID: uuid.MustParse(id), ApproverUserID: uuid.NewString(), Actor: "key_approver",
		})
		return err
	}
	activate := func(t *testing.T, id string) error {
		_, err := store.Activate(ctx, uuid.MustParse(id), "key_activator", time.Now().UTC())
		return err
	}
	// edit changes the predicate; the seeds all start on {"op":"true"}.
	edit := func(t *testing.T, id string) error {
		_, _, err := store.UpdatePredicate(ctx, uuid.MustParse(id), []byte(prodPredicate), "key_editor")
		return err
	}
	create := func(t *testing.T) string {
		d, err := store.Create(ctx, frameworkscope.CreateRequest{
			FrameworkVersionID: fvID, Name: "lifecycle " + uuid.NewString(), Predicate: []byte(`{"op":"true"}`),
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		return d.ID.String()
	}
	walk := func(t *testing.T, id string, steps ...func(*testing.T, string) error) {
		t.Helper()
		for _, step := range steps {
			if err := step(t, id); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}
	// supersede activates another revision for the same framework
	// version, which supersedes the activated one (if any).
	supersede := func(t *testing.T, _ string) error {
		walk(t, create(t), submit, approve, activate)
		return nil
	}
	seed := func(t *testing.T, state string) string {
		id := create(t)
		steps := map[string][]func(*testing.T, string) error{
			frameworkscope.StateReview:     {submit},
			frameworkscope.StateApproved:   {submit, approve},
			frameworkscope.StateActivated:  {submit, approve, activate},
			frameworkscope.StateSuperseded: {submit, approve, activate, supersede},
		}[state]
		walk(t, id, steps...)
		return id
	}
	state := func(t *testing.T, id string) string {
		got, err := store.Get(ctx, uuid.MustParse(id))
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return got.State
	}

	t.Run("store", func(t *testing.T) {
		lifecycletest.EveryPair(t, frameworkscope.Lifecycle, lifecycletest.Harness{
			Seed: seed,
			Moves: map[string]func(*testing.T, string) error{
				"submit":    submit,
				"approve":   approve,
				"edit":      edit,
				"activate":  activate,
				"supersede": supersede,
			},
			State: state,
		})
	})

	// The trigger fires for every role, the table owner included, so a
	// direct UPDATE through the migrate pool is the strongest attempt.
	t.Run("trigger", func(t *testing.T) {
		lifecycletest.TriggerPairs(t, frameworkscope.Lifecycle, seed,
			func(t *testing.T, id, to string) error {
				_, err := admin.Exec(context.Background(),
					`UPDATE framework_scopes SET state = $2, updated_at = now() WHERE id = $1`, id, to)
				return err
			},
			state)
	})
}
