//go:build integration

// #2924: a connector kind is routed to controls by evidence type. Ingest
// stores it bound to no control, keeping any wire control_id in control_ref
// as provenance, and every control in the tenant whose evidence queries
// declare the kind reads it. These tests push through ingest.Service.Process
// as a connector does and evaluate the stock bundles with the live engine.

package ingest_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// seedRoutedStockControl inserts a stock bundle as its control.yaml states
// it, under tenant: the engine reads freshness_class and evidence_queries
// from the row.
func seedRoutedStockControl(t *testing.T, admin *pgxpool.Pool, tenant, bundle string) uuid.UUID {
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
	t.Cleanup(func() {
		// Evaluations FK to the control.
		_, _ = admin.Exec(context.Background(), `DELETE FROM control_evaluations WHERE control_id = $1`, id)
		_, _ = admin.Exec(context.Background(), `DELETE FROM controls WHERE id = $1`, id)
	})
	return id
}

// evaluateInWindow runs the engine on ctrl as tenant and returns the stored
// verdict and the number of in-window records it read.
func evaluateInWindow(t *testing.T, admin, app *pgxpool.Pool, tenant string, ctrl uuid.UUID) (string, int) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl %s: %v", ctrl, err)
	}
	var result string
	var n int
	if err := admin.QueryRow(context.Background(), `
		SELECT result::text, evidence_count_in_window FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2
		ORDER BY evaluated_at DESC, created_at DESC LIMIT 1
	`, tenant, ctrl).Scan(&result, &n); err != nil {
		t.Fatalf("read evaluation for %s: %v", ctrl, err)
	}
	return result, n
}

func repoProtectionRecord(t *testing.T, controlID string) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, err := structpb.NewStruct(map[string]any{
		"repo_id":                    1,
		"repo_full_name":             "acme/api",
		"default_branch":             "main",
		"required_reviews":           1,
		"require_code_owner_reviews": false,
		"require_signed_commits":     false,
		"require_linear_history":     false,
		"enforce_admins":             true,
		"protection_source":          "classic",
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	return record(t, func(r *evidencev1.EvidenceRecord) {
		r.EvidenceKind = "github.repo_protection.v2"
		r.SchemaVersion = "2.0.0"
		r.ControlId = controlID
		r.Payload = payload
	})
}

func hostPostureRecord(t *testing.T) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, err := structpb.NewStruct(map[string]any{
		"host_uuid":               "11111111-1111-1111-1111-111111111111",
		"hostname":                "host-1111",
		"platform":                "darwin",
		"disk_encryption_enabled": true,
		"screen_lock_enabled":     true,
		"firewall_enabled":        true,
		"mdm_enrolled":            true,
		// cc4_1 decides on the host's check-in time (#2822).
		"last_seen_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	return record(t, func(r *evidencev1.EvidenceRecord) {
		r.EvidenceKind = "osquery.host_posture.v1"
		r.SchemaVersion = "1.2.0"
		r.ControlId = ""
		r.Payload = payload
	})
}

var cc81Bundles = []string{"soc2_cc8_1_change_management", "soc2_cc8_1_configuration_change_control"}

// One GitHub record on default flags (no control) feeds both cc8_1 bundles,
// and is stored bound to no control.
func TestRouted2924_OneGitHubRecordFeedsBothCC81Bundles(t *testing.T) {
	svc, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)

	ctrls := make(map[string]uuid.UUID, len(cc81Bundles))
	for _, b := range cc81Bundles {
		ctrls[b] = seedRoutedStockControl(t, admin, tenantA, b)
		if res, n := evaluateInWindow(t, admin, app, tenantA, ctrls[b]); res != eval.ResultInconclusive || n != 0 {
			t.Fatalf("%s before push: (%s, %d), want (inconclusive, 0)", b, res, n)
		}
	}

	receipt, _, err := svc.Process(context.Background(), repoProtectionRecord(t, ""), mkCred(tenantA, nil, ""))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	got, ref := ledgerControl(t, admin, receipt.RecordID)
	if got.Valid || ref != "" {
		t.Fatalf("stored (control_id=%v, control_ref=%q), want (NULL, \"\")", got, ref)
	}
	if receipt.LinkedControlID != "" {
		t.Fatalf("receipt.LinkedControlID = %q, want empty", receipt.LinkedControlID)
	}

	for _, b := range cc81Bundles {
		if res, n := evaluateInWindow(t, admin, app, tenantA, ctrls[b]); res != eval.ResultPass || n != 1 {
			t.Errorf("%s after push: (%s, %d), want (pass, 1)", b, res, n)
		}
	}

	// The hash still verifies: verify rebuilds the wire record from the
	// empty control_ref.
	row, err := dbx.New(admin).GetEvidenceRecordByID(context.Background(), dbx.GetEvidenceRecordByIDParams{
		ID:       pgtype.UUID{Bytes: uuid.MustParse(receipt.RecordID), Valid: true},
		TenantID: pgtype.UUID{Bytes: uuid.MustParse(tenantA), Valid: true},
	})
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if ok, recomputed, err := ingest.VerifyLedgerRow(row); err != nil || !ok {
		t.Fatalf("VerifyLedgerRow = (%v, %s, %v), want ok (stored %s)", ok, recomputed, err, row.Hash)
	}
}

