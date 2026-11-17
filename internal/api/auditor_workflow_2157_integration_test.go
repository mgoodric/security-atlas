//go:build integration

// #2157 — an assigned auditor's workflow through the production middleware
// chain (JWT, tenancy, OPA authz with the DB roles + attrs resolvers and the
// resource-attrs hydrators). A real-login auditor ("user:<uuid>" subject, a
// user_roles auditor row) is assigned a period by an admin over
// POST /v1/audit-periods/{id}/auditors (#2118), then reads the period's
// populations, draws and annotates a sample, and reads the evidence those
// populations contain. Everything outside the assignment is refused: a
// second, unassigned period's population, sample and evidence; evidence of
// the assigned control in a scope cell the population does not select or
// outside its window; a list without control_id, or whose since/until
// (#2424) are missing or not inside an assigned population's window and
// freeze horizon. The matrix also pins
// that an admin keeps every read and a viewer gains none.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// seed2157 holds the fixture: two periods, their populations' controls,
// two scope cells and the evidence the matrix reads.
type seed2157 struct {
	assigned, unassigned uuid.UUID
	// cellCtl has a population selecting only the prod cell, attached to
	// the assigned period.
	cellCtl uuid.UUID
	// wholeCtl has a match-every-cell population in the assigned period;
	// otherCtl one in the unassigned period. frozenCtl has one in the
	// assigned period frozen at 2026-03-01, inside its window (#2424).
	wholeCtl, otherCtl, frozenCtl uuid.UUID
	// inScope is cellCtl evidence in the prod cell inside the window;
	// outOfCell in the dev cell; outOfWindow in prod before the window;
	// whole is wholeCtl's; other is otherCtl's.
	inScope, outOfCell, outOfWindow, whole, other uuid.UUID
}

func seedPeriod2157(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	fwID, versionID, periodID := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, '#2157 framework', $2, 'test')`, []any{fwID, "test-2157-" + fwID.String()}},
		{`INSERT INTO framework_versions (id, tenant_id, framework_id, version) VALUES ($1, NULL, $2, '1.0')`, []any{versionID, fwID}},
		{`INSERT INTO audit_periods (id, tenant_id, name, framework_version_id, period_start, period_end, status, created_by)
		  VALUES ($1, $2, $3, $4, '2026-01-01', '2026-03-31', 'open', 'test-2157')`, []any{periodID, tenant, "#2157 period " + periodID.String(), versionID}},
	} {
		if _, err := admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed period: %v", err)
		}
	}
	return periodID
}

func seedControl2157(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type,
		                      bundle_id, freshness_class, evidence_queries, applicability_expr)
		VALUES ($1, $2, '#2157 control', 'AST', 'automated', $3, 'monthly', '[]'::jsonb, 'true')`,
		id, tenant, "bundle-2157-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// seedCell2157 writes a scope cell and its scopes mirror (the
