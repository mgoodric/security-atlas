//go:build integration

// Integration tests for carrying applicability decisions over to a new
// framework version (issue #1793, Statement of Applicability #1744): an
// approved carry-over in the version-migration queue (ADR 0019) copies the
// tenant's decision into its draft revision on N+1 with every field; new
// requirements stay undecided; N is untouched; only the owning tenant's
// decisions carry.

package frameworkscope_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// carryFixture is one framework at two versions with the migration queue
// between them:
//
//	N   (2017): C-01 C-02 C-03 C-04
//	N+1 (2022): C-01 C-02 C-03 C-05
//
//	C-01 exact_code approved
//	C-02 exact_code approved
//	C-03 exact_code pending
//	C-04 removed
//	C-05 added
type carryFixture struct {
	FromVersionID uuid.UUID
	ToVersionID   uuid.UUID
	From          map[string]uuid.UUID // code -> requirement id on N
	To            map[string]uuid.UUID // code -> requirement id on N+1
}

func seedCarryFixture(t *testing.T, admin *pgxpool.Pool) carryFixture {
	t.Helper()
	ctx := context.Background()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	fwID := uuid.New()
	f := carryFixture{
		FromVersionID: uuid.New(),
		ToVersionID:   uuid.New(),
		From:          map[string]uuid.UUID{},
		To:            map[string]uuid.UUID{},
	}
	exec("framework", `INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		VALUES ($1, NULL, $2, 'Carry-over test framework (#1793)', 'tester')`, fwID, "carry-1793-"+uuid.NewString()[:8])
	exec("version N", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2017', 'legacy')`, f.FromVersionID, fwID)
	exec("version N+1", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2022', 'current')`, f.ToVersionID, fwID)
	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []string{
			`DELETE FROM framework_scopes WHERE framework_version_id IN (SELECT id FROM framework_versions WHERE framework_id = $1)`,
			`DELETE FROM framework_version_migrations WHERE framework_id = $1`,
			`DELETE FROM framework_requirements WHERE framework_version_id IN (SELECT id FROM framework_versions WHERE framework_id = $1)`,
			`UPDATE frameworks SET latest_version_id = NULL WHERE id = $1`,
			`DELETE FROM framework_versions WHERE framework_id = $1`,
			`DELETE FROM frameworks WHERE id = $1`,
		} {
			if _, err := admin.Exec(c, q, fwID); err != nil {
				t.Logf("cleanup %q: %v", q, err)
			}
		}
	})

	addReq := func(version uuid.UUID, into map[string]uuid.UUID, code string) {
		id := uuid.New()
		exec("requirement", `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
			VALUES ($1, $2, $3, $4, '')`, id, version, code, "requirement "+code)
		into[code] = id
	}
	for _, code := range []string{"C-01", "C-02", "C-03", "C-04"} {
		addReq(f.FromVersionID, f.From, code)
	}
	for _, code := range []string{"C-01", "C-02", "C-03", "C-05"} {
		addReq(f.ToVersionID, f.To, code)
	}

	queue := func(code, kind, status string, from, to any) {
		exec("migration "+code, `INSERT INTO framework_version_migrations
			(framework_id, from_version_id, to_version_id, from_requirement_id, to_requirement_id,
			 requirement_code, match_kind, status, reviewer_id, decided_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7::framework_version_migration_match_kind,
			        $8::framework_version_migration_status,
			        CASE WHEN $8 = 'pending' THEN NULL ELSE gen_random_uuid() END,
			        CASE WHEN $8 = 'pending' THEN NULL ELSE now() END)`,
			fwID, f.FromVersionID, f.ToVersionID, from, to, code, kind, status)
	}
	queue("C-01", "exact_code", "approved", f.From["C-01"], f.To["C-01"])
	queue("C-02", "exact_code", "approved", f.From["C-02"], f.To["C-02"])
	queue("C-03", "exact_code", "pending", f.From["C-03"], f.To["C-03"])
	queue("C-04", "removed", "pending", f.From["C-04"], nil)
	queue("C-05", "added", "pending", nil, f.To["C-05"])
	return f
}

type carryItem struct {
	RequirementCode string `json:"requirement_code"`
	Reason          string `json:"reason"`
}

type carryResp struct {
	FrameworkScope struct {
		State string `json:"state"`
	} `json:"framework_scope"`
	SourceVersion string  `json:"source_framework_version"`
	SourceScopeID *string `json:"source_framework_scope_id"`
	Copied        []struct {
		RequirementID string `json:"requirement_id"`
	} `json:"copied"`
	Skipped []carryItem `json:"skipped"`
	Flagged []carryItem `json:"flagged"`
}

