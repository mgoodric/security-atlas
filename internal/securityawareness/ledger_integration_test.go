//go:build integration

package securityawareness_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/securityawareness"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	for dir := filepath.Dir(thisFile); ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod)")
		}
		dir = parent
	}
}

// seedCC14 installs the stock cc1_4 bundle as the tenant's active control,
// as the SOC 2 kit import does, and returns its id.
func seedCC14(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	b, err := control.ParseDirectory(filepath.Join(repoRoot(t), "controls", "soc2", securityawareness.CC14ControlRef))
	if err != nil {
		t.Fatalf("parse cc1_4: %v", err)
	}
	m := b.Manifest
	queries, err := m.EvidenceQueriesJSON()
	if err != nil {
		t.Fatalf("cc1_4 evidence queries: %v", err)
	}
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, 'true')
	`, id, tenant, m.Title, m.ControlFamily, m.ImplementationType, m.BundleID, m.FreshnessClass, queries); err != nil {
		t.Fatalf("seed cc1_4: %v", err)
	}
	return id
}

func evaluateCC14(t *testing.T, ctx context.Context, app *pgxpool.Pool, ctrl uuid.UUID) string {
	t.Helper()
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}
	states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("want 1 state, got %d", len(states))
	}
	return states[0].Result
}

// #2941: a completion recorded through the platform (Complete for alice,
// a CSV import for bob) lands in the ledger bound to cc1_4 and decides its
// evaluation. Bob's completion is months old: it is accepted because
// observed_at is the import instant, and cc1_4 judges it on completed_at.
func TestPlatformCompletionsReachCC14Evaluation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	day := 24 * time.Hour

	cases := []struct {
		name   string
		bobAgo time.Duration
		want   string
	}{
		{"both within 365 days", 200 * day, eval.ResultPass},
		{"bob's csv completion 380 days old", 380 * day, eval.ResultFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := freshTenant(t, admin)
			ctx := tenantCtx(t, tenant)
			store := newStore(t, admin, app)
			ctrl := seedCC14(t, admin, tenant)
			fx := seedCampaignFixture(t, ctx, store, "cc14.test")

			alice, err := store.Complete(ctx, securityawareness.CompleteInput{
				AssignmentID: fx["alice"],
				CompletedAt:  now.Add(-10 * day),
				Source:       securityawareness.CompletionSourceManual,
			}, "operator-1")
			if err != nil {
				t.Fatalf("Complete alice: %v", err)
			}
			csv := strings.Join([]string{
				completionCSVHeader,
				",rippling:worker-2,hris,SAT-2026,2026 annual," + now.Add(-tc.bobAgo).Format(time.RFC3339) + ",,,,",
			}, "\n")
			report, err := store.ImportCompletionsCSV(ctx, strings.NewReader(csv), "importer-1", securityawareness.DefaultCSVImportLimits)
			if err != nil {
				t.Fatalf("ImportCompletionsCSV: %v", err)
			}
			if report.Imported != 1 {
				t.Fatalf("report = %+v, want bob imported", report)
			}

			ledger := trainingLedger(t, admin, tenant)
			for _, id := range []uuid.UUID{*alice.EvidenceRecordID, report.Results[0].EvidenceRecordID} {
				rec, ok := ledger[id]
				if !ok {
					t.Fatalf("record %s not in the ledger", id)
				}
				if rec.ControlID == nil || *rec.ControlID != ctrl {
					t.Fatalf("record %s control_id = %v, want cc1_4 %s", id, rec.ControlID, ctrl)
				}
			}
			if got := evaluateCC14(t, ctx, app, ctrl); got != tc.want {
				t.Fatalf("cc1_4 result = %q, want %q", got, tc.want)
			}
		})
	}
}

// A store built without an ingest service refuses to complete rather than
// completing without evidence.
func TestCompleteWithoutIngestFailsClosed(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	fx := seedCampaignFixture(t, ctx, newStore(t, admin, app), "closed.test")

	_, err := securityawareness.NewStore(app).Complete(ctx, securityawareness.CompleteInput{
		AssignmentID: fx["alice"],
		CompletedAt:  time.Now().UTC(),
		Source:       securityawareness.CompletionSourceManual,
	}, "operator-1")
	if !errors.Is(err, ingest.ErrUnavailable) {
		t.Fatalf("Complete without ingest err = %v, want ingest.ErrUnavailable", err)
	}
	if completedAt, _ := assignmentState(t, admin, tenant, fx["alice"]); completedAt != nil {
		t.Fatalf("assignment completed without evidence: %v", completedAt)
	}
}

// A completion recorded before #2941 has no ledger record. Re-importing it
// at the same instant writes one and links it, without changing the
// completion.
func TestReimportBackfillsCompletionWithoutEvidence(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := newStore(t, admin, app)
	fx := seedCampaignFixture(t, ctx, store, "legacy.test")

	completed := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	if _, err := admin.Exec(context.Background(), `
UPDATE security_training_assignments SET completed_at = $3, completion_source = 'csv'
WHERE tenant_id = $1 AND id = $2`, tenant, fx["alice"], completed); err != nil {
		t.Fatalf("seed legacy completion: %v", err)
	}
	csv := strings.Join([]string{
		completionCSVHeader,
		"alice@legacy.test,,,SAT-2026,2026 annual," + completed.Format(time.RFC3339) + ",,,,",
	}, "\n")
	report, err := store.ImportCompletionsCSV(ctx, strings.NewReader(csv), "importer-1", securityawareness.DefaultCSVImportLimits)
	if err != nil {
		t.Fatalf("ImportCompletionsCSV: %v", err)
	}
	if report.AlreadyComplete != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v, want one already_complete", report)
	}
	got := assignmentEvidenceID(t, admin, tenant, fx["alice"])
	if got == nil || *got != report.Results[0].EvidenceRecordID {
		t.Fatalf("assignment evidence_record_id = %v, want %s", got, report.Results[0].EvidenceRecordID)
	}
	ledger := trainingLedger(t, admin, tenant)
	if len(ledger) != 1 || ledger[*got].Payload["completed_at"] != completed.Format(time.RFC3339Nano) {
		t.Fatalf("ledger = %+v, want one record for the legacy completion", ledger)
	}
}
