//go:build integration

// #2973: sast.scan_result.v1 is routed by type. One push reaches every
// control that declares the kind (cc7_1 detection, pi1_2, pi1_3), and a
// period frozen after the first routing cutover but before sast's own
// (evidence_routing_kind_cutover) keeps reading what it read while sast was
// bound.

package ingest_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var sastBundles = []string{
	"soc2_cc7_1_vulnerability_detection",
	"soc2_pi1_2_input_completeness",
	"soc2_pi1_3_processing_validity",
}

func sastFindings() map[string]any {
	return map[string]any{
		"tool":           "semgrep",
		"findings_count": 1,
		"repository":     "acme/api",
		"findings": []any{map[string]any{
			"rule_id": "python.lang.security.sqli", "severity": "high", "suppressed": false,
			"cwe": []any{"CWE-89"},
		}},
	}
}

func TestRouted2973_OneSASTPushReachesEveryDeclaringControl(t *testing.T) {
	svc, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)
	ctrls := map[string]uuid.UUID{}
	for _, b := range sastBundles {
		ctrls[b] = seedRoutedStockControl(t, admin, tenantA, b)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `TRUNCATE evidence_records CASCADE`); err != nil {
			t.Logf("cleanup truncate evidence_records: %v", err)
		}
	})

	payload, err := structpb.NewStruct(sastFindings())
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	rec := record(t, func(r *evidencev1.EvidenceRecord) {
		r.SchemaVersion = "1.2.0"
		r.ControlId = ""
		r.Payload = payload
	})
	if _, decision, err := svc.Process(context.Background(), rec, mkCred(tenantA, nil, "")); err != nil || decision != ingest.DecisionAccepted {
		t.Fatalf("Process: decision=%v err=%v, want accepted", decision, err)
	}
	var controlID *uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT control_id FROM evidence_records WHERE tenant_id = $1 AND idempotency_key = $2`,
		tenantA, rec.IdempotencyKey).Scan(&controlID); err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	if controlID != nil {
		t.Fatalf("stored control_id = %s, want NULL (routed)", controlID)
	}

	// One high CWE-89 finding: a pi1_2 finding, not a pi1_3 one, not critical.
	want := map[string]string{
		"soc2_cc7_1_vulnerability_detection": "pass",
		"soc2_pi1_2_input_completeness":      "fail",
		"soc2_pi1_3_processing_validity":     "pass",
	}
	for _, b := range sastBundles {
		got, n := evaluateInWindow(t, admin, app, tenantA, ctrls[b])
		if n != 1 || got != want[b] {
			t.Errorf("%s: result %s over %d records, want %s over 1", b, got, n, want[b])
		}
	}
}

func kindCutover(t *testing.T, admin *pgxpool.Pool, kind string) time.Time {
	t.Helper()
	var cut time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT cut_at FROM evidence_routing_kind_cutover WHERE evidence_kind = $1`, kind).Scan(&cut); err != nil {
		t.Fatalf("read %s routing cutover: %v", kind, err)
	}
	return cut
}

