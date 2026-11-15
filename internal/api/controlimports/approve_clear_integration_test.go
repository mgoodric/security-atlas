//go:build integration

// #1783 — integration tests for approve-clear: one action that approves
// every pending row whose top candidate is crosswalk-derived and reaches all
// of the row's tested criteria. Real Postgres with RLS and the assembled
// router; fixtures from integration_test.go and decisions_integration_test.go.
package controlimports_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// clearCSV has two clear rows (CLR-01 reaches 2 of 2 through the mfa anchor,
// CLR-02 reaches 1 of 1 through the review anchor), one whose top crosswalk
// candidate reaches only one of two criteria (PART-01), and two whose top
// candidate is keyword-only (KW-01 has a criterion no anchor reaches, KW-02
// has none).
const clearCSV = "Control ID,Title,Description,Tested Under,Implementation Type\n" +
	"CLR-01,MFA for workforce,All workforce accounts require multi-factor authentication.,CC6.1; CC6.2,automated\n" +
	"CLR-02,Quarterly access review,Privileged access is reviewed every quarter.,CC6.2,\n" +
	"PART-01,MFA and monitoring,Accounts use multi-factor authentication and events are monitored.,CC6.1; CC7.1,\n" +
	"KW-01,Incident monitoring,Security events are monitored and triaged daily.,CC7.1,\n" +
	"KW-02,Background checks,New hires pass a background check before their start date.,,\n"

func stageClear(t *testing.T, env testEnv, framework string) controlimport.Batch {
	t.Helper()
	status, raw := doUpload(t, env, upload{filename: "clear.csv", payload: []byte(clearCSV), framework: framework})
	if status != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", status, raw)
	}
	b := decodeBatch(t, raw)
	if b.SuggestionsStatus != controlimport.SuggestionsReady {
		t.Fatalf("suggestions = %s, want ready", b.SuggestionsStatus)
	}
	return b
}

func approveClear(t *testing.T, env testEnv, bearer, batch string) (int, []byte) {
	t.Helper()
	return doPost(t, env, bearer, "/v1/controls/imports/"+batch+"/approve-clear", map[string]any{})
}

func decodeClear(t *testing.T, raw []byte) controlimport.ApproveClearResult {
	t.Helper()
	var res controlimport.ApproveClearResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode approve-clear: %v; body=%s", err, raw)
	}
	return res
}

func rowStatuses(t *testing.T, admin *pgxpool.Pool, batch string) map[string]string {
	t.Helper()
	rows, err := admin.Query(context.Background(),
		`SELECT org_control_id, status FROM control_import_rows WHERE batch_id = $1::uuid`, batch)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		out[id] = status
	}
	return out
}

// ----- AC: only qualifying rows are approved -----

func TestApproveClearApprovesOnlyClearRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)

	b := stageClear(t, env.testEnv, cat.framework)
	// The fixture's premise: PART-01's top candidate is crosswalk but
	// reaches 1 of 2; KW-01 and KW-02 have keyword tops.
	if c := rowByOrgID(t, b, "PART-01").Candidates; len(c) == 0 || c[0].Source != controlimport.SourceCrosswalk || len(c[0].Reaches) != 1 {
		t.Fatalf("PART-01 candidates = %+v, want a crosswalk top reaching 1 of 2", c)
	}
	for _, id := range []string{"KW-01", "KW-02"} {
		if c := rowByOrgID(t, b, id).Candidates; len(c) > 0 && c[0].Source != controlimport.SourceKeyword {
			t.Fatalf("%s candidates = %+v, want a keyword top", id, c)
		}
	}

	status, raw := approveClear(t, env.testEnv, env.bearer, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve-clear = %d; body=%s", status, raw)
	}
	res := decodeClear(t, raw)
	if res.Qualifying != 2 || res.ApprovedCount != 2 || len(res.Duplicates) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, want 2 qualifying, 2 approved", res)
	}
	want := map[string]string{"CLR-01": cat.anchors["mfa"], "CLR-02": cat.anchors["review"]}
	for _, d := range res.Approved {
		if d.Status != controlimport.RowStatusApproved || d.SCFID != want[d.ExternalID] || d.DecidedBy != env.user.String() || d.ControlID == "" {
			t.Errorf("approved %+v, want %s with %s by %s", d, d.ExternalID, want[d.ExternalID], env.user)
		}
	}

	got := rowStatuses(t, admin, b.ID)
	for id, s := range map[string]string{"CLR-01": "approved", "CLR-02": "approved", "PART-01": "pending", "KW-01": "pending", "KW-02": "pending"} {
		if got[id] != s {
			t.Errorf("%s status = %q, want %s", id, got[id], s)
		}
	}
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM controls WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	if n != 2 {
		t.Errorf("%d controls in the tenant, want 2", n)
	}
	var audits int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_import_audit_log WHERE batch_id = $1::uuid AND action = 'approve' AND actor = $2 AND reason = $3`,
		b.ID, env.user.String(), controlimport.BulkAuditReason).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 2 {
		t.Errorf("%d approve audit rows marked %q, want 2", audits, controlimport.BulkAuditReason)
	}

	// A second run finds nothing left to approve.
	status, raw = approveClear(t, env.testEnv, env.bearer, b.ID)
	if status != http.StatusOK || decodeClear(t, raw).ApprovedCount != 0 {
		t.Errorf("second approve-clear = %d %s, want 200 approving none", status, raw)
	}

	// Another tenant cannot approve-clear this batch.
	other := decisionServer(t, app, freshTenant(t, admin))
	status, _ = approveClear(t, other.testEnv, other.bearer, b.ID)
	if status != http.StatusNotFound {
		t.Errorf("tenant B approve-clear of tenant A's batch = %d, want 404", status)
	}
}

// ----- AC: each control has the same properties as a single approval -----

// controlProps is what an approval writes onto a control.
type controlProps struct {
	BundleID, Title, Description, ImplType, Applicability, AnchorID, ExternalRefs, CreatedBy string
}

func readControlProps(t *testing.T, admin *pgxpool.Pool, controlID string) controlProps {
	t.Helper()
	var p controlProps
	if err := admin.QueryRow(context.Background(), `
		SELECT bundle_id, title, COALESCE(description, ''), implementation_type::text,
		       COALESCE(applicability_expr::text, ''), COALESCE(scf_anchor_id::text, ''), external_refs::text,
		       COALESCE(bundle_uploaded_by, '')
		FROM controls WHERE id = $1::uuid`, controlID).Scan(
		&p.BundleID, &p.Title, &p.Description, &p.ImplType, &p.Applicability, &p.AnchorID, &p.ExternalRefs, &p.CreatedBy); err != nil {
		t.Fatalf("read control %s: %v", controlID, err)
	}
	return p
}

func TestApproveClearMatchesSingleApproval(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	single := decisionServer(t, app, freshTenant(t, admin))
	bulk := decisionServer(t, app, freshTenant(t, admin))

	sb := stageClear(t, single.testEnv, cat.framework)
	bb := stageClear(t, bulk.testEnv, cat.framework)
	status, raw := approveClear(t, bulk.testEnv, bulk.bearer, bb.ID)
	if status != http.StatusOK {
		t.Fatalf("approve-clear = %d; body=%s", status, raw)
	}
	byOrg := map[string]controlimport.RowDecision{}
	for _, d := range decodeClear(t, raw).Approved {
		byOrg[d.ExternalID] = d
	}

	for _, orgID := range []string{"CLR-01", "CLR-02"} {
		status, raw := doPost(t, single.testEnv, single.bearer, decisionPath(sb.ID, rowByOrgID(t, sb, orgID).ID, "approve"),
			map[string]any{"candidate_rank": 1})
		if status != http.StatusOK {
			t.Fatalf("single approve %s = %d; body=%s", orgID, status, raw)
		}
		s := decodeDecision(t, raw)
		b, ok := byOrg[orgID]
		if !ok {
			t.Fatalf("approve-clear did not approve %s", orgID)
		}
		if s.BundleID != b.BundleID || s.SCFID != b.SCFID || s.SCFAnchorID != b.SCFAnchorID || s.SCFRelease != b.SCFRelease ||
			s.AIAssisted != b.AIAssisted || !reflect.DeepEqual(s.ExternalRefs, b.ExternalRefs) {
			t.Errorf("%s: single decision %+v, bulk %+v", orgID, s, b)
		}
		sp, bp := readControlProps(t, admin, s.ControlID), readControlProps(t, admin, b.ControlID)
		// bundle_uploaded_by names each tenant's own approver; compare it to them.
		if sp.CreatedBy != single.user.String() || bp.CreatedBy != bulk.user.String() {
			t.Errorf("%s created_by single=%q bulk=%q, want each approver", orgID, sp.CreatedBy, bp.CreatedBy)
		}
		sp.CreatedBy, bp.CreatedBy = "", ""
		if sp != bp {
			t.Errorf("%s: single control %+v\nbulk control %+v", orgID, sp, bp)
		}
	}

	// Only the bulk approvals' audit rows carry the approve-clear reason.
	for _, tc := range []struct {
		batch, want string
	}{{sb.ID, ""}, {bb.ID, controlimport.BulkAuditReason}} {
		rows, err := admin.Query(context.Background(),
			`SELECT COALESCE(reason, '') FROM control_import_audit_log WHERE batch_id = $1::uuid AND action = 'approve'`, tc.batch)
		if err != nil {
			t.Fatalf("read audit rows: %v", err)
		}
		n := 0
		for rows.Next() {
			var reason string
			if err := rows.Scan(&reason); err != nil {
				t.Fatalf("scan audit row: %v", err)
			}
			n++
			if reason != tc.want {
				t.Errorf("batch %s audit reason = %q, want %q", tc.batch, reason, tc.want)
			}
		}
		rows.Close()
		if n != 2 {
			t.Errorf("batch %s has %d approve audit rows, want 2", tc.batch, n)
		}
	}
}

// ----- AC: approve-clear without an approver is refused -----

func TestApproveClearNeedsAPerson(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)
	b := stageClear(t, env.testEnv, cat.framework)

	for name, bearer := range map[string]string{"machine token": env.machine, "credential naming no user": env.nouser} {
		status, raw := approveClear(t, env.testEnv, bearer, b.ID)
		if status != http.StatusForbidden || !strings.Contains(string(raw), "a person must approve") {
			t.Errorf("%s approve-clear = %d %s, want 403", name, status, raw)
		}
	}
	for id, s := range rowStatuses(t, admin, b.ID) {
		if s != "pending" {
			t.Errorf("%s status = %s after refused approve-clear, want pending", id, s)
		}
	}
}

// ----- concurrency: a running approve-clear refuses a second at once -----

func TestApproveClearRefusedWhileAnotherRuns(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)
	b := stageClear(t, env.testEnv, cat.framework)

	// Stand in for a running approve-clear: hold the batch row lock.
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM control_import_batches WHERE id = $1::uuid FOR UPDATE`, b.ID); err != nil {
		t.Fatalf("lock batch: %v", err)
	}

	status, raw := approveClear(t, env.testEnv, env.bearer, b.ID)
	if status != http.StatusConflict || !strings.Contains(string(raw), "approve-clear already running") {
		t.Fatalf("approve-clear while locked = %d %s, want 409 already running", status, raw)
	}
	for id, s := range rowStatuses(t, admin, b.ID) {
		if s != "pending" {
			t.Errorf("%s status = %s after refused approve-clear, want pending", id, s)
		}
	}

	// A single approval is not held up by a running approve-clear.
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, rowByOrgID(t, b, "CLR-01").ID, "approve"),
		map[string]any{"candidate_rank": 1})
	if status != http.StatusOK {
		t.Fatalf("single approve while batch locked = %d %s", status, raw)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	status, raw = approveClear(t, env.testEnv, env.bearer, b.ID)
	if status != http.StatusOK || decodeClear(t, raw).ApprovedCount != 1 {
		t.Errorf("approve-clear after release = %d %s, want 200 approving CLR-02 only", status, raw)
	}
}

