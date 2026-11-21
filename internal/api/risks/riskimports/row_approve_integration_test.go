//go:build integration

// #2847 — per-row approval and review flags on staged rows, through the
// same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

func approveRow(t *testing.T, e env, bearer, batchID, rowID string) (int, []byte) {
	t.Helper()
	return call(t, e, http.MethodPost, bearer, "/v1/risks/imports/"+batchID+"/rows/"+rowID+"/approve", nil)
}

func TestRiskImport_ApproveOneRow(t *testing.T) {
	e := newEnv(t)
	existing := seedRisk(t, e.app, e.tenant, "r-exist", "Original title")
	b := stage(t, e, e.grcJWT)
	row := stagedByRef(t, b, "R-001")

	status, raw := approveRow(t, e, e.apprJWT, b.ID, row.ID)
	if status != http.StatusOK {
		t.Fatalf("approve row = %d %s", status, raw)
	}
	d := decode[riskimport.RowDecision](t, raw)
	if d.Status != riskimport.RowStatusApproved || d.RiskID == "" || d.DecidedBy != e.approver.String() || d.RowID != row.ID {
		t.Fatalf("decision = %+v", d)
	}
	// Only that row became a risk; the others stay pending.
	if n := countRisks(t, e); n != 2 {
		t.Errorf("risks = %d, want the seeded one and the approved one", n)
	}
	var title, ref string
	if err := e.admin.QueryRow(context.Background(),
		`SELECT title, external_ref FROM risks WHERE id = $1`, d.RiskID).Scan(&title, &ref); err != nil {
		t.Fatalf("read risk: %v", err)
	}
	if title != "Vendor outage" || ref != "R-001" {
		t.Errorf("risk = %q %q", title, ref)
	}
	var pending int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_import_rows WHERE batch_id = $1 AND status = 'pending'`, b.ID).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != len(b.Staged)-1 {
		t.Errorf("pending = %d, want %d", pending, len(b.Staged)-1)
	}
	var action, actor string
	if err := e.admin.QueryRow(context.Background(), `
		SELECT action, actor FROM risk_import_audit_log WHERE tenant_id = $1 AND row_id = $2`,
		e.tenant, row.ID).Scan(&action, &actor); err != nil {
		t.Fatalf("read approve audit: %v", err)
	}
	if action != "approve" || actor != e.approver.String() {
		t.Errorf("approve audit = %s %s", action, actor)
	}

	// A decided row is final.
	if status, raw := approveRow(t, e, e.apprJWT, b.ID, row.ID); status != http.StatusConflict {
		t.Errorf("second approve = %d %s, want 409", status, raw)
	}

	// The row whose ID the tenant already uses is marked duplicate.
	dup := stagedByRef(t, b, "R-EXIST")
	status, raw = approveRow(t, e, e.apprJWT, b.ID, dup.ID)
	if status != http.StatusOK {
		t.Fatalf("approve duplicate row = %d %s", status, raw)
	}
	if d := decode[riskimport.RowDecision](t, raw); d.Status != riskimport.RowStatusDuplicate || d.RiskID != existing {
		t.Errorf("duplicate decision = %+v", d)
	}

	// Approve-all then takes only what is still pending.
	_, raw = approve(t, e, e.apprJWT, b.ID)
	for _, a := range decode[riskimport.ApproveResult](t, raw).Approved {
		if a.RowID == row.ID || a.RowID == dup.ID {
			t.Errorf("approve-all re-decided row %s", a.RowID)
		}
	}
}

func TestRiskImport_ApproveOneRowAccess(t *testing.T) {
	e := newEnv(t)
	b := stage(t, e, e.grcJWT)
	row := stagedByRef(t, b, "R-001")

	for name, bearer := range map[string]string{"viewer": e.viewJWT, "machine": e.machine} {
		if status, raw := approveRow(t, e, bearer, b.ID, row.ID); status != http.StatusForbidden {
			t.Errorf("%s approve row = %d, want 403; body=%s", name, status, raw)
		}
	}
	if n := countRisks(t, e); n != 0 {
		t.Fatalf("refused approvals created %d risks", n)
	}
	if status, _ := approveRow(t, e, e.apprJWT, b.ID, "00000000-0000-0000-0000-000000000000"); status != http.StatusNotFound {
		t.Errorf("unknown row = %d, want 404", status)
	}
	if status, _ := approveRow(t, e, e.apprJWT, b.ID, "not-a-uuid"); status != http.StatusNotFound {
		t.Errorf("malformed row = %d, want 404", status)
	}
}

func TestRiskImport_ApproveOneRowRefusedStaysPending(t *testing.T) {
	e := newEnv(t)
	b := stage(t, e, e.grcJWT)
	row := stagedByRef(t, b, "R-002")
	// The row's category is archived after upload: the risk store refuses
	// it, and the row stays pending for the approver to reject.
	if _, err := e.admin.Exec(context.Background(),
		`UPDATE org_risk_categories SET archived = true WHERE tenant_id = $1 AND slug = $2`,
		e.tenant, row.Category); err != nil {
		t.Fatalf("archive category: %v", err)
	}
	if status, raw := approveRow(t, e, e.apprJWT, b.ID, row.ID); status != http.StatusUnprocessableEntity {
		t.Fatalf("approve refused row = %d %s, want 422", status, raw)
	}
	var status string
	if err := e.admin.QueryRow(context.Background(),
		`SELECT status FROM risk_import_rows WHERE id = $1`, row.ID).Scan(&status); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != riskimport.RowStatusPending {
		t.Errorf("row status = %s, want pending", status)
	}
}

func TestRiskImport_RowFlags(t *testing.T) {
	e := newEnv(t)
	b := stage(t, e, e.grcJWT)
	for _, r := range b.Staged {
		if r.Flags == nil || len(r.Flags) != 0 {
			t.Fatalf("row %d flags = %#v, want empty", r.RowNumber, r.Flags)
		}
	}
	row := stagedByRef(t, b, "R-002")
	if _, err := e.admin.Exec(context.Background(),
		`UPDATE risk_import_rows SET flags = ARRAY['owner_unlinked', 'parent_link_failed'] WHERE id = $1`, row.ID); err != nil {
		t.Fatalf("set flags: %v", err)
	}
	status, raw := call(t, e, http.MethodGet, e.viewJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, raw)
	}
	got := stagedByRef(t, decode[riskimport.Batch](t, raw), "R-002").Flags
	if len(got) != 2 || got[0] != riskimport.FlagOwnerUnlinked || got[1] != riskimport.FlagParentLinkFailed {
		t.Errorf("flags = %v", got)
	}
	// The vocabulary is fixed.
	if _, err := e.admin.Exec(context.Background(),
		`UPDATE risk_import_rows SET flags = ARRAY['made_up'] WHERE id = $1`, row.ID); err == nil {
		t.Error("an unknown flag was accepted")
	}
}