// A tenant that binds explicitly still pushes: the wire control_id is kept as
// provenance only, and the record reaches every control that queries the kind.
func TestRouted2924_ExplicitControlIDIsProvenanceOnly(t *testing.T) {
	svc, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)

	ctrls := make(map[string]uuid.UUID, len(cc81Bundles))
	for _, b := range cc81Bundles {
		ctrls[b] = seedRoutedStockControl(t, admin, tenantA, b)
	}
	for name, wire := range map[string]string{
		"bundle id": cc81Bundles[0],
		"uuid":      ctrls[cc81Bundles[0]].String(),
	} {
		receipt, _, err := svc.Process(context.Background(), repoProtectionRecord(t, wire), mkCred(tenantA, nil, ""))
		if err != nil {
			t.Fatalf("%s: Process: %v", name, err)
		}
		got, ref := ledgerControl(t, admin, receipt.RecordID)
		if got.Valid || ref != wire {
			t.Fatalf("%s: stored (control_id=%v, control_ref=%q), want (NULL, %q)", name, got, ref, wire)
		}
	}
	for _, b := range cc81Bundles {
		if res, n := evaluateInWindow(t, admin, app, tenantA, ctrls[b]); res != eval.ResultPass || n != 2 {
			t.Errorf("%s: (%s, %d), want (pass, 2)", b, res, n)
		}
	}
}

// One osquery record feeds cc6_6 and cc4_1.
func TestRouted2924_OneOsqueryRecordFeedsCC66AndCC41(t *testing.T) {
	svc, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)

	bundles := []string{"soc2_cc6_6_boundary_protection", "soc2_cc4_1_continuous_monitoring"}
	ctrls := make(map[string]uuid.UUID, len(bundles))
	for _, b := range bundles {
		ctrls[b] = seedRoutedStockControl(t, admin, tenantA, b)
	}
	if _, _, err := svc.Process(context.Background(), hostPostureRecord(t), mkCred(tenantA, nil, "")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	for _, b := range bundles {
		if res, n := evaluateInWindow(t, admin, app, tenantA, ctrls[b]); res != eval.ResultPass || n != 1 {
			t.Errorf("%s: (%s, %d), want (pass, 1)", b, res, n)
		}
	}
}

// A tenant never reads another tenant's routed evidence, though both tenants
// hold the same stock bundles.
func TestRouted2924_CrossTenantIsolation(t *testing.T) {
	svc, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)

	a := seedRoutedStockControl(t, admin, tenantA, cc81Bundles[0])
	b := seedRoutedStockControl(t, admin, tenantB, cc81Bundles[0])
	if _, _, err := svc.Process(context.Background(), repoProtectionRecord(t, ""), mkCred(tenantA, nil, "")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res, n := evaluateInWindow(t, admin, app, tenantA, a); res != eval.ResultPass || n != 1 {
		t.Errorf("tenant A: (%s, %d), want (pass, 1)", res, n)
	}
	if res, n := evaluateInWindow(t, admin, app, tenantB, b); res != eval.ResultInconclusive || n != 0 {
		t.Errorf("tenant B read tenant A's routed record: (%s, %d), want (inconclusive, 0)", res, n)
	}
}

// Only a routed kind may omit control_id, and an owned kind is still refused
// from a push.
func TestRouted2924_PushRules(t *testing.T) {
	svc, _, _ := boot(t)

	if _, _, err := svc.Process(context.Background(),
		record(t, func(r *evidencev1.EvidenceRecord) { r.ControlId = "" }),
		mkCred(tenantA, nil, "")); !errors.Is(err, ingest.ErrMissingField) {
		t.Errorf("bound kind with no control_id: err=%v, want ErrMissingField", err)
	}
	ack := record(t, func(r *evidencev1.EvidenceRecord) {
		r.EvidenceKind = "policy.acknowledgment.v1"
		r.ControlId = ""
	})
	if _, _, err := svc.Process(context.Background(), ack, mkCred(tenantA, nil, "")); err == nil {
		t.Errorf("owned kind with no control_id: accepted, want refused")
	}
	ack.ControlId = "policy:x:v1"
	if _, _, err := svc.Process(context.Background(), ack, mkCred(tenantA, nil, "")); !errors.Is(err, ingest.ErrOwnedKind) {
		t.Errorf("owned kind push: err=%v, want ErrOwnedKind", err)
	}
}
