//go:build integration

// #3002 — closing a risk import batch nobody will finish, and the import
// list's pending and waiting counts. Same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

func closeBatch(t *testing.T, e env, bearer, batchID string, body any) (int, []byte) {
	t.Helper()
	return call(t, e, http.MethodPost, bearer, "/v1/risks/imports/"+batchID+"/close", body)
}

type listBody struct {
	Batches []riskimport.BatchSummary `json:"batches"`
}

func listBatches(t *testing.T, e env, bearer string) map[string]riskimport.BatchSummary {
	t.Helper()
	status, raw := call(t, e, http.MethodGet, bearer, "/v1/risks/imports", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/risks/imports = %d %s", status, raw)
	}
	out := map[string]riskimport.BatchSummary{}
	for _, b := range decode[listBody](t, raw).Batches {
		out[b.ID] = b
	}
	return out
}

type importAuditRow struct {
	action, actor, reason, externalRef string
}

func importAuditRows(t *testing.T, e env, batchID string) []importAuditRow {
	t.Helper()
	rows, err := e.admin.Query(context.Background(), `
		SELECT action, actor, COALESCE(reason, ''), COALESCE(external_ref, '')
		FROM risk_import_audit_log WHERE tenant_id = $1 AND batch_id = $2::uuid
		ORDER BY row_number`, e.tenant, batchID)
	if err != nil {
		t.Fatalf("read import audit rows: %v", err)
	}
	defer rows.Close()
	var out []importAuditRow
	for rows.Next() {
		var r importAuditRow
		if err := rows.Scan(&r.action, &r.actor, &r.reason, &r.externalRef); err != nil {
			t.Fatalf("scan import audit row: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// Closing a batch rejects its pending rows with the reason, audits each
// rejection by the closer, and settles the rows of every import waiting on
// them: a child fails when no other pending row carries its parent's ID and
// keeps waiting when one does. The batch's own row waiting on another
// import keeps waiting, and the list counts all of it before and after.
func TestRiskImport_CloseBatch(t *testing.T) {
	e := newEnv(t)

	children := uploadBatch(t, e, "Risk ID,Title,Category,Level,Parent Risk ID\n"+
		"R-C1,Child of an abandoned row,operational,team,R-P1\n"+
		"R-C2,Child with a second parent row,operational,team,R-P2\n")
	other := uploadBatch(t, e, "Risk ID,Title,Category,Level\nR-P2,Parent staged twice,operational,org\n")
	q := uploadBatch(t, e, "Risk ID,Title,Category,Level\nR-Q,Parent of the abandoned import's row,operational,org\n")
	abandoned := uploadBatch(t, e, "Risk ID,Title,Category,Level,Parent Risk ID\n"+
		"R-P1,Abandoned parent,operational,org,\n"+
		"R-P2,Abandoned parent staged twice,operational,org,\n"+
		"R-PW,Approved and waiting,operational,team,R-Q\n")

	approveOne(t, e, children, "R-C1")
	approveOne(t, e, children, "R-C2")
	if d := approveOne(t, e, abandoned, "R-PW"); d.ParentLinkStatus != riskimport.ParentLinkWaiting {
		t.Fatalf("R-PW = %+v; want waiting on R-Q", d)
	}

	before := listBatches(t, e, e.viewJWT)
	if got := before[abandoned.ID]; got.Status != riskimport.BatchStatusStaged || got.PendingCount != 2 ||
		got.WaitingCount != 1 || got.ChildrenWaitingCount != 2 || got.ClosedAt != nil {
		t.Fatalf("abandoned in the list = %+v; want staged, 2 pending, 1 waiting, 2 children waiting", got)
	}
	if got := before[children.ID]; got.Status != riskimport.BatchStatusCompleted || got.PendingCount != 0 || got.WaitingCount != 2 {
		t.Errorf("children in the list = %+v; want completed with 2 waiting", got)
	}
	if got := before[other.ID]; got.PendingCount != 1 || got.ChildrenWaitingCount != 1 {
		t.Errorf("other in the list = %+v; want 1 pending, 1 child waiting (R-C2)", got)
	}

	status, raw := closeBatch(t, e, e.apprJWT, abandoned.ID, map[string]string{"reason": "nobody owns this register"})
	if status != http.StatusOK {
		t.Fatalf("close = %d %s", status, raw)
	}
	res := decode[riskimport.CloseResult](t, raw)
	if res.Status != riskimport.BatchStatusClosed || res.ClosedBy != e.approver.String() || res.ClosedAt.IsZero() ||
		res.CloseReason != "nobody owns this register" || len(res.Rejected) != 2 {
		t.Fatalf("close result = %+v", res)
	}

	got := getBatch(t, e, abandoned.ID)
	if got.Status != riskimport.BatchStatusClosed || got.ClosedBy != e.approver.String() || got.ClosedAt == nil ||
		got.CloseReason != "nobody owns this register" {
		t.Fatalf("closed batch = status %s by %q at %v reason %q", got.Status, got.ClosedBy, got.ClosedAt, got.CloseReason)
	}
	for _, ref := range []string{"R-P1", "R-P2"} {
		r := stagedByRef(t, got, ref)
		if r.Status != riskimport.RowStatusRejected || r.DecidedBy != e.approver.String() ||
			r.RejectionReason != "import closed: nobody owns this register" {
			t.Errorf("%s = %s by %q %q; want rejected by the closer", ref, r.Status, r.DecidedBy, r.RejectionReason)
		}
	}
	// Its own row waits on R-Q, which is still pending in another import.
	if r := stagedByRef(t, got, "R-PW"); r.Status != riskimport.RowStatusApproved || r.ParentLinkStatus != riskimport.ParentLinkWaiting {
		t.Errorf("R-PW = %s %s %q; want approved and still waiting", r.Status, r.ParentLinkStatus, r.ParentLinkError)
	}

	audit := importAuditRows(t, e, abandoned.ID)
	var rejects []string
	for _, a := range audit {
		if a.action == "reject" {
			if a.actor != e.approver.String() || a.reason != "import closed: nobody owns this register" {
				t.Errorf("reject audit row = %+v", a)
			}
			rejects = append(rejects, a.externalRef)
		}
	}
	if !slices.Equal(rejects, []string{"R-P1", "R-P2"}) {
		t.Errorf("reject audit rows for %q; want R-P1, R-P2", rejects)
	}

	kids := getBatch(t, e, children.ID)
	c1 := stagedByRef(t, kids, "R-C1")
	if c1.ParentLinkStatus != riskimport.ParentLinkFailed || !slices.Contains(c1.Flags, riskimport.FlagParentLinkFailed) ||
		!strings.Contains(c1.ParentLinkError, `"R-P1"`) {
		t.Errorf("R-C1 = %s %q flags=%q; want failed: its only parent row was closed", c1.ParentLinkStatus, c1.ParentLinkError, c1.Flags)
	}
	if c2 := stagedByRef(t, kids, "R-C2"); c2.ParentLinkStatus != riskimport.ParentLinkWaiting {
		t.Errorf("R-C2 = %s %q; want waiting on the other import's R-P2", c2.ParentLinkStatus, c2.ParentLinkError)
	}

	after := listBatches(t, e, e.viewJWT)
	if a := after[abandoned.ID]; a.Status != riskimport.BatchStatusClosed || a.PendingCount != 0 ||
		a.ChildrenWaitingCount != 0 || a.ClosedAt == nil || a.CloseReason != "nobody owns this register" {
		t.Errorf("abandoned in the list after close = %+v", a)
	}
	if a := after[children.ID]; a.WaitingCount != 1 {
		t.Errorf("children waiting after close = %d; want 1 (R-C2)", a.WaitingCount)
	}

	// A closed batch takes no further decision, close or mapping.
	if status, _ := closeBatch(t, e, e.apprJWT, abandoned.ID, map[string]string{"reason": "again"}); status != http.StatusConflict {
		t.Errorf("second close = %d; want 409", status)
	}
	if status, _ := approveRow(t, e, e.apprJWT, abandoned.ID, stagedByRef(t, got, "R-P1").ID); status != http.StatusConflict {
		t.Errorf("approve a closed batch's row = %d; want 409", status)
	}
	if status, raw := mapValue(t, e, e.apprJWT, abandoned.ID, map[string]any{"kind": "category", "value": "operational", "target": ""}); status != http.StatusConflict {
		t.Errorf("map a closed batch's value = %d %s; want 409", status, raw)
	}

	// Closing the import R-PW waits on fails it: no pending row carries R-Q.
	if status, raw := closeBatch(t, e, e.apprJWT, q.ID, map[string]string{"reason": "superseded"}); status != http.StatusOK {
		t.Fatalf("close q = %d %s", status, raw)
	}
	pw := stagedByRef(t, getBatch(t, e, abandoned.ID), "R-PW")
	if pw.ParentLinkStatus != riskimport.ParentLinkFailed || !slices.Contains(pw.Flags, riskimport.FlagParentLinkFailed) {
		t.Errorf("R-PW after q closed = %s %q flags=%q; want failed", pw.ParentLinkStatus, pw.ParentLinkError, pw.Flags)
	}
}

// A row waiting on an ID nothing carries any more, stranded out of band,
// fails when its batch is closed, with the close's wording.
func TestRiskImport_CloseFailsStrandedWaiting(t *testing.T) {
	e := newEnv(t)
	b := uploadBatch(t, e, "Risk ID,Title,Category,Level,Parent Risk ID\n"+
		"R-CHILD,Child,operational,team,R-GONE\n"+
		"R-OPEN,Keeps the batch staged,operational,team,\n")
	parents := uploadBatch(t, e, "Risk ID,Title,Category,Level\nR-GONE,Parent,operational,org\n")
	approveOne(t, e, b, "R-CHILD")
	if _, err := e.admin.Exec(context.Background(),
		`DELETE FROM risk_import_rows WHERE id = $1::uuid`, stagedByRef(t, parents, "R-GONE").ID); err != nil {
		t.Fatalf("remove parent row: %v", err)
	}
	if status, raw := closeBatch(t, e, e.apprJWT, b.ID, map[string]string{"reason": "stale"}); status != http.StatusOK {
		t.Fatalf("close = %d %s", status, raw)
	}
	c := stagedByRef(t, getBatch(t, e, b.ID), "R-CHILD")
	if c.ParentLinkStatus != riskimport.ParentLinkFailed ||
		!strings.Contains(c.ParentLinkError, `still waiting on parent risk ID "R-GONE" when this import was closed`) {
		t.Errorf("R-CHILD = %s %q; want failed as stranded by the close", c.ParentLinkStatus, c.ParentLinkError)
	}
}

// Close has the decide routes' gate: a GRC engineer, a viewer, a machine
// credential and the uploader are refused and nothing is decided. A blank
// reason is a 400, a completed batch a 409, another tenant's batch a 404.
// Another tenant's list does not show the batch.
func TestRiskImport_CloseAccess(t *testing.T) {
	e := newEnv(t)
	b := uploadBatch(t, e, "Risk ID,Title,Category\nR-1,One,operational\n")

	for name, bearer := range map[string]string{"grc_engineer": e.grcJWT, "viewer": e.viewJWT, "machine": e.machine} {
		if status, raw := closeBatch(t, e, bearer, b.ID, map[string]string{"reason": "x"}); status != http.StatusForbidden {
			t.Errorf("%s close = %d %s; want 403", name, status, raw)
		}
	}
	// An admin who uploaded the batch does not close it.
	status, raw := upload(t, e, e.apprJWT, "own.csv", []byte("Risk ID,Title,Category\nR-OWN,Own,operational\n"))
	if status != http.StatusCreated {
		t.Fatalf("admin upload = %d %s", status, raw)
	}
	own := decode[riskimport.Batch](t, raw)
	if status, raw := closeBatch(t, e, e.apprJWT, own.ID, map[string]string{"reason": "x"}); status != http.StatusForbidden ||
		!strings.Contains(string(raw), "uploader") {
		t.Errorf("uploader close = %d %s; want 403", status, raw)
	}
	for name, body := range map[string]any{"blank reason": map[string]string{"reason": "  "}, "no body": nil} {
		if status, raw := closeBatch(t, e, e.apprJWT, b.ID, body); status != http.StatusBadRequest {
			t.Errorf("%s close = %d %s; want 400", name, status, raw)
		}
	}
	if n := countDecided(t, e, b.ID) + countDecided(t, e, own.ID); n != 0 {
		t.Errorf("refused closes decided %d rows", n)
	}
	if got := getBatch(t, e, b.ID); got.Status != riskimport.BatchStatusStaged {
		t.Errorf("batch status after refused closes = %s", got.Status)
	}

	otherEnv := newEnvFor(t, e.admin, e.app, dbtest.SeedTenant(t, e.admin, cleanupTables()...))
	if status, _ := closeBatch(t, otherEnv, otherEnv.apprJWT, b.ID, map[string]string{"reason": "x"}); status != http.StatusNotFound {
		t.Errorf("other tenant close = %d; want 404", status)
	}
	if _, ok := listBatches(t, otherEnv, otherEnv.apprJWT)[b.ID]; ok {
		t.Error("another tenant's list shows the batch")
	}

	approveOne(t, e, b, "R-1")
	if status, raw := closeBatch(t, e, e.apprJWT, b.ID, map[string]string{"reason": "x"}); status != http.StatusConflict {
		t.Errorf("close a completed batch = %d %s; want 409", status, raw)
	}
}
