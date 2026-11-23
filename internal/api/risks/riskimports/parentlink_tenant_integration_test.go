//go:build integration

// #2957 — risk import parent links across batches, the backstop that fails
// a row still waiting when its batch completes, and the export's
// parent_risk_ids. Same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

func uploadBatch(t *testing.T, e env, csv string) riskimport.Batch {
	t.Helper()
	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	return decode[riskimport.Batch](t, raw)
}

func approveOne(t *testing.T, e env, b riskimport.Batch, ref string) riskimport.RowDecision {
	t.Helper()
	status, raw := approveRow(t, e, e.apprJWT, b.ID, stagedByRef(t, b, ref).ID)
	if status != http.StatusOK {
		t.Fatalf("approve %s = %d %s", ref, status, raw)
	}
	return decode[riskimport.RowDecision](t, raw)
}

// A child whose parent is a pending row of another batch waits, also after
// its own batch completes, and is linked when that row is approved. A
// rejected parent row leaves it waiting while another pending row carries
// the ID.
func TestRiskImport_ParentLinkAcrossBatches(t *testing.T) {
	e := newEnv(t)

	children := uploadBatch(t, e, "Risk ID,Title,Category,Level,Parent Risk ID\n"+
		"R-CHILD,Child in its own import,operational,team,R-PARENT\n")
	first := uploadBatch(t, e, "Risk ID,Title,Category,Level\nR-PARENT,Parent rejected here,operational,org\n")
	second := uploadBatch(t, e, "Risk ID,Title,Category,Level\nR-PARENT,Parent approved here,operational,org\n")

	if d := approveOne(t, e, children, "R-CHILD"); d.ParentLinkStatus != riskimport.ParentLinkWaiting {
		t.Fatalf("R-CHILD decision = %+v; want waiting on the other batch's row", d)
	}
	got := getBatch(t, e, children.ID)
	if got.Status != riskimport.BatchStatusCompleted {
		t.Fatalf("children batch status = %s; want completed", got.Status)
	}
	// Its batch completed, but a pending row still carries the parent ID.
	child := stagedByRef(t, got, "R-CHILD")
	if child.ParentLinkStatus != riskimport.ParentLinkWaiting || slices.Contains(child.Flags, riskimport.FlagParentLinkFailed) {
		t.Fatalf("R-CHILD after its batch completed = %s %q flags=%q; want still waiting", child.ParentLinkStatus, child.ParentLinkError, child.Flags)
	}

	rejectRow(t, e, first.ID, stagedByRef(t, first, "R-PARENT").ID, "duplicate upload")
	if c := stagedByRef(t, getBatch(t, e, children.ID), "R-CHILD"); c.ParentLinkStatus != riskimport.ParentLinkWaiting {
		t.Fatalf("R-CHILD after one parent row was rejected = %s %q; want waiting on the other", c.ParentLinkStatus, c.ParentLinkError)
	}

	parent := approveOne(t, e, second, "R-PARENT")
	c := stagedByRef(t, getBatch(t, e, children.ID), "R-CHILD")
	if c.ParentLinkStatus != riskimport.ParentLinkLinked || c.ParentRiskID != parent.RiskID {
		t.Fatalf("R-CHILD = %s %s %q; want linked under %s", c.ParentLinkStatus, c.ParentRiskID, c.ParentLinkError, parent.RiskID)
	}
	if ps := parentsOf(t, e, c.RiskID); !slices.Equal(ps, []string{parent.RiskID}) {
		t.Errorf("R-CHILD risk parents = %q", ps)
	}
	// The link's audit row names the child's batch and row, and the other
	// batch whose decision settled it.
	rows := importLinkRows(t, e, parent.RiskID)
	if len(rows) != 1 {
		t.Fatalf("child_linked rows = %+v; want one", rows)
	}
	a := rows[0].After
	if rows[0].Actor != e.approver.String() || a["import_batch_id"] != children.ID || a["import_row_id"] != c.ID ||
		a["settled_by_import_batch_id"] != second.ID || !strings.Contains(a["note"].(string), "another import") {
		t.Errorf("child_linked = actor %s %+v", rows[0].Actor, a)
	}
}

// A row still waiting when its batch completes, on an ID no pending row of
// the tenant carries any more, fails with the reason and the flag. The
// parent row is removed out of band here: no decision path strands a row.
func TestRiskImport_StrandedWaitingFailsOnCompletion(t *testing.T) {
	e := newEnv(t)
	children := uploadBatch(t, e, "Risk ID,Title,Category,Level,Parent Risk ID\n"+
		"R-CHILD,Child,operational,team,R-GONE\n"+
		"R-LAST,Completes the batch,operational,team,\n")
	parents := uploadBatch(t, e, "Risk ID,Title,Category,Level\nR-GONE,Parent,operational,org\n")

	if d := approveOne(t, e, children, "R-CHILD"); d.ParentLinkStatus != riskimport.ParentLinkWaiting {
		t.Fatalf("R-CHILD decision = %+v; want waiting", d)
	}
	if _, err := e.admin.Exec(context.Background(),
		`DELETE FROM risk_import_rows WHERE id = $1::uuid`, stagedByRef(t, parents, "R-GONE").ID); err != nil {
		t.Fatalf("remove parent row: %v", err)
	}
	approveOne(t, e, children, "R-LAST")

	got := getBatch(t, e, children.ID)
	if got.Status != riskimport.BatchStatusCompleted {
		t.Fatalf("batch status = %s", got.Status)
	}
	c := stagedByRef(t, got, "R-CHILD")
	if c.ParentLinkStatus != riskimport.ParentLinkFailed || !slices.Contains(c.Flags, riskimport.FlagParentLinkFailed) ||
		!strings.Contains(c.ParentLinkError, `still waiting on parent risk ID "R-GONE" when this import completed`) {
		t.Fatalf("R-CHILD = %s %q flags=%q; want failed as stranded", c.ParentLinkStatus, c.ParentLinkError, c.Flags)
	}
	if c.Status != riskimport.RowStatusApproved || c.RiskID == "" || len(parentsOf(t, e, c.RiskID)) != 0 {
		t.Errorf("R-CHILD status=%s risk=%q; want imported and unlinked", c.Status, c.RiskID)
	}
}

