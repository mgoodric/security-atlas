//go:build integration

// #3038: a live evaluation's evaluated_at and a ledger row's ingested_at come
// from one clock, the database's, so a row ingested just before the live
// evaluation at T is counted by it and by a replay of T, whatever the app
// host's clock says.

package eval_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
)

// latestEvaluation is the control's most recent row for trigger.
func latestEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl uuid.UUID, trigger string) (count int, evaluatedAt time.Time) {
	t.Helper()
	if err := admin.QueryRow(context.Background(), `
		SELECT evidence_count_in_window, evaluated_at FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2 AND trigger = $3
		ORDER BY evaluated_at DESC LIMIT 1`, tenant, ctrl, trigger).Scan(&count, &evaluatedAt); err != nil {
		t.Fatalf("latest %s evaluation: %v", trigger, err)
	}
	return count, evaluatedAt
}

func ingestedAt(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) time.Time {
	t.Helper()
	var at time.Time
	if err := admin.QueryRow(context.Background(), `SELECT ingested_at FROM evidence_records WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("ingested_at: %v", err)
	}
	return at
}

// liveThenReplay ingests one row, evaluates the control live, then replays
// it as of the live evaluation's evaluated_at. It returns both evidence
// counts, the live evaluated_at, and the row's ingested_at.
func liveThenReplay(t *testing.T, admin *pgxpool.Pool, eng *eval.Engine, tenant string) (live, replay int, at, ingested time.Time) {
	t.Helper()
	ctx := ctxFor(t, tenant)
	ctrl := seedControl(t, admin, tenant, "automated", "monthly", "[]")
	row := seedEvidenceWithPayload(t, admin, tenant, ctrl, "pass", time.Now().UTC().Add(-time.Hour), `{}`)
	if _, err := eng.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("live EvaluateControl: %v", err)
	}
	live, at = latestEvaluation(t, admin, tenant, ctrl, eval.TriggerManual)
	if _, err := eng.EvaluateControl(ctx, ctrl, eval.TriggerReplay, at); err != nil {
		t.Fatalf("replay EvaluateControl: %v", err)
	}
	replay, _ = latestEvaluation(t, admin, tenant, ctrl, eval.TriggerReplay)
	return live, replay, at, ingestedAt(t, admin, row)
}

func TestEvaluateControl_LiveAndReplayShareTheDatabaseClock(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)

	// The hazard: an app clock a minute behind the database (equivalently,
	// a database clock running ahead). The row was written just before the
	// evaluation, but its ingested_at, stamped by the database, is after the
	// app's T, so a replay of T leaves it out. Live evaluation applies the
	// same horizon, so the two agree, but neither counts the row.
	skewed := newEngine(app)
	eval.SetAppClockForTest(skewed, func() time.Time { return time.Now().Add(-time.Minute) })
	live, replay, at, ingested := liveThenReplay(t, admin, skewed, tenant)
	if !ingested.After(at) || live != 0 || replay != 0 {
		t.Fatalf("app clock behind the database: ingested_at %s, T %s, live %d, replay %d; want the row after T and counted by neither",
			ingested, at, live, replay)
	}

	// The engine's own clock is the database's: T is read after the row
	// committed, so the live result at T and a replay of T both count it.
	live, replay, at, ingested = liveThenReplay(t, admin, newEngine(app), tenant)
	if ingested.After(at) {
		t.Fatalf("ingested_at %s after the live evaluated_at %s", ingested, at)
	}
	if live != 1 || replay != 1 {
		t.Fatalf("live counted %d, replay of T counted %d; want the row ingested just before T in both", live, replay)
	}
}
