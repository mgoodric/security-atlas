//go:build integration

// Integration tests for the applicability diff on the review screen
// (issue #1795): the diff between a revision in review and the activated
// revision lists exactly the changed decisions, plus the predicate change,
// and stays inside the tenant.

package frameworkscope_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

type diffResp struct {
	FrameworkScope struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"framework_scope"`
	Baseline *struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"baseline"`
	Predicate struct {
		Changed bool            `json:"changed"`
		Before  json.RawMessage `json:"before"`
		After   json.RawMessage `json:"after"`
	} `json:"predicate"`
	Changes []struct {
		Code   string `json:"code"`
		Change string `json:"change"`
		Before *struct {
			Decision      string `json:"decision"`
			Justification string `json:"justification"`
		} `json:"before"`
		After *struct {
			Decision      string `json:"decision"`
			Justification string `json:"justification"`
		} `json:"after"`
	} `json:"changes"`
}

func (c soaClient) diff(scopeID string) (int, diffResp, []byte) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodGet, c.base+"/v1/framework-scopes/"+scopeID+"/applicability/diff", c.bearer, "")
	var out diffResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(payload, &out); err != nil {
			c.t.Fatalf("unmarshal diff: %v", err)
		}
	}
	return resp.StatusCode, out, payload
}

func TestHTTP_ApplicabilityDiff_ListsExactlyTheChanges(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}

	// Revision A, activated: the fixture decisions (R-03 undecided).
	a := c.createScope(f.FrameworkVersionID, "ISMS rev A")
	applyFixtureDecisions(c, f, a)

	// With nothing activated yet there is no baseline: every decision is added.
	c.transition(a, "submit", bearer, "")
	code, first, payload := c.diff(a)
	if code != http.StatusOK {
		t.Fatalf("diff A: status %d; body %s", code, payload)
	}
	if first.Baseline != nil || len(first.Changes) != 7 || first.Changes[0].Change != "added" {
		t.Fatalf("diff with no baseline: baseline %+v, %d changes", first.Baseline, len(first.Changes))
	}
	c.transition(a, "approve", approver, "")
	c.transition(a, "activate", bearer, fmt.Sprintf(`{"effective_from":%q}`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)))

	// Revision B: the same decisions, then one of each change kind, an
	// implementation_ref-only edit that is not a change, and a new predicate.
	b := c.createScope(f.FrameworkVersionID, "ISMS rev B")
	applyFixtureDecisions(c, f, b)
	c.mustSet(b, f.Req["R-03"], applicableBody)                                                    // added
	c.mustSet(b, f.Req["R-01"], applicableBody)                                                    // decision_changed
	c.mustSet(b, f.Req["R-02"], `{"decision":"excluded","justification":"No card data in scope"}`) // justification_changed
	c.mustSet(b, f.Req["R-04"], `{"decision":"applicable","implementation_ref":"https://wiki.example/isms/B"}`)
	if resp, p := doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+b, bearer,
		`{"predicate":{"op":"eq","dim":"env","value":"prod"}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("patch predicate: status %d; body %s", resp.StatusCode, p)
	}
	// R-08 removed: no API deletes a decision, so drop the row directly
	// while B is a draft.
	if _, err := admin.Exec(t.Context(), `DELETE FROM framework_scope_applicability
		WHERE framework_scope_id = $1 AND framework_requirement_id = $2`, b, f.Req["R-08"]); err != nil {
		t.Fatalf("remove R-08: %v", err)
	}
	c.transition(b, "submit", bearer, "")

	code, d, payload := c.diff(b)
	if code != http.StatusOK {
		t.Fatalf("diff B: status %d; body %s", code, payload)
	}
	if d.FrameworkScope.ID != b || d.FrameworkScope.State != "review" {
		t.Fatalf("diff scope = %+v", d.FrameworkScope)
	}
	if d.Baseline == nil || d.Baseline.ID != a || d.Baseline.State != "activated" {
		t.Fatalf("baseline = %+v, want activated A", d.Baseline)
	}
	if !d.Predicate.Changed || string(d.Predicate.Before) != `{"op":"true"}` {
		t.Fatalf("predicate diff = changed %v before %s after %s", d.Predicate.Changed, d.Predicate.Before, d.Predicate.After)
	}

	want := map[string]string{
		"R-01": "decision_changed",
		"R-02": "justification_changed",
		"R-03": "added",
		"R-08": "removed",
	}
	got := map[string]string{}
	for _, ch := range d.Changes {
		got[ch.Code] = ch.Change
	}
	if len(got) != len(want) || len(d.Changes) != len(want) {
		t.Fatalf("changes = %v, want exactly %v", got, want)
	}
	for code, kind := range want {
		if got[code] != kind {
			t.Errorf("%s: change %q, want %q", code, got[code], kind)
		}
	}
	for _, ch := range d.Changes {
		switch ch.Code {
		case "R-01":
			if ch.Before.Decision != "excluded" || ch.After.Decision != "applicable" {
				t.Errorf("R-01 sides = %+v -> %+v", ch.Before, ch.After)
			}
		case "R-02":
			if ch.Before.Justification != "No cardholder data processed" || ch.After.Justification != "No card data in scope" {
				t.Errorf("R-02 sides = %+v -> %+v", ch.Before, ch.After)
			}
		case "R-03":
			if ch.Before != nil || ch.After == nil {
				t.Errorf("R-03 sides = %+v -> %+v", ch.Before, ch.After)
			}
		case "R-08":
			if ch.Before == nil || ch.After != nil {
				t.Errorf("R-08 sides = %+v -> %+v", ch.Before, ch.After)
			}
		}
	}

	// Approving B and activating it makes it the baseline-less current
	// revision: its own diff has no baseline.
	c.transition(b, "approve", approver, "")
	c.transition(b, "activate", bearer, fmt.Sprintf(`{"effective_from":%q}`, time.Now().UTC().Format(time.RFC3339)))
	if _, after, _ := c.diff(b); after.Baseline != nil || after.FrameworkScope.State != "activated" {
		t.Fatalf("diff of activated B: baseline %+v state %s", after.Baseline, after.FrameworkScope.State)
	}
}

func TestHTTP_ApplicabilityDiff_RLSIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenantA := soaTenant(t, admin)
	tenantB := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenantA)
	tsA, bearerA, _ := setupHTTPServer(t, tenantA)
	tsB, bearerB, _ := setupHTTPServer(t, tenantB)
	a := soaClient{t: t, base: tsA.URL, bearer: bearerA}
	b := soaClient{t: t, base: tsB.URL, bearer: bearerB}

	scopeID := a.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(a, f, scopeID)
	if code, _, _ := b.diff(scopeID); code != http.StatusNotFound {
		t.Fatalf("tenant B diff of A's scope: status %d, want 404", code)
	}
	if code, _, payload := a.diff(scopeID); code != http.StatusOK {
		t.Fatalf("tenant A diff: status %d; body %s", code, payload)
	}
}