func (c soaClient) carryOver(scopeID string) (int, carryResp, []byte) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodPost,
		c.base+"/v1/framework-scopes/"+scopeID+"/applicability:carry-over", c.bearer, "")
	var out carryResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(payload, &out); err != nil {
			c.t.Fatalf("unmarshal carry-over: %v; body %s", err, payload)
		}
	}
	return resp.StatusCode, out, payload
}

func (c soaClient) mustCarryOver(scopeID string) carryResp {
	c.t.Helper()
	code, out, payload := c.carryOver(scopeID)
	if code != http.StatusOK {
		c.t.Fatalf("carry-over: status %d; body %s", code, payload)
	}
	return out
}

// decisionsByCode lists a revision and maps requirement code to its decision
// fields; an undecided requirement maps to nil.
type decisionFields struct{ Decision, Justification, ImplementationRef string }

func decisionsByCode(c soaClient, scopeID string) map[string]*decisionFields {
	c.t.Helper()
	out := map[string]*decisionFields{}
	for _, r := range c.mustList(scopeID).Requirements {
		if r.Decision == nil {
			out[r.Code] = nil
			continue
		}
		out[r.Code] = &decisionFields{*r.Decision, r.Justification, r.ImplementationRef}
	}
	return out
}

func carryOverRows(t *testing.T, tenant, scopeID string) []logRow {
	t.Helper()
	var out []logRow
	for _, r := range readWorkflowLog(t, tenant, scopeID) {
		if r.Action == "applicability.carry_over" {
			out = append(out, r)
		}
	}
	return out
}

func reasonsByCode(items []carryItem) map[string]string {
	m := map[string]string{}
	for _, it := range items {
		m[it.RequirementCode] = it.Reason
	}
	return m
}

// activateRevision takes a revision through submit, approve and activate.
func activateRevision(c soaClient, scopeID, approver string) {
	c.t.Helper()
	c.transition(scopeID, "submit", c.bearer, "")
	c.transition(scopeID, "approve", approver, "")
	c.transition(scopeID, "activate", c.bearer,
		fmt.Sprintf(`{"effective_from":%q}`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)))
}

