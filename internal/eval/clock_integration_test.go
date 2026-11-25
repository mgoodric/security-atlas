//go:build integration

// #3038: a live evaluation's evaluated_at and a ledger row's ingested_at come
// from one clock, the database's, so a row ingested just before the live
// evaluation at T is counted by it and by a replay of T, whatever the app
// host's clock says.

package eval_test

import (
	"context"
	"fmt"
	"os"
	"strings"
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

// laggingDBClockPool is an app-role pool whose sessions resolve an
// unqualified clock_timestamp() to a shadow function that runs lag behind
// the real one: a database clock the app host's clock disagrees with. Only
// clock_timestamp is shadowed; ingested_at's DEFAULT now() is unaffected.
func laggingDBClockPool(t *testing.T, admin *pgxpool.Pool, lag time.Duration) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	schema := "clock3060_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE SCHEMA ` + schema,
		fmt.Sprintf(`CREATE FUNCTION %s.clock_timestamp() RETURNS timestamptz LANGUAGE sql VOLATILE
			AS $$ SELECT pg_catalog.clock_timestamp() - interval '%d seconds' $$`, schema, int(lag.Seconds())),
		`GRANT USAGE ON SCHEMA ` + schema + ` TO PUBLIC`,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Logf("drop %s: %v", schema, err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DATABASE_URL_APP: %v", err)
	}
	// pg_catalog is searched where it is listed, so the shadow schema ahead
	// of it wins for an unqualified call.
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + `, pg_catalog, "$user", public`
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// #3060: the test above cannot tell the engine's database clock from the app
// clock when the two agree, so a revert to the app clock would pass it. Here
// the database clock lags the app's by half an hour. The engine must take T
// from the database: T is then before the row's ingested_at, and neither the
// live result nor a replay of T counts the row. An engine on the app clock
// would take T as now and count it.
func TestEvaluateControl_EvaluatedAtIsTheDatabaseClock(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	const lag = 30 * time.Minute
	live, replay, at, ingested := liveThenReplay(t, admin, newEngine(laggingDBClockPool(t, admin, lag)), tenant)
	if behind := time.Since(at); behind < lag-5*time.Minute {
		t.Fatalf("evaluated_at %s is %s behind now; want about %s, the lagging database clock (the engine read the app clock?)", at, behind, lag)
	}
	if !ingested.After(at) || live != 0 || replay != 0 {
		t.Fatalf("ingested_at %s, T %s, live %d, replay %d; want the row after T and counted by neither", ingested, at, live, replay)
	}
}
