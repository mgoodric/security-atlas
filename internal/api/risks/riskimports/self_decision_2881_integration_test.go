//go:build integration

// #2881 — risk import decisions are stricter than the other approver gates,
// by Matt's decision: a grc_engineer never approves or rejects, and the
// person who uploaded a batch never decides its rows. Through the same
// harness as integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// batchRead is GET /v1/risks/imports/{id} with the caller's can_decide.
type batchRead struct {
	riskimport.Batch
	CanDecide bool `json:"can_decide"`
}

func readBatch(t *testing.T, e env, bearer, id string) batchRead {
	t.Helper()
	status, raw := call(t, e, http.MethodGet, bearer, "/v1/risks/imports/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET batch = %d; body=%s", status, raw)
	}
	return decode[batchRead](t, raw)
}

// secondAdmin seeds another person with a live admin grant and returns
// their id and bearer.
func secondAdmin(t *testing.T, e env) (uuid.UUID, string) {
	t.Helper()
	id := seedUser(t, e.admin, e.tenant, "approver-2")
	if _, err := e.admin.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'admin', 'test-2881')`,
		e.tenant, id.String()); err != nil {
		t.Fatalf("grant admin: %v", err)
	}
	claims := testjwt.OwnerFor(uuid.MustParse(e.tenant), []string{"admin"})
	claims.Subject = "user:" + id.String()
	return id, e.issue(t, claims)
}

func countDecided(t *testing.T, e env, batchID string) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_import_rows WHERE batch_id = $1 AND status <> 'pending'`, batchID).Scan(&n); err != nil {
		t.Fatalf("count decided: %v", err)
	}
	return n
}

// TestRiskImport_GRCEngineerCannotDecide: a person with a live grc_engineer
// grant uploads, but approve-all, per-row approve and reject each refuse
// them with 403, and the batch read says they cannot decide.
func TestRiskImport_GRCEngineerCannotDecide(t *testing.T) {
	e := newEnv(t)
	b := stage(t, e, e.grcJWT)
	row := stagedByRef(t, b, "R-001")
	base := "/v1/risks/imports/" + b.ID

	if status, raw := approve(t, e, e.grcJWT, b.ID); status != http.StatusForbidden {
		t.Errorf("grc_engineer approve all = %d, want 403; body=%s", status, raw)
	}
	if status, raw := call(t, e, http.MethodPost, e.grcJWT, base+"/rows/"+row.ID+"/approve", nil); status != http.StatusForbidden {
		t.Errorf("grc_engineer approve row = %d, want 403; body=%s", status, raw)
	}
	if status, raw := call(t, e, http.MethodPost, e.grcJWT, base+"/rows/"+row.ID+"/reject", map[string]string{"reason": "no"}); status != http.StatusForbidden {
		t.Errorf("grc_engineer reject row = %d, want 403; body=%s", status, raw)
	}
	if n := countDecided(t, e, b.ID); n != 0 {
		t.Errorf("refused decisions decided %d rows", n)
	}
	if n := countRisks(t, e); n != 0 {
		t.Errorf("refused decisions created %d risks", n)
	}
	if readBatch(t, e, e.grcJWT, b.ID).CanDecide {
		t.Error("grc_engineer can_decide = true, want false")
	}
	// The admin, who did not upload, decides.
	if !readBatch(t, e, e.apprJWT, b.ID).CanDecide {
		t.Error("admin can_decide = false, want true")
	}
	if status, raw := approveRow(t, e, e.apprJWT, b.ID, row.ID); status != http.StatusOK {
		t.Errorf("admin approve row = %d, want 200; body=%s", status, raw)
	}
}

// TestRiskImport_UploaderCannotDecideOwnBatch: an admin who uploads a batch
// is refused approve-all, per-row approve and reject on it with 403, and
// nothing is decided; a different admin then decides it.
func TestRiskImport_UploaderCannotDecideOwnBatch(t *testing.T) {
	e := newEnv(t)
	b := stage(t, e, e.apprJWT)
	if b.UploaderUserID != e.approver.String() {
		t.Fatalf("uploader_user_id = %q, want %s", b.UploaderUserID, e.approver)
	}
	r1 := stagedByRef(t, b, "R-001")
	r2 := stagedByRef(t, b, "R-002")
	base := "/v1/risks/imports/" + b.ID

	if status, raw := approve(t, e, e.apprJWT, b.ID); status != http.StatusForbidden {
		t.Errorf("uploader approve all = %d, want 403; body=%s", status, raw)
	}
	if status, raw := approveRow(t, e, e.apprJWT, b.ID, r1.ID); status != http.StatusForbidden {
		t.Errorf("uploader approve row = %d, want 403; body=%s", status, raw)
	}
	if status, raw := call(t, e, http.MethodPost, e.apprJWT, base+"/rows/"+r2.ID+"/reject", map[string]string{"reason": "no"}); status != http.StatusForbidden {
		t.Errorf("uploader reject row = %d, want 403; body=%s", status, raw)
	}
	if n := countDecided(t, e, b.ID); n != 0 {
		t.Errorf("refused decisions decided %d rows", n)
	}
	if n := countRisks(t, e); n != 0 {
		t.Errorf("refused decisions created %d risks", n)
	}
	if readBatch(t, e, e.apprJWT, b.ID).CanDecide {
		t.Error("uploader can_decide = true, want false")
	}

	other, otherJWT := secondAdmin(t, e)
	if !readBatch(t, e, otherJWT, b.ID).CanDecide {
		t.Error("second admin can_decide = false, want true")
	}
	status, raw := call(t, e, http.MethodPost, otherJWT, base+"/rows/"+r2.ID+"/reject", map[string]string{"reason": "not ours"})
	if status != http.StatusOK {
		t.Fatalf("second admin reject = %d; body=%s", status, raw)
	}
	if d := decode[riskimport.RowDecision](t, raw); d.DecidedBy != other.String() {
		t.Errorf("reject decided_by = %q, want %s", d.DecidedBy, other)
	}
	status, raw = approve(t, e, otherJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("second admin approve all = %d; body=%s", status, raw)
	}
	if res := decode[riskimport.ApproveResult](t, raw); res.ApprovedCount == 0 {
		t.Errorf("second admin approved nothing: %+v", res)
	}
}

// TestRiskImport_MachineUploadHasNoUploader: a machine credential's upload
// records no uploader, so the self-decision rule binds no one.
func TestRiskImport_MachineUploadHasNoUploader(t *testing.T) {
	e := newEnv(t)
	b := stage(t, e, e.machine)
	if b.UploaderUserID != "" {
		t.Errorf("machine upload uploader_user_id = %q, want empty", b.UploaderUserID)
	}
	if !readBatch(t, e, e.apprJWT, b.ID).CanDecide {
		t.Error("admin can_decide on a machine upload = false, want true")
	}
}
