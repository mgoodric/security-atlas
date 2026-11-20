//go:build integration

// Issue #2765 — ListActiveControlsWithState and CountActiveControls find
// each control's latest evaluation per scope cell with an index skip scan
// instead of a DISTINCT ON over the tenant's whole control_evaluations
// ledger. This pins that the rewrite returns exactly what the DISTINCT ON
// did: the oracle below is the replaced latest_eval CTE and its rollup,
// verbatim, and every row and total the store returns must match it.
//
// The fixture covers what the skip scan could get wrong: several cells per
// control plus the NULL whole-tenant cell, ties on evaluated_at broken by
// created_at, a control evaluated only on the NULL cell, one never
// evaluated, a superseded control with evaluations, and a deleted one.

package controls_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// oracleLatestState2765 is the latest_eval CTE ListActiveControlsWithState
// used before #2765, with the same worst-state-wins rollup, restricted to
// active controls. $2 is the optional scope cell.
const oracleLatestState2765 = `
WITH latest_eval AS (
    SELECT DISTINCT ON (ce.tenant_id, ce.control_id, ce.scope_cell_id)
        ce.control_id, ce.scope_cell_id, ce.result, ce.freshness_status,
        ce.last_observed_at, ce.evaluated_at
    FROM control_evaluations ce
    WHERE ce.tenant_id = $1
    ORDER BY ce.tenant_id, ce.control_id, ce.scope_cell_id, ce.evaluated_at DESC, ce.created_at DESC
),
ranked AS (
    SELECT le.control_id,
        MAX(CASE le.result WHEN 'fail' THEN 5 WHEN 'inconclusive' THEN 4
            WHEN 'excepted' THEN 3 WHEN 'pass' THEN 2 WHEN 'na' THEN 1 ELSE 0 END) AS rr,
        MAX(CASE le.freshness_status WHEN 'expired' THEN 4 WHEN 'stale' THEN 3
            WHEN 'no_evidence' THEN 2 WHEN 'fresh' THEN 1 ELSE 0 END) AS fr,
        MAX(le.last_observed_at) AS last_observed_at,
        MAX(le.evaluated_at) AS evaluated_at
    FROM latest_eval le
    WHERE ($2::uuid IS NULL OR le.scope_cell_id = $2::uuid)
    GROUP BY le.control_id
)
SELECT c.id,
    CASE r.rr WHEN 5 THEN 'fail' WHEN 4 THEN 'inconclusive' WHEN 3 THEN 'excepted'
        WHEN 2 THEN 'pass' WHEN 1 THEN 'na' END,
    CASE r.fr WHEN 4 THEN 'expired' WHEN 3 THEN 'stale' WHEN 2 THEN 'no_evidence'
        WHEN 1 THEN 'fresh' END,
    r.last_observed_at, r.evaluated_at
FROM controls c
LEFT JOIN ranked r ON r.control_id = c.id
WHERE c.tenant_id = $1 AND c.superseded_by IS NULL`

type state2765 struct {
	result, freshness, observed, evaluated string
}

func fmtTS2765(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return "null"
	}
	return ts.Time.UTC().Format(time.RFC3339Nano)
}