// insertSAST writes one sast.scan_result.v1 row as the owner role, the way a
// row stored while sast was bound looks: bound to ctrl when ctrl is set,
// else control-free under ref.
func insertSAST(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl *uuid.UUID, ref string, observed time.Time) uuid.UUID {
	t.Helper()
	body, _ := json.Marshal(sastFindings())
	id := uuid.New()
	var controlID any
	if ctrl != nil {
		controlID = *ctrl
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, evidence_kind, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, 'sast.scan_result.v1', $4, now(), '{}'::jsonb, 'pass', $5::jsonb, $6, $7)
	`, id, tenant, controlID, observed, string(body), "hash-2973-"+id.String()[:8], ref); err != nil {
		t.Fatalf("insert sast row: %v", err)
	}
	return id
}

func TestRouted2973_PeriodFrozenBeforeSASTCutoverKeepsItsReads(t *testing.T) {
	_, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)
	ctx, err := tenancy.WithTenant(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	first := routingCutover(t, admin)
	// One migrate run applies both cutovers within moments of each other.
	// Move sast's a day past the first, so a period can freeze between them.
	origSAST := kindCutover(t, admin, "sast.scan_result.v1")
	sastCut := first.Add(24 * time.Hour)
	if _, err := admin.Exec(context.Background(),
		`UPDATE evidence_routing_kind_cutover SET cut_at = $1 WHERE evidence_kind = 'sast.scan_result.v1'`, sastCut); err != nil {
		t.Fatalf("move sast cutover: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(),
			`UPDATE evidence_routing_kind_cutover SET cut_at = $1 WHERE evidence_kind = 'sast.scan_result.v1'`, origSAST); err != nil {
			t.Logf("restore sast cutover: %v", err)
		}
	})
	ctrl := seedRoutedStockControl(t, admin, tenantA, sastBundles[0])
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(context.Background(), stmt, tenantA); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
		if _, err := admin.Exec(context.Background(), `TRUNCATE evidence_records CASCADE`); err != nil {
			t.Logf("cleanup truncate evidence_records: %v", err)
		}
	})
	fw := pinFrameworkVersion(t, admin)
	periods := period.NewStore(app)
	summaries := evidencesummary.NewPeriodStore(app)

	freeze := func(name string, at time.Time) period.Period {
		t.Helper()
		p, err := periods.Create(ctx, period.CreateInput{
			Name:               name,
			FrameworkVersionID: fw,
			PeriodStart:        first.Add(-24 * time.Hour),
			PeriodEnd:          sastCut.Add(24 * time.Hour),
			CreatedBy:          "key_test_2973",
		})
		if err != nil {
			t.Fatalf("create period %s: %v", name, err)
		}
		if _, err := periods.Freeze(ctx, p.ID, "key_test_2973", at); err != nil {
			t.Fatalf("freeze period %s: %v", name, err)
		}
		return p
	}
	between := first.Add(time.Hour)
	oldPeriod := freeze("frozen between the cutovers", between)
	newPeriod := freeze("frozen after sast's cutover", sastCut.Add(time.Hour))

	// Three rows from while sast was bound, all before both horizons: bound
	// to the control; control-free under the control's UUID (read through
	// the control_ref leg); control-free under an SCF anchor (read by no
	// control then).
	observed := first.Add(-time.Hour)
	bound := insertSAST(t, admin, tenantA, &ctrl, ctrl.String(), observed)
	refRow := insertSAST(t, admin, tenantA, nil, ctrl.String(), observed)
	scfRow := insertSAST(t, admin, tenantA, nil, "scf:VPM-06", observed)

	setOf := func(p period.Period) []uuid.UUID {
		t.Helper()
		set, err := summaries.PeriodEvidenceSet(ctx, ctrl, p.ID)
		if err != nil {
			t.Fatalf("period evidence set: %v", err)
		}
		var ids []uuid.UUID
		for _, r := range set.Records {
			ids = append(ids, r.EvidenceID)
		}
		if int(set.TotalCount) != len(ids) {
			t.Fatalf("evidence set total %d, listed %d", set.TotalCount, len(ids))
		}
		return sortedIDs(ids...)
	}

	if got := setOf(oldPeriod); !slices.Equal(got, sortedIDs(bound, refRow)) {
		t.Errorf("period frozen between the cutovers = %v, want the bound and ref rows %v (not the scf: row %s)", got, sortedIDs(bound, refRow), scfRow)
	}
	if _, ok, err := summaries.ResolveBeforeHorizon(ctx, scfRow, ctrl, between); err != nil || ok {
		t.Errorf("period frozen between the cutovers resolves the scf: row: ok=%v err=%v, want false", ok, err)
	}
	if _, ok, err := summaries.ResolveBeforeHorizon(ctx, refRow, ctrl, between); err != nil || !ok {
		t.Errorf("period frozen between the cutovers resolves the ref row: ok=%v err=%v, want true", ok, err)
	}
	if got := setOf(newPeriod); !slices.Equal(got, sortedIDs(bound, refRow, scfRow)) {
		t.Errorf("period frozen after sast's cutover = %v, want all three rows", got)
	}
	state, err := periods.ControlState(ctx, oldPeriod.ID, ctrl)
	if err != nil {
		t.Fatalf("old period control state: %v", err)
	}
	if slices.Contains(observationIDs(state), scfRow) {
		t.Errorf("period frozen between the cutovers: control state reads the scf: row %s", scfRow)
	}
	state, err = periods.ControlState(ctx, newPeriod.ID, ctrl)
	if err != nil {
		t.Fatalf("new period control state: %v", err)
	}
	if !slices.Contains(observationIDs(state), scfRow) {
		t.Errorf("period frozen after sast's cutover: control state misses the scf: row %s", scfRow)
	}
}
