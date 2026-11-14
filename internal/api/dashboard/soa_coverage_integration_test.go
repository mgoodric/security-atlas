//go:build integration

// Issue #1788 — framework coverage honours the exclusions of the caller
// tenant's ACTIVATED framework-scope revision (Statement of Applicability,
// #1744). Covers the dashboard posture read, its 90-day trend, the dashboard
// export, the MCP list_framework_posture tool, and the per-requirement
// coverage read.
//
// Run with:
//
//	go test -tags=integration -p 1 -run SoA ./internal/api/dashboard/...

package dashboard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mcp"
	"github.com/mgoodric/security-atlas/internal/mcp/tools"
)

// soaCoverageFixture is the #1788 fixture: one framework version with eight
// requirements, R-01..R-08, and three tenant controls.
//
//	R-01 -> A1 (C1)   excluded, with passing controls behind it
//	R-02 (no edge)    excluded, nothing behind it
//	R-03 (no edge)    undecided
//	R-04 -> A2 (C2)   applicable
//	R-05 -> A3 (C3)   applicable
//	R-06 -> A1 (C1)   applicable
//	R-07 -> A2 (C2)   applicable
//	R-08 (no edge)    applicable
//
// Latest evaluations: C1 pass, C2 pass, C3 fail. 120 days ago: C1 fail,
// C2 pass, C3 fail, so 90 days ago only the A2 requirements counted.
type soaCoverageFixture struct {
	frameworkID        uuid.UUID
	frameworkVersionID uuid.UUID
	req                map[string]uuid.UUID
}

func soaCoverageTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"framework_scope_applicability",
		"framework_scope_workflow_log",
		"framework_scopes",
		"control_evaluations",
		"evidence_freshness",
		"controls",
	)
}