// AC-1 and AC-2: approved carry-overs copy the decision into the N+1 draft
// with all fields, logged as applicability.carry_over naming the source; N
// is unchanged; a requirement new in N+1 stays undecided; pending, removed
// and added rows are flagged. A re-run writes nothing.
func TestHTTP_Applicability_CarryOver(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedCarryFixture(t, admin)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}

	oldRev := c.createScope(f.FromVersionID, "ISMS 2017")
	c.mustSet(oldRev, f.From["C-01"], `{"decision":"excluded","justification":"No cardholder data","implementation_ref":"https://wiki.example/isms/c01"}`)
	c.mustSet(oldRev, f.From["C-02"], `{"decision":"applicable","implementation_ref":"https://wiki.example/isms/c02"}`)
	c.mustSet(oldRev, f.From["C-03"], applicableBody)
	c.mustSet(oldRev, f.From["C-04"], applicableBody)
	activateRevision(c, oldRev, approver)
	before := decisionsByCode(c, oldRev)

	draft := c.createScope(f.ToVersionID, "ISMS 2022")
	res := c.mustCarryOver(draft)

	if res.SourceVersion != "2017" || res.SourceScopeID == nil || *res.SourceScopeID != oldRev {
		t.Fatalf("source = %q / %v, want 2017 / %s", res.SourceVersion, res.SourceScopeID, oldRev)
	}
	if len(res.Copied) != 2 {
		t.Fatalf("copied = %d, want 2; %+v", len(res.Copied), res)
	}
	if res.FrameworkScope.State != "draft" {
		t.Fatalf("draft state after carry-over = %s", res.FrameworkScope.State)
	}
	wantFlags := map[string]string{"C-03": "carry_over_pending", "C-04": "removed_requirement", "C-05": "new_requirement"}
	if got := reasonsByCode(res.Flagged); fmt.Sprint(got) != fmt.Sprint(wantFlags) {
		t.Fatalf("flagged = %v, want %v", got, wantFlags)
	}

	after := decisionsByCode(c, draft)
	if d := after["C-01"]; d == nil || *d != (decisionFields{"excluded", "No cardholder data", "https://wiki.example/isms/c01"}) {
		t.Fatalf("C-01 on N+1 = %+v", d)
	}
	if d := after["C-02"]; d == nil || *d != (decisionFields{"applicable", "", "https://wiki.example/isms/c02"}) {
		t.Fatalf("C-02 on N+1 = %+v", d)
	}
	if after["C-03"] != nil {
		t.Fatalf("C-03 (pending carry-over) on N+1 = %+v, want undecided", after["C-03"])
	}
	if d, ok := after["C-05"]; !ok || d != nil {
		t.Fatalf("C-05 (new in N+1) = %+v (present %v), want undecided", d, ok)
	}

	// N's decisions are unchanged and its revision is still activated.
	if got := decisionsByCode(c, oldRev); fmt.Sprint(deref(got)) != fmt.Sprint(deref(before)) {
		t.Fatalf("N decisions changed: before %v after %v", deref(before), deref(got))
	}
	if st := c.mustList(oldRev).FrameworkScope.State; st != "activated" {
		t.Fatalf("N revision state = %s, want activated", st)
	}
	if n := len(carryOverRows(t, tenant, oldRev)); n != 0 {
		t.Fatalf("carry_over rows on N revision = %d, want 0", n)
	}

	// Each carried decision has a history row naming its source version.
	rows := carryOverRows(t, tenant, draft)
	if len(rows) != 2 {
		t.Fatalf("carry_over rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		src, ok := r.After["carried_over_from"].(map[string]any)
		if !ok {
			t.Fatalf("carry_over row without carried_over_from: %+v", r.After)
		}
		if src["framework_version"] != "2017" || src["framework_version_id"] != f.FromVersionID.String() || src["framework_scope_id"] != oldRev {
			t.Fatalf("carried_over_from = %v", src)
		}
		if r.Actor == "" {
			t.Fatalf("carry_over row missing actor: %+v", r)
		}
	}
	if n := applicabilityUpdates(t, tenant, draft); n != 0 {
		t.Fatalf("applicability.update rows on draft = %d, want 0", n)
	}

	// Idempotent: a re-run copies nothing, reports the copied rows as
	// already decided, and leaves a hand edit alone.
	c.mustSet(draft, f.To["C-02"], `{"decision":"excluded","justification":"Changed in 2022"}`)
	again := c.mustCarryOver(draft)
	if len(again.Copied) != 0 {
		t.Fatalf("re-run copied %d, want 0", len(again.Copied))
	}
	if got := reasonsByCode(again.Skipped); got["C-01"] != "already_decided" || got["C-02"] != "already_decided" {
		t.Fatalf("re-run skipped = %v", got)
	}
	if n := len(carryOverRows(t, tenant, draft)); n != 2 {
		t.Fatalf("carry_over rows after re-run = %d, want 2", n)
	}
	if d := decisionsByCode(c, draft)["C-02"]; d == nil || d.Decision != "excluded" {
		t.Fatalf("hand edit overwritten: C-02 = %+v", d)
	}

	// Only a draft takes carried decisions.
	c.transition(draft, "submit", bearer, "")
	if code, _, payload := c.carryOver(draft); code != http.StatusConflict {
		t.Fatalf("carry-over into review revision: status %d, want 409; body %s", code, payload)
	}
	// A revision on N has no migration leading into it.
	other := c.createScope(f.FromVersionID, "ISMS 2017 rev 2")
	if code, _, payload := c.carryOver(other); code != http.StatusConflict {
		t.Fatalf("carry-over with no source version: status %d, want 409; body %s", code, payload)
	}
}

