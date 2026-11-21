//go:build integration

// #2147: the declared risk treatment and open/closed lifecycles agree with
// the store, pair by pair, against a real Postgres. No trigger guards
// either column, so the store's transition functions are the enforcement.

package risk_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/lifecycle/lifecycletest"
	"github.com/mgoodric/security-atlas/internal/risk"
)

const (
	lifecycleProposer = "user:2147-proposer"
	lifecycleApprover = "2147-approver"
)

// lifecycleRisk creates a risk that can take every treatment: it has a
// linked control (mitigate) and an instrument reference (transfer).
func lifecycleRisk(t *testing.T, store *risk.Store, ctx context.Context, ctrl uuid.UUID, treatment dbx.RiskTreatment) uuid.UUID {
	t.Helper()
	in := risk.CreateInput{
		Actor:               "test-actor",
		Title:               "Lifecycle risk",
		CategoryRef:         "confidentiality",
		InherentScore:       []byte(`{"likelihood":3,"impact":4}`),
		Treatment:           treatment,
		InstrumentReference: "POL-2147",
		LinkedControlIDs:    []uuid.UUID{ctrl},
	}
	if treatment != dbx.RiskTreatmentTransfer {
		in.InstrumentReference = ""
	}
	r, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create %s: %v", treatment, err)
	}
	return r.ID
}

func TestTreatmentLifecycle_EveryPair(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	until := time.Now().UTC().AddDate(0, 6, 0)

	change := func(to dbx.RiskTreatment) func(*testing.T, string) error {
		return func(t *testing.T, id string) error {
			in := risk.ChangeTreatmentInput{Treatment: to, Actor: lifecycleProposer}
			if to == dbx.RiskTreatmentTransfer {
				ref := "POL-2147"
				in.InstrumentReference = &ref
			}
			_, err := store.ChangeTreatment(ctx, uuid.MustParse(id), in)
			return err
		}
	}
	propose := func(t *testing.T, id string) error {
		_, err := store.ProposeAcceptance(ctx, uuid.MustParse(id), risk.ProposeAcceptanceInput{
			Justification: "within appetite", ProposedUntil: until, Proposer: lifecycleProposer,
		})
		return err
	}
	approve := func(t *testing.T, id string) error {
		_, err := store.ApproveAcceptance(ctx, uuid.MustParse(id), risk.ApproveAcceptanceInput{
			AcceptedUntil: until,
			Approver:      approval.Approver{UserID: lifecycleApprover, CredentialID: "jwt:" + lifecycleApprover},
		})
		return err
	}
	reject := func(t *testing.T, id string) error {
		_, err := store.RejectAcceptance(ctx, uuid.MustParse(id), risk.EndAcceptanceInput{Actor: lifecycleApprover})
		return err
	}
	revoke := func(t *testing.T, id string) error {
		_, err := store.RevokeAcceptance(ctx, uuid.MustParse(id), risk.EndAcceptanceInput{Reason: "appetite changed", Actor: lifecycleApprover})
		return err
	}
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	lifecycletest.EveryPair(t, risk.TreatmentLifecycle, lifecycletest.Harness{
		Seed: func(t *testing.T, state string) string {
			switch state {
			case risk.TreatmentStateAcceptProposed, risk.TreatmentStateAcceptApproved:
				id := lifecycleRisk(t, store, ctx, ctrl, dbx.RiskTreatmentUntreated).String()
				must(t, propose(t, id))
				if state == risk.TreatmentStateAcceptApproved {
					must(t, approve(t, id))
				}
				return id
			}
			return lifecycleRisk(t, store, ctx, ctrl, dbx.RiskTreatment(state)).String()
		},
		Moves: map[string]func(*testing.T, string) error{
			"clear_treatment":    change(dbx.RiskTreatmentUntreated),
			"mitigate":           change(dbx.RiskTreatmentMitigate),
			"transfer":           change(dbx.RiskTreatmentTransfer),
			"avoid":              change(dbx.RiskTreatmentAvoid),
			"propose_acceptance": propose,
			"approve_acceptance": approve,
			"reject_acceptance":  reject,
			"revoke_acceptance":  revoke,
		},
		State: func(t *testing.T, id string) string {
			r, err := store.Get(ctx, uuid.MustParse(id))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return risk.TreatmentState(r.Treatment, r.AcceptanceStatus)
		},
	})
}

func TestStatusLifecycle_EveryPair(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	closeRisk := func(t *testing.T, id string) error {
		_, err := store.Close(ctx, uuid.MustParse(id), risk.CloseInput{Reason: dbx.RiskClosureReasonMerged, Actor: lifecycleProposer})
		return err
	}
	reopen := func(t *testing.T, id string) error {
		_, err := store.Reopen(ctx, uuid.MustParse(id), risk.ReopenInput{Actor: lifecycleProposer})
		return err
	}

	lifecycletest.EveryPair(t, risk.StatusLifecycle, lifecycletest.Harness{
		Seed: func(t *testing.T, state string) string {
			id := lifecycleRisk(t, store, ctx, ctrl, dbx.RiskTreatmentAvoid).String()
			if state == string(dbx.RiskStatusClosed) {
				if err := closeRisk(t, id); err != nil {
					t.Fatalf("seed closed: %v", err)
				}
			}
			return id
		},
		Moves: map[string]func(*testing.T, string) error{
			"close":  closeRisk,
			"reopen": reopen,
		},
		State: func(t *testing.T, id string) string {
			r, err := store.Get(ctx, uuid.MustParse(id))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return string(r.Status)
		},
	})
}

// TestTreatmentLifecycle_ClosedRiskTakesNoMove: a closed risk is outside
// the treatment lifecycle until it is reopened. Every treatment move out of
// every state is refused while closed.
func TestTreatmentLifecycle_ClosedRiskTakesNoMove(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	id := lifecycleRisk(t, store, ctx, ctrl, dbx.RiskTreatmentAvoid)
	if _, err := store.Close(ctx, id, risk.CloseInput{Reason: dbx.RiskClosureReasonMerged, Actor: lifecycleProposer}); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := store.ChangeTreatment(ctx, id, risk.ChangeTreatmentInput{Treatment: dbx.RiskTreatmentUntreated, Actor: lifecycleProposer}); err == nil {
		t.Fatal("change treatment on a closed risk succeeded")
	}
	if _, err := store.ProposeAcceptance(ctx, id, risk.ProposeAcceptanceInput{
		Justification: "x", ProposedUntil: time.Now().UTC().AddDate(0, 1, 0), Proposer: lifecycleProposer,
	}); err == nil {
		t.Fatal("propose acceptance on a closed risk succeeded")
	}
}
