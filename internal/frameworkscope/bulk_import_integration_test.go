//go:build integration

// Integration tests for bulk-set and CSV import of applicability decisions
// (issue #1790, spec #1744), over the HTTP API against the shared SoA
// fixture (seedSoAFixture).

package frameworkscope_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

type importResp struct {
	Applied  int `json:"applied"`
	Rejected []struct {
		Line            int    `json:"line"`
		RequirementCode string `json:"requirement_code"`
		Reason          string `json:"reason"`
	} `json:"rejected"`
	FrameworkScope struct {
		State string `json:"state"`
	} `json:"framework_scope"`
	ApprovalInvalidated bool `json:"approval_invalidated"`
}

func (c soaClient) importCSV(scopeID, csv string) (int, importResp, []byte) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodPost,
		c.base+"/v1/framework-scopes/"+scopeID+"/applicability/import", c.bearer, csv)
	var out importResp
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnprocessableEntity {
		if err := json.Unmarshal(payload, &out); err != nil {
			c.t.Fatalf("unmarshal import: %v; body %s", err, payload)
		}
	}
	return resp.StatusCode, out, payload
}

func applicabilityUpdates(t *testing.T, tenant, scopeID string) int {
	t.Helper()
	n := 0
	for _, r := range readWorkflowLog(t, tenant, scopeID) {
		if r.Action == "applicability.update" {
			n++
		}
	}
	return n
}