func deref(m map[string]*decisionFields) map[string]decisionFields {
	out := map[string]decisionFields{}
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// AC-3: carry-over copies only the calling tenant's decisions. Tenant B's
// draft takes B's decisions, never A's; B cannot carry into A's draft; and a
// tenant with no revision on N copies nothing.
func TestHTTP_Applicability_CarryOverRLS(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenantA := soaTenant(t, admin)
	tenantB := soaTenant(t, admin)
	tenantC := soaTenant(t, admin)
	f := seedCarryFixture(t, admin)
	tsA, bearerA, approverA := setupHTTPServer(t, tenantA)
	tsB, bearerB, approverB := setupHTTPServer(t, tenantB)
	tsC, bearerC, _ := setupHTTPServer(t, tenantC)
	a := soaClient{t: t, base: tsA.URL, bearer: bearerA}
	b := soaClient{t: t, base: tsB.URL, bearer: bearerB}
	cc := soaClient{t: t, base: tsC.URL, bearer: bearerC}

	oldA := a.createScope(f.FromVersionID, "A 2017")
	a.mustSet(oldA, f.From["C-01"], `{"decision":"excluded","justification":"tenant A reason"}`)
	a.mustSet(oldA, f.From["C-02"], `{"decision":"excluded","justification":"tenant A reason"}`)
	activateRevision(a, oldA, approverA)

	oldB := b.createScope(f.FromVersionID, "B 2017")
	b.mustSet(oldB, f.From["C-01"], `{"decision":"applicable","implementation_ref":"tenant-b"}`)
	activateRevision(b, oldB, approverB)

	draftA := a.createScope(f.ToVersionID, "A 2022")

	// B cannot carry into A's draft.
	if code, _, payload := b.carryOver(draftA); code != http.StatusNotFound {
		t.Fatalf("tenant B carry-over into A's draft: status %d, want 404; body %s", code, payload)
	}

	draftB := b.createScope(f.ToVersionID, "B 2022")
	res := b.mustCarryOver(draftB)
	if res.SourceScopeID == nil || *res.SourceScopeID != oldB {
		t.Fatalf("tenant B source scope = %v, want %s", res.SourceScopeID, oldB)
	}
	if len(res.Copied) != 1 {
		t.Fatalf("tenant B copied %d, want 1 (C-01 only)", len(res.Copied))
	}
	got := decisionsByCode(b, draftB)
	if d := got["C-01"]; d == nil || *d != (decisionFields{"applicable", "", "tenant-b"}) {
		t.Fatalf("tenant B C-01 = %+v, want B's own decision", d)
	}
	if got["C-02"] != nil {
		t.Fatalf("tenant B C-02 = %+v, want undecided (A's exclusion must not carry)", got["C-02"])
	}
	if reasonsByCode(res.Skipped)["C-02"] != "source_undecided" {
		t.Fatalf("tenant B skipped = %v", res.Skipped)
	}

	// A's draft is untouched by B's carry-over.
	for code, d := range decisionsByCode(a, draftA) {
		if d != nil {
			t.Fatalf("tenant A draft has %s = %+v after B's carry-over", code, d)
		}
	}

	// A tenant with no revision on N copies nothing.
	draftC := cc.createScope(f.ToVersionID, "C 2022")
	resC := cc.mustCarryOver(draftC)
	if len(resC.Copied) != 0 || resC.SourceScopeID != nil {
		t.Fatalf("tenant C copied %d with source %v, want 0 / nil", len(resC.Copied), resC.SourceScopeID)
	}

	// A's own carry-over takes A's decisions.
	resA := a.mustCarryOver(draftA)
	if len(resA.Copied) != 2 {
		t.Fatalf("tenant A copied %d, want 2", len(resA.Copied))
	}
	if d := decisionsByCode(a, draftA)["C-01"]; d == nil || d.Justification != "tenant A reason" {
		t.Fatalf("tenant A C-01 = %+v", d)
	}
}

// The carry-over sits behind the same write gate as the bulk set: a viewer
// gets 403 and nothing is written.
func TestHTTP_Applicability_CarryOverViewerForbidden(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedCarryFixture(t, admin)
	app := dbtest.NewAppPool(t)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	tid := uuid.MustParse(tenant)
	viewerClaims := testjwt.OwnerFor(tid, []string{"viewer"})
	viewerClaims.Subject = "test-viewer-1793:" + tenant
	engineerClaims := testjwt.OwnerFor(tid, []string{"grc_engineer"})
	engineerClaims.Subject = "test-engineer-1793:" + tenant
	engine, err := authz.NewEngine(context.Background(), subjectRoles{
		viewerClaims.Subject:   {authz.RoleViewer},
		engineerClaims.Subject: {authz.RoleGRCEngineer},
	})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv.AttachAuthz(engine, nil)
	viewer := srv.IssueTestJWT(t, viewerClaims)
	engineer := srv.IssueTestJWT(t, engineerClaims)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	eng := soaClient{t: t, base: ts.URL, bearer: engineer}
	oldRev := eng.createScope(f.FromVersionID, "ISMS 2017")
	eng.mustSet(oldRev, f.From["C-01"], applicableBody)
	draft := eng.createScope(f.ToVersionID, "ISMS 2022")

	v := soaClient{t: t, base: ts.URL, bearer: viewer}
	if code, _, payload := v.carryOver(draft); code != http.StatusForbidden {
		t.Fatalf("viewer carry-over: status %d, want 403; body %s", code, payload)
	}
	if n := len(carryOverRows(t, tenant, draft)); n != 0 {
		t.Fatalf("carry_over rows after viewer call = %d, want 0", n)
	}
	if res := eng.mustCarryOver(draft); len(res.Copied) != 1 {
		t.Fatalf("engineer carry-over copied %d, want 1", len(res.Copied))
	}
}
