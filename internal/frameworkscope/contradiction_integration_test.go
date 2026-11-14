//go:build integration

// Integration tests for SoA contradiction flags (issue #1789, spec #1744): an
// excluded requirement with an active tenant control behind it whose latest
// evaluation passes is flagged on the decision list, the flag follows the
// latest evaluation, and the list filters and counts on it.

package frameworkscope_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func contradicted(list applicabilityResp) []string {
	var out []string
	for _, r := range list.Requirements {
		if r.Contradiction {
			out = append(out, r.Code)
		}
	}
	return out
}

func codesOf(list applicabilityResp) string {
	codes := make([]string, len(list.Requirements))
	for i, r := range list.Requirements {
		codes[i] = r.Code
	}
	return strings.Join(codes, ",")
}

// R-01 is excluded with passing C1 behind it and is flagged. R-02 is
// excluded with no control behind it and is not. Applicable requirements
// are never flagged, even R-06, which passing C1 also reaches.
func TestHTTP_Applicability_ContradictionFlagged(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)

	list := c.mustList(scopeID)
	if got := strings.Join(contradicted(list), ","); got != "R-01" {
		t.Fatalf("contradictions = %q, want R-01", got)
	}
	if list.Counts.Contradictions != 1 {
		t.Fatalf("counts.contradictions = %d, want 1", list.Counts.Contradictions)
	}
	for _, r := range list.Requirements {
		if r.Contradiction && (r.Decision == nil || *r.Decision != "excluded") {
			t.Fatalf("%s flagged but not excluded: %+v", r.Code, r)
		}
	}

	// Excluding R-06 too (C1 passing behind it) makes it a second one.
	c.mustSet(scopeID, f.Req["R-06"], excludedBody)
	if got := strings.Join(contradicted(c.mustList(scopeID)), ","); got != "R-01,R-06" {
		t.Fatalf("after excluding R-06: contradictions = %q, want R-01,R-06", got)
	}

	// Excluding R-05, whose only control (C3) fails, is not a contradiction.
	c.mustSet(scopeID, f.Req["R-05"], excludedBody)
	if got := strings.Join(contradicted(c.mustList(scopeID)), ","); got != "R-01,R-06" {
		t.Fatalf("after excluding R-05: contradictions = %q, want R-01,R-06", got)
	}
}

// When the passing control's latest evaluation turns failing, the flag
// clears; when it passes again, the flag returns.
func TestHTTP_Applicability_ContradictionFollowsLatestEvaluation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)

	if list := c.mustList(scopeID); len(contradicted(list)) != 1 {
		t.Fatalf("before: contradictions = %v, want [R-01]", contradicted(list))
	}

	evaluate := func(result, offset string) {
		t.Helper()
		if _, err := admin.Exec(context.Background(), `INSERT INTO control_evaluations (
				id, tenant_id, control_id, scope_cell_id, eval_run_id,
				evaluated_at, result, freshness_status, evidence_count_in_window, trigger
			) VALUES ($1, $2, $3, NULL, $4, now() + $6::interval, $5, 'fresh', 1, 'manual')`,
			uuid.New(), tenant, f.Controls["C1"], uuid.New(), result, offset); err != nil {
			t.Fatalf("insert %s evaluation: %v", result, err)
		}
	}

	evaluate("fail", "1 minute")
	list := c.mustList(scopeID)
	if got := contradicted(list); len(got) != 0 || list.Counts.Contradictions != 0 {
		t.Fatalf("after C1 fails: contradictions = %v (count %d), want none", got, list.Counts.Contradictions)
	}
	for _, r := range list.Requirements {
		if r.Code == "R-01" && (r.Decision == nil || *r.Decision != "excluded") {
			t.Fatalf("R-01 should still be excluded: %+v", r)
		}
	}

	evaluate("pass", "2 minutes")
	if got := strings.Join(contradicted(c.mustList(scopeID)), ","); got != "R-01" {
		t.Fatalf("after C1 passes again: contradictions = %q, want R-01", got)
	}
}

// ?contradiction= and ?decision= filter the list; counts stay those of the
// whole revision.
func TestHTTP_Applicability_ContradictionFilterAndCount(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)

	base := "/v1/framework-scopes/" + scopeID + "/applicability"
	for _, tc := range []struct {
		query, want string
	}{
		{"?contradiction=true", "R-01"},
		{"?contradiction=false", "R-02,R-03,R-04,R-05,R-06,R-07,R-08"},
		{"?decision=excluded", "R-01,R-02"},
		{"?decision=excluded&contradiction=false", "R-02"},
		{"?decision=applicable&contradiction=true", ""},
		{"?decision=undecided", "R-03"},
		{"?decision=applicable", "R-04,R-05,R-06,R-07,R-08"},
	} {
		code, list, payload := c.get(base + tc.query)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d; body %s", tc.query, code, payload)
		}
		if got := codesOf(list); got != tc.want {
			t.Fatalf("%s: requirements = %q, want %q", tc.query, got, tc.want)
		}
		want := struct{ Total, Applicable, Excluded, Undecided, Contradictions int }{8, 5, 2, 1, 1}
		got := struct{ Total, Applicable, Excluded, Undecided, Contradictions int }{
			list.Counts.Total, list.Counts.Applicable, list.Counts.Excluded, list.Counts.Undecided, list.Counts.Contradictions,
		}
		if got != want {
			t.Fatalf("%s: counts = %+v, want %+v", tc.query, got, want)
		}
	}

	for _, q := range []string{"?decision=maybe", "?contradiction=sometimes"} {
		if code, _, payload := c.get(base + q); code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400; body %s", q, code, payload)
		}
	}
}