func fmtStr2765(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

func oracle2765(t *testing.T, admin *pgxpool.Pool, tenant string, cell pgtype.UUID) map[uuid.UUID]state2765 {
	t.Helper()
	rows, err := admin.Query(context.Background(), oracleLatestState2765, tenant, cell)
	if err != nil {
		t.Fatalf("oracle query: %v", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]state2765{}
	for rows.Next() {
		var (
			id                  uuid.UUID
			result, freshness   *string
			observed, evaluated pgtype.Timestamptz
		)
		if err := rows.Scan(&id, &result, &freshness, &observed, &evaluated); err != nil {
			t.Fatalf("oracle scan: %v", err)
		}
		out[id] = state2765{fmtStr2765(result), fmtStr2765(freshness), fmtTS2765(observed), fmtTS2765(evaluated)}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("oracle rows: %v", err)
	}
	return out
}

func TestListWithState2765_MatchesDistinctOnOracle(t *testing.T) {
	t.Parallel()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()

	tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "controls", "scope_cells")
	tag := uuid.NewString()[:8]
	anchor := insertAnchor(t, admin, "IAC-2765", "Identity & Access Mgmt")

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	cells := make([]uuid.UUID, 3)
	for i := range cells {
		cells[i] = uuid.New()
		exec("scope cell", `
			INSERT INTO scope_cells (id, tenant_id, dimensions, dimensions_hash)
			VALUES ($1, $2, $3::jsonb, $4)
		`, cells[i], tenant, fmt.Sprintf(`{"env":"e%d"}`, i), fmt.Sprintf("h-%s-%d", tag, i))
	}

	base := time.Now().UTC().Truncate(time.Microsecond).Add(-48 * time.Hour)
	at := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	// evaluate appends one ledger row. cell uuid.Nil is the NULL cell.
	evaluate := func(ctrl, cell uuid.UUID, evaluatedAt, createdAt time.Time, result, freshness string) {
		t.Helper()
		var c *uuid.UUID
		if cell != uuid.Nil {
			c = &cell
		}
		count := 1
		if freshness == "no_evidence" {
			count = 0
		}
		exec("evaluation", `
			INSERT INTO control_evaluations (
				id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at, created_at,
				result, freshness_status, last_observed_at, evidence_count_in_window, trigger)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $6, $10, 'manual')
		`, uuid.New(), tenant, ctrl, c, uuid.New(), evaluatedAt, createdAt, result, freshness, count)
	}

	// Many cells, history, and a tie on the latest evaluated_at in cell 0:
	// the later created_at (pass) wins over the fail stamped the same instant.
	multi := insertControl(t, admin, tenant, anchor, "Multi cell", "IAC", "c2765-multi-"+tag)
	evaluate(multi, cells[0], at(1), at(1), "fail", "stale")
	evaluate(multi, cells[0], at(5), at(5), "fail", "fresh")
	evaluate(multi, cells[0], at(5), at(6), "pass", "fresh")
	evaluate(multi, cells[1], at(2), at(2), "pass", "fresh")
	evaluate(multi, cells[1], at(3), at(3), "na", "stale")
	evaluate(multi, uuid.Nil, at(4), at(4), "inconclusive", "no_evidence")
	evaluate(multi, uuid.Nil, at(1), at(1), "fail", "fresh")

	// Tie in the NULL cell, the reverse way round: the later created_at is
	// the fail, so the control rolls up fail.
	nullOnly := insertControl(t, admin, tenant, anchor, "Null cell only", "IAC", "c2765-null-"+tag)
	evaluate(nullOnly, uuid.Nil, at(2), at(2), "pass", "fresh")
	evaluate(nullOnly, uuid.Nil, at(7), at(7), "pass", "fresh")
	evaluate(nullOnly, uuid.Nil, at(7), at(8), "fail", "stale")

	// Only non-NULL cells, the last of them (the skip scan's final step).
	lastCell := insertControl(t, admin, tenant, anchor, "Last cell only", "IAC", "c2765-last-"+tag)
	evaluate(lastCell, cells[2], at(3), at(3), "pass", "fresh")
	evaluate(lastCell, cells[2], at(9), at(9), "excepted", "fresh")
	evaluate(lastCell, cells[1], at(1), at(1), "pass", "stale")

	insertControl(t, admin, tenant, anchor, "Never evaluated", "IAC", "c2765-none-"+tag)

	// Superseded: evaluations exist, the control must not list.
	superseded := insertControl(t, admin, tenant, anchor, "Superseded", "IAC", "c2765-sup-"+tag)
	evaluate(superseded, cells[0], at(10), at(10), "fail", "fresh")
	exec("supersede", `UPDATE controls SET superseded_by = $1 WHERE id = $2`, multi, superseded)

	// Deleted: its evaluations cascade away with it.
	deleted := insertControl(t, admin, tenant, anchor, "Deleted", "IAC", "c2765-del-"+tag)
	evaluate(deleted, cells[1], at(10), at(10), "fail", "fresh")
	exec("delete control", `DELETE FROM controls WHERE id = $1`, deleted)

	store := control.NewStore(app)
	tctx := dbtest.WithTenantCtx(t, tenant)
	cellFilter := func(i int) pgtype.UUID { return pgtype.UUID{Bytes: cells[i], Valid: true} }

	cases := []struct {
		name string
		f    control.ListFilter
	}{
		{"unfiltered", control.ListFilter{}},
		{"scope cell 0", control.ListFilter{ScopeCell: cellFilter(0)}},
		{"scope cell 1", control.ListFilter{ScopeCell: cellFilter(1)}},
		{"scope cell 2", control.ListFilter{ScopeCell: cellFilter(2)}},
		{"result fail", control.ListFilter{Result: "fail"}},
		{"result pass", control.ListFilter{Result: "pass"}},
		{"result excepted", control.ListFilter{Result: "excepted"}},
		{"freshness stale", control.ListFilter{Freshness: "stale"}},
		{"freshness no_evidence", control.ListFilter{Freshness: "no_evidence"}},
		{"cell 1 + stale", control.ListFilter{ScopeCell: cellFilter(1), Freshness: "stale"}},
	}
	for _, tc := range cases {
		want := oracle2765(t, admin, tenant, tc.f.ScopeCell)
		for id, s := range want {
			if (tc.f.Result != "" && s.result != tc.f.Result) ||
				(tc.f.Freshness != "" && s.freshness != tc.f.Freshness) {
				delete(want, id)
			}
		}
		rows, total, err := store.ListWithState(tctx, 1000, 0, tc.f)
		if err != nil {
			t.Fatalf("%s: ListWithState: %v", tc.name, err)
		}
		if total != int64(len(want)) {
			t.Errorf("%s: total = %d; oracle has %d", tc.name, total, len(want))
		}
		if len(rows) != len(want) {
			t.Errorf("%s: %d rows; oracle has %d", tc.name, len(rows), len(want))
		}
		for _, r := range rows {
			id := uuid.UUID(r.ID.Bytes)
			var result *string
			if r.StateResult != nil {
				s := string(*r.StateResult)
				result = &s
			}
			got := state2765{fmtStr2765(result), fmtStr2765(r.StateFreshnessStatus),
				fmtTS2765(r.StateLastObservedAt), fmtTS2765(r.StateEvaluatedAt)}
			w, ok := want[id]
			if !ok {
				t.Errorf("%s: control %s (%s) listed; oracle omits it", tc.name, id, r.Title)
				continue
			}
			if got != w {
				t.Errorf("%s: control %s (%s) = %+v; oracle %+v", tc.name, id, r.Title, got, w)
			}
		}
	}

	// The fixture must exercise the ties it claims to: pin the two
	// tie-broken rollups directly, so a fixture that stopped tying would
	// fail here rather than pass vacuously.
	rows, _, err := store.ListWithState(tctx, 1000, 0, control.ListFilter{})
	if err != nil {
		t.Fatalf("ListWithState: %v", err)
	}
	byID := map[uuid.UUID]string{}
	for _, r := range rows {
		if r.StateResult != nil {
			byID[uuid.UUID(r.ID.Bytes)] = string(*r.StateResult)
		}
	}
	if got := byID[nullOnly]; got != "fail" {
		t.Errorf("null-cell tie: result = %q; want fail (later created_at)", got)
	}
	cell0, _, err := store.ListWithState(tctx, 1000, 0, control.ListFilter{ScopeCell: cellFilter(0)})
	if err != nil {
		t.Fatalf("ListWithState cell 0: %v", err)
	}
	for _, r := range cell0 {
		if uuid.UUID(r.ID.Bytes) != multi {
			continue
		}
		got := "null"
		if r.StateResult != nil {
			got = string(*r.StateResult)
		}
		if got != "pass" {
			t.Errorf("cell 0 tie: multi result = %q; want pass (later created_at)", got)
		}
	}
}
