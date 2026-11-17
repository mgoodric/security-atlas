//go:build integration

package risk_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// #2175: switching Risk off hides it, it does not stop data upkeep. With
// Risk off, the evidence-ingest path (the residual recompute
// ResidualSubscriber runs for every ingested record; the NATS wiring is
// TestResidualSubscriber_RecomputesOnEvidenceIngest) still recomputes a
// linked risk's residual. Switched back on, the risk shows the new
// residual and its parent's roll-up is computed from current children.
//
// No core path runs the aggregation engine today: it runs only inside
// risk.Store writes, which come through /v1/risks* (decisions log
// docs/audit-log/2175-background-module-switches-decisions.md D3).
func TestRiskOff_EvidenceStillRecomputesResidualAndRollUpIsCurrent(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate,
		"feature_flag_audit_log",
		"feature_flags",
		"risk_aggregations",
		"risk_control_links",
		"control_evaluations",
		"evidence_records",
		"risks",
		"controls",
	)
	ctx := dbtest.WithTenantCtx(t, tenant)
	store := risk.NewStore(app)
	flags := featureflag.NewStore(app)

	child := seedNistRisk(t, migrate, tenant, 5, 5) // inherent 25
	sibling := seedNistRisk(t, migrate, tenant, 2, 2)
	ctrlID := seedEvalControl(t, migrate, tenant)
	if err := store.LinkControl(ctx, risk.LinkControlInput{Actor: "test",
		RiskID: child, ControlID: ctrlID,
		DesignScore: 0.0, DesignScoreSet: true,
		WeightDesign: 0.0, WeightOperation: 1.0, WeightCoverage: 0.0, WeightsSet: true,
	}); err != nil {
		t.Fatalf("LinkControl: %v", err)
	}
	parent, err := store.Aggregate(ctx, risk.AggregateInput{
		ParentTitle:      "Rolled up",
		ParentLevel:      dbx.RiskLevelOrg,
		SeverityFunction: risk.SeverityFunctionMax,
		ChildRiskIDs:     []uuid.UUID{child, sibling},
	})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if _, err := flags.Set(ctx, "risk.enabled", false, "test", "#2175"); err != nil {
		t.Fatalf("switch Risk off: %v", err)
	}
	// Passing evidence lands for the linked control while Risk is off.
	seedEvidence(t, migrate, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*time.Hour))
	deriver := risk.NewResidualDeriver(store, eval.NewEngine(eval.NewStore(app), scope.NewStore(app)))
	if _, err := deriver.DeriveAndPersist(ctx, child, true); err != nil {
		t.Fatalf("residual recompute with Risk off: %v", err)
	}
	var blob []byte
	if err := migrate.QueryRow(context.Background(),
		`SELECT residual_score FROM risks WHERE tenant_id = $1 AND id = $2`, tenant, child).Scan(&blob); err != nil {
		t.Fatalf("read residual: %v", err)
	}
	if score, ok := residualScoreField(blob); !ok || score >= 1.0 {
		t.Fatalf("Risk off: residual = %v (ok=%v); want recomputed below 1 from passing evidence", score, ok)
	}

	if _, err := flags.Set(ctx, "risk.enabled", true, "test", "#2175"); err != nil {
		t.Fatalf("switch Risk on: %v", err)
	}
	got, err := store.Get(ctx, child)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if score, ok := residualScoreField(got.ResidualScore); !ok || score >= 1.0 {
		t.Errorf("Risk on: child residual = %v; want the value recomputed while off", score)
	}
	live, err := store.LiveAggregation(ctx, parent.Parent.ID)
	if err != nil {
		t.Fatalf("LiveAggregation: %v", err)
	}
	if live.ChildCount != 2 || live.Severity != 25 {
		t.Errorf("Risk on: roll-up = %d children, severity %d; want 2, 25 (max of current children)", live.ChildCount, live.Severity)
	}
}
