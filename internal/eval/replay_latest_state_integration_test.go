//go:build integration

// #3071: a replay of an earlier instant T is written to control_evaluations
// with evaluated_at = the clock of the run and evaluated_as_of = T. It is not
// the control's state now, so ControlState and the controls list behind the
// dashboard and needs-attention keep serving the latest live run.

package eval_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// runStates is the control's latest run with the given trigger, one State
// per cell in ControlState's order. A run as of an earlier instant (a
// replay, or any run whose horizon is before its clock) is not latest state
// (#3071), so a test that checks what such a run computed reads it here,
// not via ControlState.
func runStates(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl uuid.UUID, trigger string) []eval.State {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT scope_cell_id, result::text, freshness_status, evidence_count_in_window, evaluated_at, trigger
		FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2
		  AND eval_run_id = (
		      SELECT eval_run_id FROM control_evaluations
		      WHERE tenant_id = $1 AND control_id = $2 AND trigger = $3
		      ORDER BY evaluated_at DESC, created_at DESC LIMIT 1)
		ORDER BY scope_cell_id`, tenant, ctrl, trigger)
	if err != nil {
		t.Fatalf("read %s run: %v", trigger, err)
	}
	defer rows.Close()
	var out []eval.State
	for rows.Next() {
		s := eval.State{ControlID: ctrl}
		var cell *uuid.UUID
		if err := rows.Scan(&cell, &s.Result, &s.FreshnessStatus, &s.EvidenceCountInWindow, &s.EvaluatedAt, &s.Trigger); err != nil {
			t.Fatalf("scan replay row: %v", err)
		}
		s.ScopeCellID = cell
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("replay rows: %v", err)
	}
	return out
}

// listedResult is the control's state_result in ListActiveControlsWithState,
// the read behind GET /v1/controls (the dashboard's control list) and
// needs-attention.
func listedResult(t *testing.T, app *pgxpool.Pool, tenant string, ctrl uuid.UUID) string {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
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
	rows, err := dbx.New(tx).ListActiveControlsWithState(ctx, dbx.ListActiveControlsWithStateParams{
		TenantID: pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true},
		SortKey:  "default",
		RowLimit: math.MaxInt32,
	})
	if err != nil {
		t.Fatalf("ListActiveControlsWithState: %v", err)
	}
	for _, r := range rows {
		if uuid.UUID(r.ID.Bytes) != ctrl {
			continue
		}
		if r.StateResult == nil {
			return ""
		}
		return string(*r.StateResult)
	}
	t.Fatalf("control %s not listed", ctrl)
	return ""
}

func TestReplay_OfAnEarlierInstantIsNotTheLatestState(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	eng := newEngine(app)

	// One record five days old, judged by a 24 h window: live it fails; as
	// of one hour after it was observed it passes.
	observed := time.Now().UTC().Add(-5 * 24 * time.Hour).Truncate(time.Second)
	queriesJSON := `[{"id":"within-24h","language":"sql","expression":` + jsonString(sqlWithin24h) + `}]`
	ctrl := seedControl(t, admin, tenant, "automated", "monthly", queriesJSON)
	seedEvidence(t, admin, tenant, ctrl, "pass", observed)

	if _, err := eng.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("live EvaluateControl: %v", err)
	}
	asOf := observed.Add(time.Hour)
	if _, err := eng.EvaluateControl(ctx, ctrl, eval.TriggerReplay, asOf); err != nil {
		t.Fatalf("replay EvaluateControl: %v", err)
	}

	// The replay row exists, judged T, and computed T's result.
	replayed := runStates(t, admin, tenant, ctrl, eval.TriggerReplay)
	if len(replayed) != 1 || replayed[0].Result != eval.ResultPass {
		t.Fatalf("replay of T = %+v, want one pass row", replayed)
	}
	var judged *time.Time
	if err := admin.QueryRow(context.Background(), `
		SELECT evaluated_as_of FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2 AND trigger = 'replay'`, tenant, ctrl).Scan(&judged); err != nil {
		t.Fatalf("read replay evaluated_as_of: %v", err)
	}
	if judged == nil || !judged.Equal(asOf) {
		t.Fatalf("replay evaluated_as_of = %v, want %s", judged, asOf)
	}

	// The control's state now is still the live run's.
	states, err := eng.ControlState(ctx, ctrl, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(states) != 1 || states[0].Result != eval.ResultFail || states[0].Trigger != eval.TriggerManual {
		t.Fatalf("ControlState after replay = %+v, want the live manual fail", states)
	}
	if got := listedResult(t, app, tenant, ctrl); got != eval.ResultFail {
		t.Fatalf("controls list state_result after replay = %q, want the live fail", got)
	}
}