func seedSoACoverageFixture(t *testing.T, admin *pgxpool.Pool, tenant string) soaCoverageFixture {
	t.Helper()
	ctx := context.Background()
	uniq := uuid.NewString()[:8]
	f := soaCoverageFixture{
		frameworkID:        uuid.New(),
		frameworkVersionID: uuid.New(),
		req:                map[string]uuid.UUID{},
	}
	scfFVID := uuid.New()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("framework", `INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		VALUES ($1, NULL, $2, 'SoA coverage framework (#1788)', 'tester')`, f.frameworkID, "soa-1788-"+uniq)
	exec("scf version", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, 'scf-1788', 'legacy')`, scfFVID, f.frameworkID)
	exec("framework version", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2022', 'current')`, f.frameworkVersionID, f.frameworkID)

	anchors := map[string]uuid.UUID{}
	for _, a := range []string{"A1", "A2", "A3"} {
		id := uuid.New()
		exec("anchor", `INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
			VALUES ($1, $2, $3, 'IAC', $4, '')`, id, scfFVID, "SOA1788-"+uniq+"-"+a, "anchor "+a)
		anchors[a] = id
	}
	edges := map[string]string{"R-01": "A1", "R-04": "A2", "R-05": "A3", "R-06": "A1", "R-07": "A2"}
	for i := 1; i <= 8; i++ {
		code := fmt.Sprintf("R-%02d", i)
		id := uuid.New()
		exec("requirement", `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
			VALUES ($1, $2, $3, $4, '')`, id, f.frameworkVersionID, code, "requirement "+code)
		f.req[code] = id
		if a, ok := edges[code]; ok {
			exec("edge", `INSERT INTO fw_to_scf_edges
				(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
				VALUES ($1, $2, $3, 'equal', 1.0, 'community_draft', '')`, uuid.New(), id, anchors[a])
		}
	}

	old := time.Now().UTC().Add(-120 * 24 * time.Hour)
	for _, c := range []struct{ name, anchor, then, now string }{
		{"C1", "A1", "fail", "pass"}, {"C2", "A2", "pass", "pass"}, {"C3", "A3", "fail", "fail"},
	} {
		id := seedControlOnAnchor(t, admin, tenant, anchors[c.anchor])
		seedEvaluation(t, admin, tenant, id, c.then, old)
		seedEvaluation(t, admin, tenant, id, c.now, time.Now().UTC())
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM framework_scopes WHERE framework_version_id = $1`, f.frameworkVersionID},
			{`DELETE FROM control_evaluations WHERE tenant_id = $1`, tenant},
			{`DELETE FROM controls WHERE tenant_id = $1`, tenant},
			{`DELETE FROM frameworks WHERE id = $1`, f.frameworkID},
		} {
			if _, err := admin.Exec(c, q.sql, q.arg); err != nil {
				t.Logf("cleanup %q: %v", q.sql, err)
			}
		}
	})
	return f
}

// seedScopeRevision writes a draft framework-scope revision carrying the
// fixture's decisions: R-01 and R-02 excluded, R-03 undecided, the rest
// applicable. Decisions are only writable on a draft, so activation (when
// asked for) happens after the decisions land.
func seedScopeRevision(t *testing.T, admin *pgxpool.Pool, tenant string, f soaCoverageFixture, activate bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	scopeID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_scopes (id, tenant_id, framework_version_id, name, predicate, predicate_hash, state)
		VALUES ($1, $2, $3, 'ISMS', '{"op":"true"}'::jsonb, 'h0', 'draft')`,
		scopeID, tenant, f.frameworkVersionID); err != nil {
		t.Fatalf("seed framework_scope: %v", err)
	}
	decide := func(code, decision, justification string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_scope_applicability
				(tenant_id, framework_scope_id, framework_requirement_id, decision, justification, updated_by)
			VALUES ($1, $2, $3, $4, $5, 'tester')`,
			tenant, scopeID, f.req[code], decision, justification); err != nil {
			t.Fatalf("seed decision %s: %v", code, err)
		}
	}
	decide("R-01", "excluded", "No in-house software development")
	decide("R-02", "excluded", "No cardholder data processed")
	for _, code := range []string{"R-04", "R-05", "R-06", "R-07", "R-08"} {
		decide(code, "applicable", "")
	}
	if activate {
		if _, err := admin.Exec(ctx, `
			UPDATE framework_scopes SET state = 'activated', effective_from = now()
			WHERE id = $1`, scopeID); err != nil {
			t.Fatalf("activate framework_scope: %v", err)
		}
	}
	return scopeID
}

type postureNumbers struct {
	Coverage  float64 `json:"coverage_pct"`
	Freshness float64 `json:"freshness_composite"`
	Trend     float64 `json:"trend_delta_90d"`
	Framework string  `json:"framework_id"`
}

func soaEnv(t *testing.T, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(dbtest.NewAppPool(t))
	// Admin: admitted by both the dashboard's program-read gate and the
	// export's admin-or-approver gate.
	bearer := srv.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func findPosture(t *testing.T, rows []postureNumbers, frameworkID uuid.UUID, source string) postureNumbers {
	t.Helper()
	for _, r := range rows {
		if r.Framework == frameworkID.String() {
			return r
		}
	}
	t.Fatalf("%s: no posture row for framework %s", source, frameworkID)
	return postureNumbers{}
}

func dashboardPosture(t *testing.T, env testEnv, f soaCoverageFixture) postureNumbers {
	t.Helper()
	resp, body := get(t, env, "/v1/frameworks/posture")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET posture: status %d", resp.StatusCode)
	}
	raw, _ := json.Marshal(body["frameworks"])
	var rows []postureNumbers
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode posture: %v", err)
	}
	return findPosture(t, rows, f.frameworkID, "dashboard")
}

func exportPosture(t *testing.T, env testEnv, f soaCoverageFixture) postureNumbers {
	t.Helper()
	resp, body := get(t, env, "/v1/dashboard/export?format=json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET export: status %d", resp.StatusCode)
	}
	panels, _ := body["panels"].(map[string]any)
	raw, _ := json.Marshal(panels["framework_posture"])
	var rows []postureNumbers
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode export posture: %v", err)
	}
	return findPosture(t, rows, f.frameworkID, "export")
}

func mcpPosture(t *testing.T, env testEnv, f soaCoverageFixture) postureNumbers {
	t.Helper()
	client, err := mcp.NewClient(env.server.URL, env.bearer, "test-1788")
	if err != nil {
		t.Fatalf("mcp client: %v", err)
	}
	out, err := tools.NewListFrameworkPosture(client).Handle(context.Background(),
		json.RawMessage(fmt.Sprintf(`{"limit":%d}`, tools.MaxLimit)))
	if err != nil {
		t.Fatalf("list_framework_posture: %v", err)
	}
	raw, _ := json.Marshal(out.(map[string]any)["frameworks"])
	var rows []postureNumbers
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode mcp posture: %v", err)
	}
	return findPosture(t, rows, f.frameworkID, "mcp")
}

// pct mirrors the query's ROUND(100.0 * n / d, 2).
func pct(n, d float64) float64 { return math.Round(10000*n/d) / 100 }

func assertPosture(t *testing.T, label string, got postureNumbers, coverage, trend float64) {
	t.Helper()
	if got.Coverage != coverage {
		t.Errorf("%s: coverage_pct = %v, want %v", label, got.Coverage, coverage)
	}
	if got.Trend != trend {
		t.Errorf("%s: trend_delta_90d = %v, want %v", label, got.Trend, trend)
	}
}

// Before activation every requirement counts; once the revision with two
// exclusions is activated the denominator drops from 8 to 6. The trend is
// on the same 6-requirement basis for both halves, and the export and MCP
// tool agree with the dashboard API.
func TestSoACoverage_ActivatedExclusionsLeaveTheDenominator(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaCoverageTenant(t, admin)
	f := seedSoACoverageFixture(t, admin, tenant)
	env := soaEnv(t, tenant)

	// Covered now: R-01, R-04, R-05, R-06, R-07 (5 of 8). Covered 90 days
	// ago: R-04, R-07 (C2 was the only control passing then).
	before := dashboardPosture(t, env, f)
	assertPosture(t, "before activation", before, pct(5, 8), pct(5, 8)-pct(2, 8)) // 62.5, 37.5

	seedScopeRevision(t, admin, tenant, f, true)

	// R-01 and R-02 leave the set. Covered now: R-04, R-05, R-06, R-07
	// (4 of 6) = 66.67. Covered then: R-04, R-07 (2 of 6) = 33.33, so the
	// trend is 33.33. A trend that kept the old 8-requirement basis for
	// the 90-day-ago half would read 66.67 - 25 = 41.67.
	after := dashboardPosture(t, env, f)
	assertPosture(t, "after activation", after, pct(4, 6), 33.33) // 66.67, 33.33

	for _, other := range []struct {
		name string
		got  postureNumbers
	}{
		{"export", exportPosture(t, env, f)},
		{"mcp", mcpPosture(t, env, f)},
	} {
		if other.got != after {
			t.Errorf("%s posture = %+v, dashboard API = %+v", other.name, other.got, after)
		}
	}
}

// A draft revision's exclusions don't change coverage.
func TestSoACoverage_DraftExclusionsHaveNoEffect(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaCoverageTenant(t, admin)
	f := seedSoACoverageFixture(t, admin, tenant)
	env := soaEnv(t, tenant)

	before := dashboardPosture(t, env, f)
	seedScopeRevision(t, admin, tenant, f, false)
	after := dashboardPosture(t, env, f)
	if after != before {
		t.Fatalf("draft exclusions changed posture: before %+v, after %+v", before, after)
	}
	assertPosture(t, "draft", after, 62.5, 37.5)
}

// Per-requirement coverage returns an excluded requirement as `excluded`
// with its justification, whether or not controls sit behind it. Undecided
// and applicable requirements carry no exclusion.
func TestSoACoverage_RequirementCoverageReportsExcluded(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaCoverageTenant(t, admin)
	f := seedSoACoverageFixture(t, admin, tenant)
	env := soaEnv(t, tenant)

	type coverage struct {
		Band      string `json:"confidence_band"`
		Exclusion *struct {
			FrameworkScopeID string `json:"framework_scope_id"`
			Justification    string `json:"justification"`
		} `json:"exclusion"`
	}
	read := func(code string) coverage {
		t.Helper()
		resp, body := get(t, env, "/v1/requirements/"+f.req[code].String()+"/coverage")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET coverage %s: status %d", code, resp.StatusCode)
		}
		raw, _ := json.Marshal(body)
		var c coverage
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("decode coverage %s: %v", code, err)
		}
		return c
	}

	// A draft exclusion is not reported.
	draft := seedScopeRevision(t, admin, tenant, f, false)
	if c := read("R-02"); c.Band == "excluded" || c.Exclusion != nil {
		t.Fatalf("draft: R-02 = %+v, want not excluded", c)
	}
	if _, err := admin.Exec(context.Background(), `DELETE FROM framework_scopes WHERE id = $1`, draft); err != nil {
		t.Fatalf("delete draft: %v", err)
	}

	scopeID := seedScopeRevision(t, admin, tenant, f, true)
	for code, want := range map[string]string{
		"R-01": "No in-house software development",
		"R-02": "No cardholder data processed",
	} {
		c := read(code)
		if c.Band != "excluded" {
			t.Errorf("%s: confidence_band = %q, want excluded", code, c.Band)
		}
		if c.Exclusion == nil || c.Exclusion.Justification != want || c.Exclusion.FrameworkScopeID != scopeID.String() {
			t.Errorf("%s: exclusion = %+v, want justification %q on scope %s", code, c.Exclusion, want, scopeID)
		}
	}
	for _, code := range []string{"R-03", "R-04"} {
		if c := read(code); c.Band == "excluded" || c.Exclusion != nil {
			t.Errorf("%s: %+v, want not excluded", code, c)
		}
	}
}
