//go:build integration

// #2817: an acknowledgement is pushed under control_id
// "policy:<id>:v<ver>", which names no control, so ingest leaves the
// record's control_id NULL. The three stock SOC 2 controls that query
// policy.acknowledgment.v1 must still see it when they evaluate, and the
// evidence views an auditor traces that verdict through must show it.

package policyacks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ackStockBundles are the stock bundles the ticket names. stockAckBundles
// re-derives the set from controls/soc2, so a fourth bundle that starts
// querying the kind fails this test until it is added here.
var ackStockBundles = []string{
	"soc2_cc1_5_accountability_acknowledgment",
	"soc2_cc5_3_policies_procedures",
	"soc2_cc7_4_incident_response",
}

func stockAckBundles(t *testing.T) []string {
	t.Helper()
	soc2 := filepath.Join("..", "..", "..", "controls", "soc2")
	entries, err := os.ReadDir(soc2)
	if err != nil {
		t.Fatalf("read controls/soc2: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := control.ParseDirectory(filepath.Join(soc2, e.Name()))
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, q := range b.Manifest.EvidenceQueries {
			if q.EvidenceKind == "policy.acknowledgment.v1" {
				out = append(out, e.Name())
				break
			}
		}
	}
	return out
}

// seedStockControl inserts the bundle exactly as its control.yaml states
// it: the engine reads freshness_class and evidence_queries from the row.
func seedStockControl(t *testing.T, admin *pgxpool.Pool, tenant, bundle string) uuid.UUID {
	t.Helper()
	b, err := control.ParseDirectory(filepath.Join("..", "..", "..", "controls", "soc2", bundle))
	if err != nil {
		t.Fatalf("parse %s: %v", bundle, err)
	}
	m := b.Manifest
	queries, err := m.EvidenceQueriesJSON()
	if err != nil {
		t.Fatalf("%s evidence queries: %v", bundle, err)
	}
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, 'true')
	`, id, tenant, m.Title, m.ControlFamily, m.ImplementationType, m.BundleID, m.FreshnessClass, queries); err != nil {
		t.Fatalf("seed %s: %v", bundle, err)
	}
	return id
}

func latestEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl uuid.UUID) (result string, inWindow int) {
	t.Helper()
	if err := admin.QueryRow(context.Background(), `
		SELECT result::text, evidence_count_in_window FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2
		ORDER BY evaluated_at DESC, created_at DESC LIMIT 1
	`, tenant, ctrl).Scan(&result, &inWindow); err != nil {
		t.Fatalf("read evaluation for %s: %v", ctrl, err)
	}
	return result, inWindow
}

func TestAcknowledge2817_ReachesStockAckControls(t *testing.T) {
	s := setup(t)
	t.Cleanup(func() {
		// Registered after setup's cleanup, so it runs first: evaluations
		// FK to the controls that cleanup deletes.
		if _, err := s.admin.Exec(context.Background(),
			`DELETE FROM control_evaluations WHERE tenant_id = $1`, s.tenant); err != nil {
			t.Logf("cleanup control_evaluations: %v", err)
		}
	})

	if got := stockAckBundles(t); !slices.Equal(got, ackStockBundles) {
		t.Fatalf("stock bundles querying policy.acknowledgment.v1: got %v, want %v", got, ackStockBundles)
	}
	controls := make(map[string]uuid.UUID, len(ackStockBundles))
	for _, b := range ackStockBundles {
		controls[b] = seedStockControl(t, s.admin, s.tenant, b)
	}

	ctx, err := tenancy.WithTenant(context.Background(), s.tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	engine := eval.NewEngine(eval.NewStore(s.app), scope.NewStore(s.app))
	evaluate := func(bundle string) (string, int) {
		t.Helper()
		if _, err := engine.EvaluateControl(ctx, controls[bundle], eval.TriggerManual, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("evaluate %s: %v", bundle, err)
		}
		return latestEvaluation(t, s.admin, s.tenant, controls[bundle])
	}

	// Before any acknowledgement: no evidence, inconclusive.
	for _, b := range ackStockBundles {
		if res, n := evaluate(b); res != eval.ResultInconclusive || n != 0 {
			t.Fatalf("%s before ack: got (%s, %d in window), want (inconclusive, 0)", b, res, n)
		}
	}

	policyID := createAndPublishPolicy(t, s, "Information Security Policy", "1", []string{s.requiredRole}, s.controlID)
	_, bearer := s.makeUserAndBearer(t, "ack-2817@test", []string{s.requiredRole}, false)
	s.postJSON(t, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)

	// The ack's control_id names no control, so the record is unbound.
	var unbound int
	if err := s.admin.QueryRow(context.Background(), `
		SELECT count(*) FROM evidence_records
		WHERE tenant_id = $1 AND evidence_kind = 'policy.acknowledgment.v1' AND control_id IS NULL
	`, s.tenant).Scan(&unbound); err != nil {
		t.Fatalf("count ack records: %v", err)
	}
	if unbound != 1 {
		t.Fatalf("want 1 unbound policy.acknowledgment.v1 record, got %d", unbound)
	}

	for _, b := range ackStockBundles {
		if res, n := evaluate(b); res != eval.ResultPass || n != 1 {
			t.Errorf("%s after ack: got (%s, %d in window), want (pass, 1)", b, res, n)
		}
	}

	// A control that does not query the kind never sees the ack.
	if _, err := engine.EvaluateControl(ctx, s.controlID, eval.TriggerManual, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("evaluate unrelated control: %v", err)
	}
	if res, n := latestEvaluation(t, s.admin, s.tenant, s.controlID); n != 0 {
		t.Errorf("unrelated control: got (%s, %d in window), want 0 in window", res, n)
	}

	// The verdict traces to evidence: the ack is on each control's evidence
	// list, in its period control state, in its population and in the
	// sample drawn from it, and a period-scoped auditor can open it.
	var ackID uuid.UUID
	if err := s.admin.QueryRow(context.Background(), `
		SELECT id FROM evidence_records
		WHERE tenant_id = $1 AND evidence_kind = 'policy.acknowledgment.v1'
	`, s.tenant).Scan(&ackID); err != nil {
		t.Fatalf("read ack record id: %v", err)
	}
	withAck := make(map[string]uuid.UUID, len(controls)+1)
	for b, id := range controls {
		withAck[b] = id
	}
	withAck["unrelated"] = s.controlID
	want := func(name string) bool { return name != "unrelated" }

	for name, ctrl := range withAck {
		ids, total := evidenceList(t, s, ctrl)
		if got := slices.Contains(ids, ackID.String()); got != want(name) || (total == 1) != want(name) {
			t.Errorf("%s: GET /v1/evidence lists ack=%v total=%d, want ack=%v", name, got, total, want(name))
		}
	}

	// The freshness read model counts it too.
	t.Cleanup(func() {
		_, _ = s.admin.Exec(context.Background(), `DELETE FROM evidence_freshness WHERE tenant_id = $1`, s.tenant)
	})
	if _, err := freshness.NewStore(s.app).Refresh(ctx); err != nil {
		t.Fatalf("refresh freshness: %v", err)
	}
	for name, ctrl := range withAck {
		var n int
		if err := s.admin.QueryRow(context.Background(), `
			SELECT evidence_count FROM evidence_freshness WHERE tenant_id = $1 AND control_id = $2
		`, s.tenant, ctrl).Scan(&n); err != nil {
			t.Fatalf("%s: read freshness: %v", name, err)
		}
		if (n == 1) != want(name) {
			t.Errorf("%s: freshness evidence_count = %d, want ack counted=%v", name, n, want(name))
		}
	}

	audits := audit.NewStore(s.app)
	periods := period.NewStore(s.app)
	fw := seedFrameworkVersion2817(t, s.admin)
	t.Cleanup(func() { cleanupPeriods2817(t, s.admin, s.tenant) })
	p, err := periods.Create(ctx, period.CreateInput{
		Name:               "FY26 SOC 2",
		FrameworkVersionID: fw,
		PeriodStart:        time.Now().Add(-24 * time.Hour),
		PeriodEnd:          time.Now().Add(24 * time.Hour),
		CreatedBy:          "key_test_2817",
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}
	pops := make(map[string]audit.Population, len(controls))
	for b, ctrl := range controls {
		pop, err := audits.CreatePopulation(ctx, audit.CreatePopulationInput{
			ControlID:       ctrl,
			ScopePredicate:  []byte(`{}`),
			TimeWindowStart: time.Now().Add(-time.Hour),
			TimeWindowEnd:   time.Now().Add(time.Hour),
			CreatedBy:       "key_test_2817",
		})
		if err != nil {
			t.Fatalf("%s: create population: %v", b, err)
		}
		if pop.RowCount != 1 {
			t.Errorf("%s: population row_count = %d, want 1", b, pop.RowCount)
		}
		if err := periods.AttachPopulation(ctx, p.ID, pop.ID, "key_test_2817"); err != nil {
			t.Fatalf("%s: attach population: %v", b, err)
		}
		pops[b] = pop
	}
	if _, err := periods.Freeze(ctx, p.ID, "key_test_2817", time.Now().UTC()); err != nil {
		t.Fatalf("freeze period: %v", err)
	}

	for name, ctrl := range withAck {
		obs, err := periods.ControlState(ctx, p.ID, ctrl)
		if err != nil {
			t.Fatalf("%s: period control state: %v", name, err)
		}
		got := slices.ContainsFunc(obs, func(o period.ControlStateObservation) bool { return o.EvidenceRecordID == ackID })
		if got != want(name) {
			t.Errorf("%s: frozen period control state lists ack=%v, want %v", name, got, want(name))
		}
	}
	for b, pop := range pops {
		smp, err := audits.DrawSample(ctx, audit.DrawSampleInput{
			PopulationID: pop.ID, N: 1, Seed: "seed-2817", CreatedBy: "key_test_2817",
		})
		if err != nil {
			t.Fatalf("%s: draw sample: %v", b, err)
		}
		if !slices.Equal(smp.EvidenceRecordIDs, []uuid.UUID{ackID}) {
			t.Errorf("%s: sample = %v, want [%s]", b, smp.EvidenceRecordIDs, ackID)
		}
	}
	scopeOf, ok, err := audits.EvidencePeriods(ctx, ackID)
	if err != nil || !ok {
		t.Fatalf("evidence periods for ack: ok=%v err=%v", ok, err)
	}
	if !slices.Equal(scopeOf.Periods, []uuid.UUID{p.ID}) {
		t.Errorf("ack's periods = %v, want [%s]", scopeOf.Periods, p.ID)
	}
}

// evidenceList is GET /v1/evidence?control_id=ctrl as the admin: the ids on
// the first page and the total.
func evidenceList(t *testing.T, s setupResult, ctrl uuid.UUID) ([]string, int64) {
	t.Helper()
	body := s.getJSON(t, "/v1/evidence?control_id="+ctrl.String(), s.adminBear, http.StatusOK)
	var resp struct {
		Evidence []struct {
			EvidenceID string `json:"evidence_id"`
		} `json:"evidence"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode evidence list: %v", err)
	}
	ids := make([]string, len(resp.Evidence))
	for i, e := range resp.Evidence {
		ids[i] = e.EvidenceID
	}
	return ids, resp.Total
}

// seedFrameworkVersion2817 seeds a global framework + version for the audit
// period to reference (the shape of internal/audit/period's fixture).
func seedFrameworkVersion2817(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fwID, versionID := uuid.New(), uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Issue 2817 test framework', $2, 'test')
	`, fwID, "issue2817-"+fwID.String()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')
	`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = admin.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, versionID)
		_, _ = admin.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return versionID
}

// cleanupPeriods2817 drops the period-side rows before setup's cleanup
// deletes the controls and evidence they reference.
func cleanupPeriods2817(t *testing.T, admin *pgxpool.Pool, tenant string) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM sample_audit_log WHERE tenant_id = $1`,
		`DELETE FROM sample_annotations WHERE tenant_id = $1`,
		`DELETE FROM sample_evidence WHERE tenant_id = $1`,
		`DELETE FROM samples WHERE tenant_id = $1`,
		`DELETE FROM populations WHERE tenant_id = $1`,
		`DELETE FROM audit_periods WHERE tenant_id = $1`,
	} {
		if _, err := admin.Exec(context.Background(), stmt, tenant); err != nil {
			t.Logf("cleanup %s: %v", stmt, err)
		}
	}
}