// ----- concurrency: approve-clear racing a single approval and itself -----

func TestApproveClearConcurrentWithSingleApproveAndItself(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)
	b := stageClear(t, env.testEnv, cat.framework)
	clr02 := rowByOrgID(t, b, "CLR-02").ID

	type outcome struct {
		status int
		raw    []byte
	}
	var wg sync.WaitGroup
	clears := make([]outcome, 2)
	var single outcome
	start := make(chan struct{})
	for i := range clears {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			clears[i].status, clears[i].raw = approveClear(t, env.testEnv, env.bearer, b.ID)
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		single.status, single.raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, clr02, "approve"),
			map[string]any{"candidate_rank": 1})
	}()
	close(start)
	wg.Wait()

	// The approve-clears either overlap (one is refused with 409) or run
	// one after the other (the second finds less, or nothing, to approve).
	approved, ran := 0, 0
	for i, c := range clears {
		switch c.status {
		case http.StatusOK:
			ran++
		case http.StatusConflict:
			if !strings.Contains(string(c.raw), "approve-clear already running") {
				t.Errorf("approve-clear %d = 409 %s, want already running", i, c.raw)
			}
			continue
		default:
			t.Fatalf("approve-clear %d = %d; body=%s", i, c.status, c.raw)
		}
		res := decodeClear(t, c.raw)
		approved += res.ApprovedCount
		for _, s := range res.Skipped {
			if s.ExternalID != "CLR-02" || !strings.Contains(s.Reason, "already decided") {
				t.Errorf("approve-clear %d skipped %+v, want only CLR-02 decided by the single approval", i, s)
			}
		}
	}
	if ran == 0 {
		t.Fatal("both approve-clears were refused, want at least one to run")
	}
	switch single.status {
	case http.StatusOK:
		approved++
	case http.StatusConflict:
		if !strings.Contains(string(single.raw), "already decided") {
			t.Errorf("single approve = 409 %s, want already decided", single.raw)
		}
	default:
		t.Fatalf("single approve = %d; body=%s", single.status, single.raw)
	}
	if approved != 2 {
		t.Errorf("%d approvals across the three requests, want exactly 2 (CLR-01, CLR-02)", approved)
	}
	for _, bundle := range []string{"clr-01", "clr-02"} {
		var n int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM controls WHERE tenant_id = $1 AND bundle_id = $2`, tenant, bundle).Scan(&n); err != nil {
			t.Fatalf("count controls: %v", err)
		}
		if n != 1 {
			t.Errorf("%d %s controls, want 1", n, bundle)
		}
	}
	var audits int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_import_audit_log WHERE batch_id = $1::uuid AND action = 'approve'`, b.ID).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 2 {
		t.Errorf("%d approve audit rows, want 2", audits)
	}
}