// The export's parent_risk_ids links a re-imported risk under each id that
// is a risk of the uploading tenant; an id that is not stays unmapped.
func TestRiskImport_ParentRiskIDsRoundTrip(t *testing.T) {
	e := newEnv(t)
	p1 := seedRiskAt(t, e.app, e.tenant, "P-ONE", dbx.RiskLevelCompany)
	p2 := seedRiskAt(t, e.app, e.tenant, "P-TWO", dbx.RiskLevelCompany)
	other := dbtest.SeedTenant(t, e.admin, cleanupTables()...)
	foreign := seedRiskAt(t, e.app, other, "F-ONE", dbx.RiskLevelCompany)

	csv := "Risk ID,Title,Category,Level,Parent Risk ID,parent_risk_ids\n" +
		"R-BOTH,Under both,operational,team,," + p1 + ";" + p2 + "\n" +
		"R-SOME,One resolves,operational,team,," + foreign + ";" + p1 + ";not-a-uuid\n" +
		"R-NONE,None resolve,operational,team,," + foreign + "\n" +
		"R-REF,Parent ref wins,operational,team,P-TWO," + p1 + "\n" +
		"R-LEVEL,Not below,operational,company,," + p2 + "\n"
	b := uploadBatch(t, e, csv)
	if b.StagedCount != 5 {
		t.Fatalf("staged = %d rejected = %+v", b.StagedCount, b.Rejected)
	}
	type staged struct {
		ids      []string
		unmapped string
	}
	for ref, w := range map[string]staged{
		"R-BOTH":  {[]string{p1, p2}, ""},
		"R-SOME":  {[]string{p1}, foreign + ";not-a-uuid"},
		"R-NONE":  {nil, foreign},
		"R-REF":   {nil, p1},
		"R-LEVEL": {[]string{p2}, ""},
	} {
		r := stagedByRef(t, b, ref)
		if !slices.Equal(r.ParentRiskIDs, w.ids) || r.Unmapped["parent_risk_ids"] != w.unmapped {
			t.Errorf("staged %s ids=%q unmapped=%q; want %+v", ref, r.ParentRiskIDs, r.Unmapped["parent_risk_ids"], w)
		}
	}
	if !slices.Contains(b.UnmappedColumns, "parent_risk_ids") {
		t.Errorf("unmapped columns = %q; rows still hold unresolved ids", b.UnmappedColumns)
	}

	if status, raw := approve(t, e, e.apprJWT, b.ID); status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	got := getBatch(t, e, b.ID)
	type linked struct {
		status, first string
		parents       []string
		errorHas      string
	}
	for ref, w := range map[string]linked{
		"R-BOTH":  {riskimport.ParentLinkLinked, p1, sorted(p1, p2), ""},
		"R-SOME":  {riskimport.ParentLinkLinked, p1, []string{p1}, ""},
		"R-NONE":  {riskimport.ParentLinkNone, "", nil, ""},
		"R-REF":   {riskimport.ParentLinkLinked, p2, []string{p2}, ""},
		"R-LEVEL": {riskimport.ParentLinkFailed, "", nil, "level must be below"},
	} {
		r := stagedByRef(t, got, ref)
		if r.ParentLinkStatus != w.status || r.ParentRiskID != w.first || !strings.Contains(r.ParentLinkError, w.errorHas) ||
			slices.Contains(r.Flags, riskimport.FlagParentLinkFailed) != (w.status == riskimport.ParentLinkFailed) {
			t.Errorf("%s = %s %s %q flags=%q; want %+v", ref, r.ParentLinkStatus, r.ParentRiskID, r.ParentLinkError, r.Flags, w)
		}
		if ps := parentsOf(t, e, r.RiskID); !slices.Equal(ps, w.parents) {
			t.Errorf("%s risk parents = %q; want %q", ref, ps, w.parents)
		}
	}
	// Each link writes its child_linked row by the approver.
	for _, row := range importLinkRows(t, e, p1) {
		if row.Actor != e.approver.String() || row.After["import_batch_id"] != b.ID {
			t.Errorf("child_linked on P-ONE = %s %+v", row.Actor, row.After)
		}
	}
	if n := len(importLinkRows(t, e, p1)); n != 2 {
		t.Errorf("child_linked rows on P-ONE = %d; want 2 (R-BOTH, R-SOME)", n)
	}
	var foreignChildren int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_aggregations WHERE parent_risk_id = $1::uuid`, foreign).Scan(&foreignChildren); err != nil {
		t.Fatalf("count foreign children: %v", err)
	}
	if foreignChildren != 0 {
		t.Errorf("another tenant's risk gained %d children", foreignChildren)
	}
}

func sorted(s ...string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
