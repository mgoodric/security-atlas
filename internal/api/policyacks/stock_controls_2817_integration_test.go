//go:build integration

// #2817: an acknowledgement is pushed under control_id
// "policy:<id>:v<ver>", which names no control, so ingest leaves the
// record's control_id NULL. The three stock SOC 2 controls that query
// policy.acknowledgment.v1 must still see it when they evaluate.

package policyacks_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/eval"
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
}