// evidence_records.scope_id FK target, same id).
func seedCell2157(t *testing.T, admin *pgxpool.Pool, tenant, env string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if _, err := admin.Exec(ctx,
		`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
		 VALUES ($1, $2, $3, jsonb_build_object('environment', $3::text), $4)`,
		id, tenant, env, "hash-2157-"+id.String()); err != nil {
		t.Fatalf("seed scope cell: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO scopes (id, tenant_id) VALUES ($1, $2)`, id, tenant); err != nil {
		t.Fatalf("seed scopes mirror: %v", err)
	}
	return id
}

func seedEvidence2157(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl, cell uuid.UUID, observed time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (id, tenant_id, control_id, scope_id, observed_at, ingested_at,
		                              provenance, result, payload, hash, control_ref)
		VALUES ($1, $2, $3, $4, $5, now(), jsonb_build_object('evidence_kind', 'test.2157'), 'pass', '{}'::jsonb, $6, $7)`,
		id, tenant, ctrl, cell, observed, "hash-2157-"+id.String(), ctrl.String()); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

func TestAuditorWorkflow2157_AssignedPeriodThroughAuthzmw(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin)
	tid := uuid.MustParse(tenant)

	var s seed2157
	s.assigned = seedPeriod2157(t, admin, tenant)
	s.unassigned = seedPeriod2157(t, admin, tenant)
	s.cellCtl = seedControl2157(t, admin, tenant)
	s.wholeCtl = seedControl2157(t, admin, tenant)
	s.otherCtl = seedControl2157(t, admin, tenant)
	s.frozenCtl = seedControl2157(t, admin, tenant)
	prod := seedCell2157(t, admin, tenant, "prod")
	dev := seedCell2157(t, admin, tenant, "dev")
	inWindow := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	s.inScope = seedEvidence2157(t, admin, tenant, s.cellCtl, prod, inWindow)
	s.outOfCell = seedEvidence2157(t, admin, tenant, s.cellCtl, dev, inWindow)
	s.outOfWindow = seedEvidence2157(t, admin, tenant, s.cellCtl, prod, inWindow.AddDate(-1, 0, 0))
	s.whole = seedEvidence2157(t, admin, tenant, s.wholeCtl, dev, inWindow)
	s.other = seedEvidence2157(t, admin, tenant, s.otherCtl, prod, inWindow)

	auditorUser := seedPeriodUser(t, admin, tenant, "active", "auditor")
	adminUser := seedPeriodUser(t, admin, tenant, "active", "admin")
	viewerUser := seedPeriodUser(t, admin, tenant, "active", "viewer")
	// A grc_engineer builds the populations (POST /v1/populations).
	grcUser := seedPeriodUser(t, admin, tenant, "active", "grc_engineer")

	// The real resolvers, as production wires them.
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	engine = engine.WithAttrsResolver(auditor.NewDBAttrsResolver(app))
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	bearer := func(u uuid.UUID, role string) string {
		c := testjwt.OwnerFor(tid, []string{role})
		c.Subject = "user:" + u.String()
		return srv.IssueTestJWT(t, c)
	}
	// Mint before building the handler: the first IssueTestJWT wires the
	// signer, and the JWT middleware mounts only when it is set.
	adminBearer, auditorBearer, viewerBearer := bearer(adminUser, "admin"), bearer(auditorUser, "auditor"), bearer(viewerUser, "viewer")
	grcBearer := bearer(grcUser, "grc_engineer")
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	asAdmin := periodAuditorsCall{t, ts.URL, adminBearer}
	asAuditor := periodAuditorsCall{t, ts.URL, auditorBearer}
	asViewer := periodAuditorsCall{t, ts.URL, viewerBearer}
	asGRC := periodAuditorsCall{t, ts.URL, grcBearer}

	// A grc_engineer builds both periods' populations over HTTP and a
	// tenant admin attaches each to its period (#2425, #2428: the attach
	// gate reads the live tenant admin role, as the rego does). An auditor
	// and a viewer are refused the attach.
	window := map[string]any{"time_window_start": "2026-01-01T00:00:00Z", "time_window_end": "2026-03-31T23:59:59Z"}
	population := func(ctrl uuid.UUID, predicate string, period uuid.UUID) string {
		t.Helper()
		body := map[string]any{"control_id": ctrl.String(), "scope_predicate": rawJSON(predicate)}
		for k, v := range window {
			body[k] = v
		}
		st, out := asGRC.do(http.MethodPost, "/v1/populations", body)
		if st != http.StatusCreated {
			t.Fatalf("grc POST /v1/populations: %d %v", st, out)
		}
		id, _ := out["population"].(map[string]any)["id"].(string)
		attach := "/v1/audit-periods/" + period.String() + "/populations/" + id
		for name, c := range map[string]periodAuditorsCall{"auditor": asAuditor, "viewer": asViewer} {
			if st, out := c.do(http.MethodPost, attach, nil); st != http.StatusForbidden {
				t.Fatalf("%s POST %s: %d %v, want 403", name, attach, st, out)
			}
		}
		if st, out := asAdmin.do(http.MethodPost, attach, nil); st != http.StatusOK {
			t.Fatalf("tenant admin POST %s: %d %v, want 200", attach, st, out)
		}
		return id
	}
	cellPop := population(s.cellCtl, `{"op":"eq","dim":"environment","value":"prod"}`, s.assigned)
	population(s.wholeCtl, `{"op":"true"}`, s.assigned)
	otherPop := population(s.otherCtl, `{"op":"true"}`, s.unassigned)
	frozenPop := population(s.frozenCtl, `{"op":"true"}`, s.assigned)
	// A period freeze stamps frozen_at with the time of the call, after the
	// window ends. A freeze inside the window is stamped directly, so the
	// matrix can ask for a list that runs past it.
	if _, err := admin.Exec(context.Background(),
		`UPDATE populations SET frozen_at = '2026-03-01T00:00:00Z' WHERE id = $1`, frozenPop); err != nil {
		t.Fatalf("freeze population: %v", err)
	}
	draw := func(c periodAuditorsCall, pop string) (int, string, map[string]any) {
		st, out := c.do(http.MethodPost, "/v1/samples", map[string]any{"population_id": pop, "n": 1, "seed": "seed-2157"})
		sample, _ := out["sample"].(map[string]any)
		id, _ := sample["id"].(string)
		return st, id, sample
	}
	st, otherSample, _ := draw(asAdmin, otherPop)
	if st != http.StatusCreated {
		t.Fatalf("admin draw in the unassigned period: %d", st)
	}

	// Before the grant the auditor reaches nothing period-scoped.
	if st, out := asAuditor.do(http.MethodGet, "/v1/populations/"+cellPop, nil); st != http.StatusForbidden {
		t.Fatalf("auditor GET population before assignment: %d %v; want 403", st, out)
	}

	// The grant, through the #2118 route.
	if st, out := asAdmin.do(http.MethodPost, "/v1/audit-periods/"+s.assigned.String()+"/auditors",
		map[string]string{"user_id": auditorUser.String()}); st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("admin assigns auditor: %d %v", st, out)
	}
	if got := mePeriodIDs(t, asAuditor); len(got) != 1 || got[0] != s.assigned.String() {
		t.Fatalf("auditor periods = %v; want only %s", got, s.assigned)
	}

	// The auditor's own sample in the assigned period.
	st, sample, body := draw(asAuditor, cellPop)
	if st != http.StatusCreated {
		t.Fatalf("auditor POST /v1/samples in the assigned period: %d %v; want 201", st, body)
	}
	drawn, _ := body["evidence_record_ids"].([]any)
	if len(drawn) != 1 || drawn[0] != s.inScope.String() {
		t.Fatalf("sample drew %v; want only the in-scope record %s", drawn, s.inScope)
	}
	if st, out := asAuditor.do(http.MethodPost, "/v1/samples/"+sample+"/annotations",
		map[string]any{"evidence_record_id": s.inScope.String(), "result": "passed"}); st != http.StatusCreated {
		t.Fatalf("auditor annotates own sample: %d %v; want 201", st, out)
	}
	if st, out := asAuditor.do(http.MethodPost, "/v1/samples/"+sample+"/annotations",
		map[string]any{"evidence_record_id": s.other.String(), "result": "passed"}); st != http.StatusBadRequest {
		t.Fatalf("auditor annotates own sample with a record it did not draw: %d %v; want 400", st, out)
	}
	if st, out := asAuditor.do(http.MethodPost, "/v1/samples/"+otherSample+"/annotations",
		map[string]any{"evidence_record_id": s.other.String(), "result": "passed"}); st != http.StatusForbidden {
		t.Fatalf("auditor annotates the unassigned period's sample: %d %v; want 403", st, out)
	}
	if st, _, out := draw(asAuditor, otherPop); st != http.StatusForbidden {
		t.Fatalf("auditor draws in the unassigned period: %d %v; want 403", st, out)
	}
	if st, out := asAuditor.do(http.MethodPost, "/v1/populations", map[string]any{
		"control_id": s.cellCtl.String(), "time_window_start": window["time_window_start"], "time_window_end": window["time_window_end"],
	}); st != http.StatusForbidden {
		t.Fatalf("auditor creates a population: %d %v; want 403", st, out)
	}

	// The read matrix: route -> want status per role.
	ev := func(id uuid.UUID) string { return "/v1/evidence/" + id.String() }
	listIn := func(ctrl uuid.UUID, since, until string) string {
		out := "/v1/evidence?control_id=" + ctrl.String()
		if since != "" {
			out += "&since=" + since
		}
		if until != "" {
			out += "&until=" + until
		}
		return out
	}
	const start, end = "2026-01-01T00:00:00Z", "2026-03-31T23:59:59Z"
	list := func(ctrl uuid.UUID) string { return listIn(ctrl, start, end) }
	reads := []struct {
		name, path             string
		auditor, admin, viewer int
	}{
		{"assigned population", "/v1/populations/" + cellPop, 200, 200, 403},
		{"unassigned population", "/v1/populations/" + otherPop, 403, 200, 403},
		{"assigned sample", "/v1/samples/" + sample, 200, 200, 403},
		{"assigned sample annotations", "/v1/samples/" + sample + "/annotations", 200, 200, 403},
		{"unassigned sample", "/v1/samples/" + otherSample, 403, 200, 403},
		{"unassigned sample annotations", "/v1/samples/" + otherSample + "/annotations", 403, 200, 403},
		{"in-scope evidence", ev(s.inScope), 200, 200, 403},
		{"evidence in an unselected scope cell", ev(s.outOfCell), 403, 200, 403},
		{"evidence outside the population window", ev(s.outOfWindow), 403, 200, 403},
		{"evidence of a whole-control population", ev(s.whole), 200, 200, 403},
		{"evidence of the unassigned period", ev(s.other), 403, 200, 403},
		{"unknown evidence", ev(uuid.New()), 403, 404, 403},
		{"list of a whole-control population's control", list(s.wholeCtl), 200, 200, 403},
		{"list inside the window", listIn(s.wholeCtl, "2026-02-01T00:00:00Z", "2026-02-28T00:00:00Z"), 200, 200, 403},
		{"list without since or until", listIn(s.wholeCtl, "", ""), 403, 200, 403},
		{"list without until", listIn(s.wholeCtl, start, ""), 403, 200, 403},
		{"list without since", listIn(s.wholeCtl, "", end), 403, 200, 403},
		{"list before the window", listIn(s.wholeCtl, "2025-01-01T00:00:00Z", "2025-06-30T00:00:00Z"), 403, 200, 403},
		{"list starting before the window", listIn(s.wholeCtl, "2025-12-01T00:00:00Z", "2026-02-01T00:00:00Z"), 403, 200, 403},
		{"list ending after the window", listIn(s.wholeCtl, "2026-02-01T00:00:00Z", "2026-06-30T00:00:00Z"), 403, 200, 403},
		{"list of a frozen population, before the freeze", listIn(s.frozenCtl, start, "2026-03-01T00:00:00Z"), 200, 200, 403},
		{"list of a frozen population, past the freeze", listIn(s.frozenCtl, start, end), 403, 200, 403},
		{"list of a cell-scoped population's control", list(s.cellCtl), 403, 200, 403},
		{"list of the unassigned period's control", list(s.otherCtl), 403, 200, 403},
		{"tenant-wide list", "/v1/evidence", 403, 200, 403},
		{"tenant-wide list by cell", "/v1/evidence?scope_cell_id=" + prod.String(), 403, 200, 403},
		{"freshness panel", "/v1/evidence/freshness", 200, 200, 200},
	}
	// A grc_engineer's evidence lists are not bounded by any period.
	for _, p := range []string{list(s.cellCtl), listIn(s.wholeCtl, "", ""), listIn(s.frozenCtl, start, end)} {
		if st, out := asGRC.do(http.MethodGet, p, nil); st != http.StatusOK {
			t.Errorf("grc_engineer GET %s: %d %v; want 200", p, st, out)
		}
	}
	for _, rd := range reads {
		for role, c := range map[string]struct {
			call periodAuditorsCall
			want int
		}{"auditor": {asAuditor, rd.auditor}, "admin": {asAdmin, rd.admin}, "viewer": {asViewer, rd.viewer}} {
			if st, out := c.call.do(http.MethodGet, rd.path, nil); st != c.want {
				t.Errorf("%s GET %s (%s): %d %v; want %d", role, rd.path, rd.name, st, out, c.want)
			}
		}
	}

	// #2470: the per-control list's total is that list's own count, and an
	// auditor's rows are the control_id set its authz lookup covered. A
	// record filed under another control whose control_ref names wholeCtl
	// is listed for admin and grc_engineer (the slice-012 resolution), and
	// not for the auditor.
	crossRef := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (id, tenant_id, control_id, scope_id, observed_at, ingested_at,
		                              provenance, result, payload, hash, control_ref)
		VALUES ($1, $2, $3, $4, $5, now(), jsonb_build_object('evidence_kind', 'test.2470'), 'pass', '{}'::jsonb, $6, $7)`,
		crossRef, tenant, s.otherCtl, prod, inWindow, "hash-2470-"+crossRef.String(), s.wholeCtl.String()); err != nil {
		t.Fatalf("seed cross-ref evidence: %v", err)
	}
	listed := func(c periodAuditorsCall, path string) ([]string, float64) {
		t.Helper()
		st, out := c.do(http.MethodGet, path, nil)
		if st != http.StatusOK {
			t.Fatalf("GET %s: %d %v; want 200", path, st, out)
		}
		var ids []string
		rows, _ := out["evidence"].([]any)
		for _, r := range rows {
			id, _ := r.(map[string]any)["evidence_id"].(string)
			ids = append(ids, id)
		}
		total, _ := out["total"].(float64)
		return ids, total
	}
	for role, tc := range map[string]struct {
		call periodAuditorsCall
		want []string
	}{
		"auditor":      {asAuditor, []string{s.whole.String()}},
		"admin":        {asAdmin, []string{s.whole.String(), crossRef.String()}},
		"grc_engineer": {asGRC, []string{s.whole.String(), crossRef.String()}},
	} {
		ids, total := listed(tc.call, list(s.wholeCtl))
		slices.Sort(ids)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Errorf("%s list(wholeCtl) rows = %v; want %v", role, ids, want)
		}
		if int(total) != len(want) {
			t.Errorf("%s list(wholeCtl) total = %v; want the filtered count %d, not the tenant ledger", role, total, len(want))
		}
	}
	// The window bounds total too: an earlier range holds no wholeCtl record.
	if ids, total := listed(asAdmin, listIn(s.wholeCtl, "2025-01-01T00:00:00Z", "2025-06-30T00:00:00Z")); len(ids) != 0 || total != 0 {
		t.Errorf("admin list before the window: rows %v total %v; want none and 0", ids, total)
	}

	// Removing the grant takes the period away on the next request.
	if st, out := asAdmin.do(http.MethodDelete, "/v1/audit-periods/"+s.assigned.String()+"/auditors/"+auditorUser.String(), nil); st >= 300 {
		t.Fatalf("admin removes auditor: %d %v", st, out)
	}
	for _, p := range []string{"/v1/populations/" + cellPop, "/v1/samples/" + sample, ev(s.inScope), list(s.wholeCtl)} {
		if st, out := asAuditor.do(http.MethodGet, p, nil); st != http.StatusForbidden {
			t.Errorf("auditor GET %s after removal: %d %v; want 403", p, st, out)
		}
	}
}

// rawJSON is a JSON value the periodAuditorsCall body marshals verbatim.
type rawJSON string

func (r rawJSON) MarshalJSON() ([]byte, error) { return []byte(r), nil }