// A bulk exclude of three requirements with one justification sets all
// three and writes a history entry for each.
func TestHTTP_Applicability_BulkExclude(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)
	before := applicabilityUpdates(t, tenant, scopeID)

	body := fmt.Sprintf(`{"requirement_ids":[%q,%q,%q],"decision":"excluded","justification":"Outsourced to the cloud provider"}`,
		f.Req["R-03"], f.Req["R-07"], f.Req["R-08"])
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/framework-scopes/"+scopeID+"/applicability/bulk", bearer, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk exclude: status %d; body %s", resp.StatusCode, payload)
	}
	var out struct {
		Decisions []struct {
			Decision string `json:"decision"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal bulk: %v", err)
	}
	if len(out.Decisions) != 3 {
		t.Fatalf("bulk returned %d decisions, want 3", len(out.Decisions))
	}

	list := c.mustList(scopeID)
	for _, r := range list.Requirements {
		switch r.Code {
		case "R-03", "R-07", "R-08":
			if r.Decision == nil || *r.Decision != "excluded" || r.Justification != "Outsourced to the cloud provider" {
				t.Errorf("%s = %v %q, want excluded with the bulk justification", r.Code, r.Decision, r.Justification)
			}
		}
	}
	if list.Counts.Excluded != 5 || list.Counts.Undecided != 0 {
		t.Fatalf("counts after bulk = %+v, want excluded 5, undecided 0", list.Counts)
	}
	if got := applicabilityUpdates(t, tenant, scopeID) - before; got != 3 {
		t.Fatalf("bulk wrote %d history entries, want 3", got)
	}

	// One bad input writes nothing: an exclusion without a justification,
	// and a requirement from no version of this scope.
	for _, bad := range []struct {
		body string
		want int
	}{
		{fmt.Sprintf(`{"requirement_ids":[%q],"decision":"excluded"}`, f.Req["R-04"]), http.StatusBadRequest},
		{fmt.Sprintf(`{"requirement_ids":[%q,"%s"],"decision":"applicable"}`, f.Req["R-01"], "00000000-0000-0000-0000-000000000001"), http.StatusNotFound},
	} {
		resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/framework-scopes/"+scopeID+"/applicability/bulk", bearer, bad.body)
		if resp.StatusCode != bad.want {
			t.Fatalf("bad bulk %s: status %d, want %d; body %s", bad.body, resp.StatusCode, bad.want, payload)
		}
	}
	if after := c.mustList(scopeID); after.Counts != list.Counts {
		t.Fatalf("counts after refused bulks = %+v, want unchanged %+v", after.Counts, list.Counts)
	}
}

// A CSV import applies the valid rows, reports each bad row with its
// reason, and sends an approved revision back to draft.
func TestHTTP_Applicability_ImportCSV(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)
	c.transition(scopeID, "submit", bearer, "")
	c.transition(scopeID, "approve", approver, "")
	before := applicabilityUpdates(t, tenant, scopeID)

	csv := strings.Join([]string{
		"requirement_code,decision,justification,implementation_ref",
		"R-03,excluded,No physical premises,",
		"R-99,applicable,,",
		"R-04,excluded,,",
		"R-05,maybe,,",
		"R-07,Applicable,,https://wiki.example/isms/R-07",
	}, "\n") + "\n"
	code, out, payload := c.importCSV(scopeID, csv)
	if code != http.StatusOK {
		t.Fatalf("import: status %d; body %s", code, payload)
	}
	if out.Applied != 2 {
		t.Fatalf("applied = %d, want 2; body %s", out.Applied, payload)
	}
	wantRejected := []struct {
		line         int
		code, reason string
	}{
		{3, "R-99", "unknown requirement code"},
		{4, "R-04", "a justification is required to exclude a requirement"},
		{5, "R-05", "decision must be `applicable` or `excluded`"},
	}
	if len(out.Rejected) != len(wantRejected) {
		t.Fatalf("rejected = %+v, want %+v", out.Rejected, wantRejected)
	}
	for i, w := range wantRejected {
		got := out.Rejected[i]
		if got.Line != w.line || got.RequirementCode != w.code || got.Reason != w.reason {
			t.Errorf("rejection %d = %+v, want %+v", i, got, w)
		}
	}
	if out.FrameworkScope.State != "draft" || !out.ApprovalInvalidated {
		t.Fatalf("after import: state %s invalidated %v, want draft/true", out.FrameworkScope.State, out.ApprovalInvalidated)
	}

	list := c.mustList(scopeID)
	for _, r := range list.Requirements {
		switch r.Code {
		case "R-03":
			if r.Decision == nil || *r.Decision != "excluded" || r.Justification != "No physical premises" {
				t.Errorf("R-03 = %v %q, want excluded", r.Decision, r.Justification)
			}
		case "R-07":
			if r.Decision == nil || *r.Decision != "applicable" || r.ImplementationRef != "https://wiki.example/isms/R-07" {
				t.Errorf("R-07 = %v %q, want applicable with the imported reference", r.Decision, r.ImplementationRef)
			}
		case "R-04", "R-05":
			if r.Decision == nil || *r.Decision != "applicable" {
				t.Errorf("%s = %v, want unchanged applicable", r.Code, r.Decision)
			}
		}
	}
	if got := applicabilityUpdates(t, tenant, scopeID) - before; got != 2 {
		t.Fatalf("import wrote %d history entries, want 2", got)
	}
}

// An activated revision's decisions are frozen: bulk and import both
// return 409 and write no history.
func TestHTTP_Applicability_BulkImportFrozen(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)
	c.transition(scopeID, "submit", bearer, "")
	c.transition(scopeID, "approve", approver, "")
	c.transition(scopeID, "activate", bearer, fmt.Sprintf(`{"effective_from":%q}`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)))
	listBefore := c.mustList(scopeID)
	logBefore := applicabilityUpdates(t, tenant, scopeID)

	body := fmt.Sprintf(`{"requirement_ids":[%q],"decision":"applicable"}`, f.Req["R-03"])
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/framework-scopes/"+scopeID+"/applicability/bulk", bearer, body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("bulk on activated revision: status %d, want 409; body %s", resp.StatusCode, payload)
	}
	if code, _, payload := c.importCSV(scopeID, "requirement_code,decision\nR-03,applicable\n"); code != http.StatusConflict {
		t.Fatalf("import on activated revision: status %d, want 409; body %s", code, payload)
	}

	if after := c.mustList(scopeID); after.Counts != listBefore.Counts || after.FrameworkScope.State != "activated" {
		t.Fatalf("after refused writes: state %s counts %+v, want activated %+v", after.FrameworkScope.State, after.Counts, listBefore.Counts)
	}
	if got := applicabilityUpdates(t, tenant, scopeID); got != logBefore {
		t.Fatalf("history entries %d -> %d, want unchanged", logBefore, got)
	}
}

// Bulk case-folds its decision, as the import does, and refuses a body over
// 1 MiB with 413.
func TestHTTP_Applicability_BulkDecisionCaseAndBodyCap(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	url := ts.URL + "/v1/framework-scopes/" + scopeID + "/applicability/bulk"

	body := fmt.Sprintf(`{"requirement_ids":[%q],"decision":" Excluded ","justification":"No premises"}`, f.Req["R-03"])
	if resp, payload := doJSON(t, http.MethodPost, url, bearer, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk with `Excluded`: status %d, want 200; body %s", resp.StatusCode, payload)
	}
	for _, r := range c.mustList(scopeID).Requirements {
		if r.Code == "R-03" && (r.Decision == nil || *r.Decision != "excluded") {
			t.Fatalf("R-03 = %v, want excluded", r.Decision)
		}
	}

	big := fmt.Sprintf(`{"requirement_ids":[%q],"decision":"excluded","justification":%q}`,
		f.Req["R-04"], strings.Repeat("x", 1<<20))
	if resp, payload := doJSON(t, http.MethodPost, url, bearer, big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized bulk: status %d, want 413; body %.200s", resp.StatusCode, payload)
	}
}

// An import whose every row is bad changes nothing.
func TestHTTP_Applicability_ImportOnlyBadRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)
	c.transition(scopeID, "submit", bearer, "")
	c.transition(scopeID, "approve", approver, "")
	listBefore := c.mustList(scopeID)
	logBefore := applicabilityUpdates(t, tenant, scopeID)

	csv := "requirement_code,decision,justification\nR-99,applicable,\nR-03,excluded,\nR-04,never,\n"
	code, out, payload := c.importCSV(scopeID, csv)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("all-bad import: status %d, want 422; body %s", code, payload)
	}
	if out.Applied != 0 || len(out.Rejected) != 3 {
		t.Fatalf("all-bad import: applied %d rejected %d, want 0/3", out.Applied, len(out.Rejected))
	}
	if out.FrameworkScope.State != "approved" || out.ApprovalInvalidated {
		t.Fatalf("all-bad import: state %s invalidated %v, want approved/false", out.FrameworkScope.State, out.ApprovalInvalidated)
	}

	listAfter := c.mustList(scopeID)
	if listAfter.FrameworkScope.State != "approved" {
		t.Fatalf("scope state after all-bad import = %s, want approved", listAfter.FrameworkScope.State)
	}
	if listAfter.Counts != listBefore.Counts {
		t.Fatalf("counts changed: %+v -> %+v", listBefore.Counts, listAfter.Counts)
	}
	for i := range listBefore.Requirements {
		b, a := listBefore.Requirements[i], listAfter.Requirements[i]
		dec := func(p *string) string {
			if p == nil {
				return "undecided"
			}
			return *p
		}
		if dec(b.Decision) != dec(a.Decision) || b.Justification != a.Justification {
			t.Errorf("%s changed: %s %q -> %s %q", b.Code, dec(b.Decision), b.Justification, dec(a.Decision), a.Justification)
		}
	}
	if got := applicabilityUpdates(t, tenant, scopeID); got != logBefore {
		t.Fatalf("history entries %d -> %d, want unchanged", logBefore, got)
	}
}
