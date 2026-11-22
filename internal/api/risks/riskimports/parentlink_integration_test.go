//go:build integration

// #2846 — a parent-risk-ID column links imported risks under their parents
// after approval, through the risk store's parent/child path and its rules.
// A link that breaks a rule, or names an unknown parent, fails on that row
// and the risk still imports. Same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// seedRiskAt creates a risk of tenant at level, carrying ref as its
// organisation risk ID.
func seedRiskAt(t *testing.T, app *pgxpool.Pool, tenant, ref string, level dbx.RiskLevel) string {
	t.Helper()
	r, err := risk.NewStore(app).Create(dbtest.WithTenantCtx(t, tenant), risk.CreateInput{
		Title:       "Seeded " + ref,
		CategoryRef: "operational",
		ExternalRef: ref,
		Level:       level,
		Actor:       "test-2846",
	})
	if err != nil {
		t.Fatalf("seed risk %s: %v", ref, err)
	}
	return r.ID.String()
}

// parentsOf lists the parent risk ids linked over child (any tenant).
func parentsOf(t *testing.T, e env, child string) []string {
	t.Helper()
	rows, err := e.admin.Query(context.Background(),
		`SELECT parent_risk_id::text FROM risk_aggregations WHERE child_risk_id = $1::uuid ORDER BY 1`, child)
	if err != nil {
		t.Fatalf("read parents of %s: %v", child, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan parent: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func rejectRow(t *testing.T, e env, batchID, rowID, reason string) {
	t.Helper()
	status, raw := call(t, e, http.MethodPost, e.apprJWT,
		"/v1/risks/imports/"+batchID+"/rows/"+rowID+"/reject", map[string]string{"reason": reason})
	if status != http.StatusOK {
		t.Fatalf("reject = %d %s", status, raw)
	}
}

func TestRiskImport_ParentLinks(t *testing.T) {
	e := newEnv(t)
	existing := seedRiskAt(t, e.app, e.tenant, "E-COMPANY", dbx.RiskLevelCompany)
	// Another tenant holds a company risk under the ID a row names: it is
	// never matched, so the row's parent is unknown here.
	other := dbtest.SeedTenant(t, e.admin, cleanupTables()...)
	foreign := seedRiskAt(t, e.app, other, "F-PARENT", dbx.RiskLevelCompany)

	csv := "Risk ID,Title,Category,Level,Parent Risk ID\n" +
		// A new child above its new parent in the file: it waits for the
		// parent row, and is linked when that row is approved.
		"R-CHILD,New child of a new parent,operational,team,R-PARENT\n" +
		"R-PARENT,New parent,operational,org,\n" +
		// A new child of an existing risk, named in another case.
		"R-UNDER-E,New child of an existing parent,operational,org,e-company\n" +
		// A row naming itself is a cycle.
		"R-SELF,Names itself,operational,org,R-SELF\n" +
		// A loop across two rows: R-LOOP-A links under R-LOOP-B, then
		// R-LOOP-B under R-LOOP-A would be a cycle.
		"R-LOOP-B,Loop org end,operational,org,R-LOOP-A\n" +
		"R-LOOP-A,Loop team end,operational,team,R-LOOP-B\n" +
		// Same level as its parent.
		"R-LEVEL,Not below its parent,operational,org,R-PARENT\n" +
		"R-UNKNOWN,Unknown parent,operational,team,R-NOWHERE\n" +
		"R-FOREIGN,Another tenant's parent,operational,team,F-PARENT\n" +
		// A child waiting on a row that is then rejected.
		"R-ORPHAN,Child of a rejected row,operational,team,R-REJECTED\n" +
		"R-REJECTED,Rejected parent,operational,org,\n" +
		"R-PLAIN,No parent,operational,team,\n"

	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	if b.StagedCount != 12 || b.RejectedCount != 0 || len(b.UnmappedColumns) != 0 {
		t.Fatalf("batch staged=%d rejected=%+v unmapped=%q", b.StagedCount, b.Rejected, b.UnmappedColumns)
	}
	if r := stagedByRef(t, b, "R-UNDER-E"); r.ParentRef != "e-company" || r.ParentLinkStatus != riskimport.ParentLinkNone {
		t.Errorf("staged R-UNDER-E parent=%q status=%q", r.ParentRef, r.ParentLinkStatus)
	}

	// R-ORPHAN is approved while its parent row is pending, so it waits;
	// rejecting the parent row then fails its link.
	orphan := stagedByRef(t, b, "R-ORPHAN")
	status, raw = approveRow(t, e, e.apprJWT, b.ID, orphan.ID)
	if status != http.StatusOK {
		t.Fatalf("approve R-ORPHAN = %d %s", status, raw)
	}
	if d := decode[riskimport.RowDecision](t, raw); d.ParentLinkStatus != riskimport.ParentLinkWaiting {
		t.Errorf("R-ORPHAN decision = %+v; want waiting", d)
	}
	rejectRow(t, e, b.ID, stagedByRef(t, b, "R-REJECTED").ID, "not ours")

	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	res := decode[riskimport.ApproveResult](t, raw)
	// Every row imports, however its link went.
	if res.ApprovedCount != 10 || len(res.Skipped) != 0 || len(res.Rejected) != 0 {
		t.Fatalf("approve result = %+v", res)
	}

	got := getBatch(t, e, b.ID)
	riskOf := func(ref string) string { return stagedByRef(t, got, ref).RiskID }
	type want struct {
		status, parent, errorHas string
	}
	wants := map[string]want{
		"R-CHILD":   {riskimport.ParentLinkLinked, riskOf("R-PARENT"), ""},
		"R-PARENT":  {riskimport.ParentLinkNone, "", ""},
		"R-UNDER-E": {riskimport.ParentLinkLinked, existing, ""},
		"R-SELF":    {riskimport.ParentLinkFailed, "", "cycle"},
		"R-LOOP-A":  {riskimport.ParentLinkLinked, riskOf("R-LOOP-B"), ""},
		"R-LOOP-B":  {riskimport.ParentLinkFailed, "", "cycle"},
		"R-LEVEL":   {riskimport.ParentLinkFailed, "", "level must be below"},
		"R-UNKNOWN": {riskimport.ParentLinkFailed, "", `"R-NOWHERE" matches no risk`},
		"R-FOREIGN": {riskimport.ParentLinkFailed, "", `"F-PARENT" matches no risk`},
		"R-ORPHAN":  {riskimport.ParentLinkFailed, "", `"R-REJECTED" matches no risk`},
		"R-PLAIN":   {riskimport.ParentLinkNone, "", ""},
	}
	for ref, w := range wants {
		r := stagedByRef(t, got, ref)
		if r.Status != riskimport.RowStatusApproved || r.RiskID == "" {
			t.Errorf("%s status=%s risk=%q; want approved with a risk", ref, r.Status, r.RiskID)
			continue
		}
		if r.ParentLinkStatus != w.status || r.ParentRiskID != w.parent ||
			(w.errorHas == "") != (r.ParentLinkError == "") || !strings.Contains(r.ParentLinkError, w.errorHas) {
			t.Errorf("%s link status=%q parent=%q error=%q; want %+v", ref, r.ParentLinkStatus, r.ParentRiskID, r.ParentLinkError, w)
		}
		failed := slices.Contains(r.Flags, riskimport.FlagParentLinkFailed)
		if failed != (w.status == riskimport.ParentLinkFailed) {
			t.Errorf("%s flags=%q; parent_link_failed iff the link failed", ref, r.Flags)
		}
		// The link in the risk hierarchy is exactly the recorded one.
		parents := parentsOf(t, e, r.RiskID)
		if w.parent == "" && len(parents) != 0 || w.parent != "" && !slices.Equal(parents, []string{w.parent}) {
			t.Errorf("%s risk parents = %q; want %q", ref, parents, w.parent)
		}
	}
	if r := stagedByRef(t, got, "R-REJECTED"); r.Status != riskimport.RowStatusRejected {
		t.Errorf("R-REJECTED status = %s", r.Status)
	}
	if n := len(parentsOf(t, e, foreign)); n != 0 {
		t.Errorf("another tenant's risk gained %d parents", n)
	}
	var foreignChildren int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_aggregations WHERE parent_risk_id = $1::uuid`, foreign).Scan(&foreignChildren); err != nil {
		t.Fatalf("count foreign children: %v", err)
	}
	if foreignChildren != 0 {
		t.Errorf("another tenant's risk gained %d children", foreignChildren)
	}

	// The link is the store's own: the parent lists the child with its
	// roll-up, as a link made through POST /v1/risks/{id}/children does.
	status, raw = call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/"+existing+"/children", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), riskOf("R-UNDER-E")) {
		t.Errorf("children of the existing parent = %d %s", status, raw)
	}
}

// A child that is not 5x5-scored cannot roll up: under a FAIR batch the
// link fails on the row and the risk imports.
func TestRiskImport_ParentLinkNon5x5Child(t *testing.T) {
	e := newEnv(t)
	seedRiskAt(t, e.app, e.tenant, "E-PARENT", dbx.RiskLevelCompany)

	csv := "Risk ID,Title,Category,Level,Parent Risk ID\n" +
		"R-FAIR,FAIR child,operational,team,E-PARENT\n"
	status, raw := uploadWithMethodology(t, e, e.grcJWT, "fair", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	status, raw = approveRow(t, e, e.apprJWT, b.ID, stagedByRef(t, b, "R-FAIR").ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	d := decode[riskimport.RowDecision](t, raw)
	if d.Status != riskimport.RowStatusApproved || d.RiskID == "" ||
		d.ParentLinkStatus != riskimport.ParentLinkFailed || !strings.Contains(d.ParentLinkError, "5x5") {
		t.Fatalf("decision = %+v", d)
	}
	var methodology string
	if err := e.admin.QueryRow(context.Background(),
		`SELECT methodology::text FROM risks WHERE id = $1::uuid`, d.RiskID).Scan(&methodology); err != nil {
		t.Fatalf("read methodology: %v", err)
	}
	if methodology != "fair" {
		t.Errorf("risk methodology = %q", methodology)
	}
	if parents := parentsOf(t, e, d.RiskID); len(parents) != 0 {
		t.Errorf("FAIR child linked under %q", parents)
	}
	r := stagedByRef(t, getBatch(t, e, b.ID), "R-FAIR")
	if !slices.Contains(r.Flags, riskimport.FlagParentLinkFailed) || r.ParentLinkError != d.ParentLinkError {
		t.Errorf("row flags=%q error=%q", r.Flags, r.ParentLinkError)
	}
}

// A row whose parent's row is a duplicate links under the existing risk
// carrying that ID: the duplicate creates nothing and changes nothing.
func TestRiskImport_ParentLinkThroughDuplicate(t *testing.T) {
	e := newEnv(t)
	existing := seedRiskAt(t, e.app, e.tenant, "R-PARENT", dbx.RiskLevelOrg)

	csv := "Risk ID,Title,Category,Level,Parent Risk ID\n" +
		"R-PARENT,Already imported,operational,org,\n" +
		"R-CHILD,Child,operational,team,R-PARENT\n"
	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	if status, raw := approve(t, e, e.apprJWT, b.ID); status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	got := getBatch(t, e, b.ID)
	if r := stagedByRef(t, got, "R-PARENT"); r.Status != riskimport.RowStatusDuplicate || r.RiskID != existing {
		t.Errorf("R-PARENT = %s %s", r.Status, r.RiskID)
	}
	child := stagedByRef(t, got, "R-CHILD")
	if child.ParentLinkStatus != riskimport.ParentLinkLinked || child.ParentRiskID != existing {
		t.Errorf("R-CHILD link = %s %s %q", child.ParentLinkStatus, child.ParentRiskID, child.ParentLinkError)
	}
}
