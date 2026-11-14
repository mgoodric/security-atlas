//go:build integration

// #2056: the board pack's posture is the dashboard's posture. Two frameworks
// at different coverage each keep their own percentage, equal to what
// GET /v1/frameworks/posture's store returns; the coverage-trend baseline
// comes from the posture trend; a framework with no activated scope reads
// "Scope not set".

package board_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/dashboard"
	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// postureFramework describes one seeded framework version: reqs
// requirements, each on its own SCF anchor; the first coveredNow have a
// covering control; the first coveredThen of those passed 120 days ago.
type postureFramework struct {
	name        string
	reqs        int
	coveredNow  int
	coveredThen int
	activate    bool
}

// seedPostureFramework seeds a global framework with a current version, its
// requirements, STRM edges to SCF anchors, the tenant's covering controls and
// their evaluations, and (when asked) an activated framework scope. Returns
// the framework id. Cleanup removes everything it wrote.
func seedPostureFramework(t *testing.T, admin *pgxpool.Pool, tenant string, f postureFramework) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	uniq := uuid.NewString()[:8]
	fwID, scfFVID, fvID := uuid.New(), uuid.New(), uuid.New()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("framework", `INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		VALUES ($1, NULL, $2, $3, 'tester')`, fwID, "posture-2056-"+uniq, f.name)
	exec("scf version", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, 'scf-2056', 'legacy')`, scfFVID, fwID)
	exec("framework version", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2026', 'current')`, fvID, fwID)

	old := time.Now().UTC().Add(-120 * 24 * time.Hour)
	for i := 0; i < f.reqs; i++ {
		anchorID, reqID := uuid.New(), uuid.New()
		exec("anchor", `INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
			VALUES ($1, $2, $3, 'IAC', 'anchor', '')`, anchorID, scfFVID, fmt.Sprintf("P2056-%s-%d", uniq, i))
		exec("requirement", `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
			VALUES ($1, $2, $3, 'requirement', '')`, reqID, fvID, fmt.Sprintf("R-%02d", i))
		exec("edge", `INSERT INTO fw_to_scf_edges
			(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
			VALUES ($1, $2, $3, 'equal', 1.0, 'community_draft', '')`, uuid.New(), reqID, anchorID)
		if i >= f.coveredNow {
			continue
		}
		ctrlID := uuid.New()
		exec("control", `INSERT INTO controls (
				id, tenant_id, title, control_family, implementation_type,
				bundle_id, evidence_queries, applicability_expr, scf_anchor_id)
			VALUES ($1, $2, 'posture 2056 control', 'IAC', 'automated', $3, '[]'::jsonb, 'true', $4)`,
			ctrlID, tenant, "posture-2056-"+ctrlID.String(), anchorID)
		then := "fail"
		if i < f.coveredThen {
			then = "pass"
		}
		for _, ev := range []struct {
			result string
			at     time.Time
		}{{then, old}, {"pass", time.Now().UTC()}} {
			exec("evaluation", `INSERT INTO control_evaluations (
					id, tenant_id, control_id, eval_run_id, evaluated_at,
					result, freshness_status, evidence_count_in_window, trigger)
				VALUES ($1, $2, $3, $4, $5, $6, 'fresh', 1, 'manual')`,
				uuid.New(), tenant, ctrlID, uuid.New(), ev.at, ev.result)
		}
	}
	if f.activate {
		scopeID := uuid.New()
		exec("framework scope", `INSERT INTO framework_scopes
				(id, tenant_id, framework_version_id, name, predicate, predicate_hash, state)
			VALUES ($1, $2, $3, 'ISMS', '{"op":"true"}'::jsonb, 'h0', 'draft')`, scopeID, tenant, fvID)
		exec("activate scope", `UPDATE framework_scopes SET state = 'activated', effective_from = now()
			WHERE id = $1`, scopeID)
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM framework_scope_workflow_log WHERE tenant_id = $1`, tenant},
			{`DELETE FROM framework_scopes WHERE framework_version_id = $1`, fvID},
			{`DELETE FROM control_evaluations WHERE tenant_id = $1`, tenant},
			{`DELETE FROM controls WHERE tenant_id = $1`, tenant},
			{`DELETE FROM frameworks WHERE id = $1`, fwID},
		} {
			if _, err := admin.Exec(c, q.sql, q.arg); err != nil {
				t.Logf("cleanup %q: %v", q.sql, err)
			}
		}
	})
	return fwID
}

func TestPackGenerator_PostureIsTheDashboardsPerFramework(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// A: 3 of 4 covered now, 1 passing 120 days ago -> 75%, +50, from 25%.
	// B: 1 of 5 covered now, none then -> 20%, +20, from 0%.
	// C: fully covered but no activated scope -> "Scope not set".
	fwA := seedPostureFramework(t, admin, tenant, postureFramework{name: "Framework A (#2056)", reqs: 4, coveredNow: 3, coveredThen: 1, activate: true})
	fwB := seedPostureFramework(t, admin, tenant, postureFramework{name: "Framework B (#2056)", reqs: 5, coveredNow: 1, coveredThen: 0, activate: true})
	fwC := seedPostureFramework(t, admin, tenant, postureFramework{name: "Framework C (#2056)", reqs: 2, coveredNow: 2, coveredThen: 2, activate: false})

	now := time.Now().UTC()
	stored, err := board.NewPackGenerator(board.NewPackStore(app), nil).
		WithClock(func() time.Time { return now }).
		Generate(ctx, now.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dashRows, err := dashboard.NewStore(app).FrameworkPosture(ctx,
		pgtype.Timestamptz{Time: now.Add(-board.PostureTrendWindow), Valid: true})
	if err != nil {
		t.Fatalf("dashboard FrameworkPosture: %v", err)
	}
	dash := map[string]struct{ coverage, freshness, trend float64 }{}
	for _, r := range dashRows {
		dash[uuid.UUID(r.FrameworkID.Bytes).String()] = struct{ coverage, freshness, trend float64 }{
			r.CoveragePct, r.FreshnessComposite, r.TrendDelta90d,
		}
	}
	pack := map[string]board.PackFrameworkPosture{}
	for _, p := range stored.Content.Sections[board.SectionPosture].Data.Frameworks {
		pack[p.FrameworkID] = p
	}

	for _, c := range []struct {
		id                  uuid.UUID
		coverage, base, dlt float64
		state               string
	}{
		{fwA, 75, 25, 50, "in-progress"},
		{fwB, 20, 0, 20, "at-risk"},
	} {
		p, ok := pack[c.id.String()]
		if !ok {
			t.Fatalf("framework %s missing from the pack's posture section", c.id)
		}
		d := dash[c.id.String()]
		if p.CoveragePct != d.coverage || p.FreshnessPct != d.freshness || p.Delta != d.trend {
			t.Errorf("%s: pack (coverage %v, freshness %v, trend %v) != dashboard (%v, %v, %v)",
				p.Name, p.CoveragePct, p.FreshnessPct, p.Delta, d.coverage, d.freshness, d.trend)
		}
		if p.CoveragePct != c.coverage || p.BaselinePct != c.base || p.Delta != c.dlt || p.State != c.state {
			t.Errorf("%s: got coverage %v from %v (%+v pts), %s; want %v from %v (%+v), %s",
				p.Name, p.CoveragePct, p.BaselinePct, p.Delta, p.State, c.coverage, c.base, c.dlt, c.state)
		}
	}
	if p := pack[fwC.String()]; !p.ScopeNotSet || p.State != board.PostureScopeNotSet || p.CoveragePct != 0 {
		t.Errorf("unscoped framework C = %+v, want scope-not-set with no percentage", p)
	}

	// Coverage trend: the mean over the two scoped frameworks, baseline from
	// the trend's start.
	trend := stored.Content.Sections[board.SectionCoverageTrend].Data
	if trend.ScopedFrameworkCount != 2 || trend.CoveragePct != 47.5 || trend.BaselineCoveragePct != 12.5 || trend.CoverageDelta != 35 {
		t.Errorf("coverage_trend = (scoped %d, coverage %v, baseline %v, delta %v), want (2, 47.5, 12.5, 35)",
			trend.ScopedFrameworkCount, trend.CoveragePct, trend.BaselineCoveragePct, trend.CoverageDelta)
	}
	if inv := stored.Content.Sections[board.SectionInvestment].Data; inv.CoverageDelta != 35 {
		t.Errorf("investment coverage delta = %v, want 35 (mirrors coverage_trend)", inv.CoverageDelta)
	}
}
