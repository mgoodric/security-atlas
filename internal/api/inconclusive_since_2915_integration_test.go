//go:build integration

// #2915 — a control that has been inconclusive for a while has a signal of
// its own: "inconclusive since <date>" on GET /v1/controls/{id}/state, and
// a needs-attention item in band `inconclusive` once the run is
// needsattention.InconclusiveDays old. Real Postgres under RLS, through the
// production router.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/needsattention"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

type fixture2915 struct {
	tenant string
	owner  uuid.UUID
	cell   uuid.UUID
	now    time.Time
	day    time.Duration
	// The controls, by the shape of their ledger.
	alwaysInconclusive uuid.UUID // inconclusive at -20d, -10d, -1h: since -20d
	afterPass          uuid.UUID // pass -20d, inconclusive -9d, -1h: since -9d
	recovered          uuid.UUID // inconclusive -20d, pass -1h: no since
	recent             uuid.UUID // pass -20d, inconclusive -2d: since -2d, under the threshold
	twoCells           uuid.UUID // NULL cell inconclusive since -12d; cell inconclusive since -30d
}

func seed2915(t *testing.T, admin *pgxpool.Pool) fixture2915 {
	t.Helper()
	ctx := context.Background()
	fx := fixture2915{
		tenant: dbtest.SeedTenant(t, admin,
			"control_owner_assignments", "control_evaluations", "controls", "scope_cells", "users"),
		owner: uuid.New(),
		cell:  uuid.New(),
		now:   time.Now().UTC().Truncate(time.Microsecond),
		day:   24 * time.Hour,
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'Owner 2915', 'active')`,
		fx.owner, fx.tenant, "owner-2915-"+fx.owner.String()[:8]+"@test.example")
	exec("scope cell", `INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash) VALUES ($1, $2, 'cell 2915', '{"env":"prod"}'::jsonb, $3)`,
		fx.cell, fx.tenant, "hash-2915-"+fx.cell.String())

	control := func(title string) uuid.UUID {
		id := uuid.New()
		exec("control "+title, `
			INSERT INTO controls (
				id, tenant_id, title, control_family, implementation_type,
				bundle_id, evidence_queries, applicability_expr, lifecycle_state
			)
			VALUES ($1, $2, $3, 'AAA', 'automated', $4, '[]'::jsonb, 'true', 'active')
		`, id, fx.tenant, title, "test-bundle-2915-"+id.String())
		exec("owner", `INSERT INTO control_owner_assignments (tenant_id, control_id, owner_user_id, assigned_by) VALUES ($1, $2, $3, $3)`,
			fx.tenant, id, fx.owner)
		return id
	}
	evaluate := func(ctrl uuid.UUID, cell *uuid.UUID, result string, ago time.Duration) {
		t.Helper()
		freshness, count := "fresh", 1
		if result == "inconclusive" {
			freshness, count = "no_evidence", 0
		}
		exec("evaluation", `
			INSERT INTO control_evaluations (
				id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
				result, freshness_status, evidence_count_in_window, trigger
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'scheduled')
		`, uuid.New(), fx.tenant, ctrl, cell, uuid.New(), fx.now.Add(-ago), result, freshness, count)
	}
	d := fx.day

	fx.alwaysInconclusive = control("always inconclusive")
	evaluate(fx.alwaysInconclusive, nil, "inconclusive", 20*d)
	evaluate(fx.alwaysInconclusive, nil, "inconclusive", 10*d)
	evaluate(fx.alwaysInconclusive, nil, "inconclusive", time.Hour)

	fx.afterPass = control("inconclusive after a pass")
	evaluate(fx.afterPass, nil, "pass", 20*d)
	evaluate(fx.afterPass, nil, "inconclusive", 9*d)
	evaluate(fx.afterPass, nil, "inconclusive", time.Hour)

	fx.recovered = control("recovered")
	evaluate(fx.recovered, nil, "inconclusive", 20*d)
	evaluate(fx.recovered, nil, "pass", time.Hour)

	fx.recent = control("recently inconclusive")
	evaluate(fx.recent, nil, "pass", 20*d)
	evaluate(fx.recent, nil, "inconclusive", 2*d)

	fx.twoCells = control("two cells")
	evaluate(fx.twoCells, nil, "fail", 13*d)
	evaluate(fx.twoCells, nil, "inconclusive", 12*d)
	evaluate(fx.twoCells, &fx.cell, "inconclusive", 30*d)
	evaluate(fx.twoCells, &fx.cell, "inconclusive", time.Hour)
	return fx
}

// The query on its own, under RLS: every currently inconclusive cell's run
// start, nothing for a recovered cell, and the as-of horizon honoured.
func TestListInconclusiveStreaks_2915(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seed2915(t, admin)
	ctx := context.Background()

	read := func(asOf time.Time) map[[2]uuid.UUID]time.Time {
		t.Helper()
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", fx.tenant); err != nil {
			t.Fatal(err)
		}
		var ids []pgtype.UUID
		for _, id := range []uuid.UUID{fx.alwaysInconclusive, fx.afterPass, fx.recovered, fx.recent, fx.twoCells} {
			ids = append(ids, pgtype.UUID{Bytes: id, Valid: true})
		}
		rows, err := dbx.New(tx).ListInconclusiveStreaks(ctx, dbx.ListInconclusiveStreaksParams{
			TenantID: pgtype.UUID{Bytes: uuid.MustParse(fx.tenant), Valid: true}, ControlIds: ids,
			AsOf: pgtype.Timestamptz{Time: asOf, Valid: true},
		})
		if err != nil {
			t.Fatalf("ListInconclusiveStreaks: %v", err)
		}
		out := map[[2]uuid.UUID]time.Time{}
		for _, r := range rows {
			var cell uuid.UUID
			if r.ScopeCellID.Valid {
				cell = r.ScopeCellID.Bytes
			}
			out[[2]uuid.UUID{r.ControlID.Bytes, cell}] = r.InconclusiveSince.Time
		}
		return out
	}
	d := fx.day
	key := func(ctrl uuid.UUID, cell uuid.UUID) [2]uuid.UUID { return [2]uuid.UUID{ctrl, cell} }

	got := read(fx.now)
	want := map[[2]uuid.UUID]time.Time{
		key(fx.alwaysInconclusive, uuid.Nil): fx.now.Add(-20 * d),
		key(fx.afterPass, uuid.Nil):          fx.now.Add(-9 * d),
		key(fx.recent, uuid.Nil):             fx.now.Add(-2 * d),
		key(fx.twoCells, uuid.Nil):           fx.now.Add(-12 * d),
		key(fx.twoCells, fx.cell):            fx.now.Add(-30 * d),
	}
	if len(got) != len(want) {
		t.Fatalf("streaks = %v\nwant      %v", got, want)
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || !g.Equal(w) {
			t.Errorf("%v: since = %v (found %v); want %v", k, g, ok, w)
		}
	}

	// As of 15 days ago the recovered control was inconclusive (since
	// -20d) and the after-pass one was still passing.
	past := read(fx.now.Add(-15 * d))
	if g := past[key(fx.recovered, uuid.Nil)]; !g.Equal(fx.now.Add(-20 * d)) {
		t.Errorf("as of -15d, recovered since = %v; want -20d", g)
	}
	if _, ok := past[key(fx.afterPass, uuid.Nil)]; ok {
		t.Errorf("as of -15d, after-pass control has a streak; it was passing")
	}
}

// Through the router: the state endpoint carries inconclusive_since per
// cell, and needs-attention lists the controls inconclusive for at least
// InconclusiveDays, dated from the run's start.
func TestInconclusiveSince_2915_StateAndNeedsAttention(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seed2915(t, admin)
	ctx := context.Background()
	t.Cleanup(func() {
		for _, table := range []string{"decision_audit_log", "user_roles"} {
			_, _ = admin.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = $1", fx.tenant)
		}
	})

	engine, err := authz.NewEngine(ctx, authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	if _, err := admin.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'grc_engineer', 'test-2915')`,
		fx.tenant, fx.owner.String()); err != nil {
		t.Fatalf("grant grc_engineer: %v", err)
	}
	c := testjwt.OwnerFor(uuid.MustParse(fx.tenant), []string{"grc_engineer"})
	c.Subject = "user:" + fx.owner.String()
	bearer := srv.IssueTestJWT(t, c)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	get := func(path string, into any) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d body=%s", path, resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatalf("GET %s: decode %v: %s", path, err, body)
		}
	}
	d := fx.day

	type cellState struct {
		ScopeCellID       *string `json:"scope_cell_id"`
		Result            string  `json:"result"`
		InconclusiveSince *string `json:"inconclusive_since"`
	}
	var state struct {
		States []cellState `json:"states"`
	}
	get("/v1/controls/"+fx.twoCells.String()+"/state", &state)
	if len(state.States) != 2 {
		t.Fatalf("two-cell state = %+v; want two cells", state)
	}
	for _, s := range state.States {
		want := fx.now.Add(-12 * d)
		if s.ScopeCellID != nil {
			want = fx.now.Add(-30 * d)
		}
		if s.InconclusiveSince == nil {
			t.Fatalf("cell %v: no inconclusive_since in %+v", s.ScopeCellID, s)
		}
		if g, err := time.Parse(time.RFC3339Nano, *s.InconclusiveSince); err != nil || !g.Equal(want) {
			t.Errorf("cell %v: inconclusive_since = %s; want %s", s.ScopeCellID, *s.InconclusiveSince, want.Format(time.RFC3339Nano))
		}
	}
	var recovered struct {
		States []cellState `json:"states"`
	}
	get("/v1/controls/"+fx.recovered.String()+"/state", &recovered)
	if len(recovered.States) != 1 || recovered.States[0].Result != "pass" || recovered.States[0].InconclusiveSince != nil {
		t.Errorf("recovered state = %+v; want pass with no inconclusive_since", recovered)
	}

	var page needsattention.Response
	get("/v1/needs-attention?for=all&limit=100", &page)
	got := map[string]time.Time{}
	for _, it := range page.Items {
		if it.Band != needsattention.BandInconclusive {
			continue
		}
		if it.Since == nil || it.Owner == nil || it.Owner.UserID != fx.owner.String() {
			t.Errorf("inconclusive item %+v; want a since and the owner", it)
			continue
		}
		got[it.ID] = *it.Since
	}
	want := map[string]time.Time{
		fx.alwaysInconclusive.String(): fx.now.Add(-20 * d),
		fx.afterPass.String():          fx.now.Add(-9 * d),
		fx.twoCells.String():           fx.now.Add(-30 * d), // the earliest cell run
	}
	if len(got) != len(want) {
		t.Fatalf("inconclusive band = %v\nwant              %v", got, want)
	}
	for id, w := range want {
		if !got[id].Equal(w) {
			t.Errorf("%s: since = %v; want %v", id, got[id], w)
		}
	}
	if page.Counts[needsattention.BandInconclusive] != 3 {
		t.Errorf("counts = %v; want 3 inconclusive", page.Counts)
	}
}
