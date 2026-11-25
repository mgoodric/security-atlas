//go:build integration

// #2911: a SQL evidence query reads the evaluation instant from the constant
// `evidence.evaluated_at` column, set from the engine's evaluation time (the
// as-of horizon for a replay) and from EvaluateFixture's fixed now. Rego's
// time.now_ns() is the same instant (#2890).

package eval_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const sqlWithin24h = `SELECT bool_and(observed_at >= evaluated_at - interval '24 hours') FROM evidence`

const regoWithin24hIntegration = `package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
	every r in input.records {
		time.now_ns() - time.parse_rfc3339_ns(r.observed_at) < 24 * 60 * 60 * 1000000000
	}
}`

func TestEvaluateFixture_SQLPinnedDatePassesIndefinitely(t *testing.T) {
	app := dbtest.NewAppPool(t)
	ctx, err := tenancy.WithTenant(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}

	pinned := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	records := []eval.FixtureRecord{{Result: eval.ResultPass, ObservedAt: pinned.Add(-time.Hour)}}

	for _, tc := range []struct {
		name string
		expr string
		at   time.Time
		want string
	}{
		// Years before the wall clock: only evaluated_at can make this pass.
		{"record one hour before the instant", sqlWithin24h, pinned, eval.ResultPass},
		{"record two days before the instant", sqlWithin24h, pinned.Add(48 * time.Hour), eval.ResultFail},
		{"evaluated_at is exactly the fixture's now", `SELECT bool_and(evaluated_at = '2020-01-01T12:00:00Z'::timestamptz) FROM evidence`, pinned, eval.ResultPass},
	} {
		queries := []eval.FixtureQuery{{Language: "sql", Expression: tc.expr}}
		got, err := eval.EvaluateFixture(ctx, "monthly", queries, records, tc.at, tx)
		if err != nil {
			t.Fatalf("%s: EvaluateFixture: %v", tc.name, err)
		}
		if got.Result != tc.want {
			t.Errorf("%s: result = %q, want %q", tc.name, got.Result, tc.want)
		}
	}
}

// A live evaluation judges the 24 h window at the evaluation time; a replay
// as of an earlier instant judges it at that instant, in both languages.
func TestEvaluateControl_AsOfReplayUsesTheAsOfInstant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	eng := newEngine(app)

	observed := time.Now().UTC().Add(-5 * 24 * time.Hour).Truncate(time.Second)
	asOf := observed.Add(time.Hour)

	for _, tc := range []struct {
		lang, expr string
	}{
		{"sql", sqlWithin24h},
		{"rego", regoWithin24hIntegration},
	} {
		queriesJSON := `[{"id":"within-24h","language":"` + tc.lang + `","expression":` + jsonString(tc.expr) + `}]`
		ctrlID := seedControl(t, admin, tenant, "automated", "monthly", queriesJSON)
		seedEvidence(t, admin, tenant, ctrlID, "pass", observed)

		if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
			t.Fatalf("%s: EvaluateControl live: %v", tc.lang, err)
		}
		live, err := eng.ControlState(ctx, ctrlID, "", eval.FarFuture)
		if err != nil {
			t.Fatalf("%s: ControlState live: %v", tc.lang, err)
		}
		if len(live) != 1 || live[0].Result != eval.ResultFail {
			t.Fatalf("%s: live evaluation of a 5-day-old record = %+v, want fail", tc.lang, live)
		}

		if _, err := eng.EvaluateControl(ctx, ctrlID, eval.TriggerReplay, asOf); err != nil {
			t.Fatalf("%s: EvaluateControl as of %s: %v", tc.lang, asOf, err)
		}
		replayed := runStates(t, admin, tenant, ctrlID, eval.TriggerReplay)
		if len(replayed) != 1 || replayed[0].Result != eval.ResultPass {
			t.Fatalf("%s: replay as of one hour after the record = %+v, want pass", tc.lang, replayed)
		}
	}
}
