package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// #2157: the auditor's evidence read is admitted only when the hydrated
// resource periods (input.resource.attrs.audit_period_ids) intersect the
// assigned periods, and never for a write. The HTTP path that sets those
// attributes is pinned by the #2157 integration test in internal/api.
func TestDecide2157_AuditorEvidenceByPeriod(t *testing.T) {
	t.Parallel()
	evidence := func(action, method, path string, roles []authz.Role, attrs map[string]any) authz.Input {
		in := auditorInput(action, "evidence", "")
		in.User.Roles = roles
		in.Request = authz.RequestInput{Method: method, Path: path}
		in.Resource.Attrs = attrs
		return in
	}
	auditor := []authz.Role{authz.RoleAuditor}
	viewer := []authz.Role{authz.RoleViewer}
	inA := map[string]any{"audit_period_ids": []any{periodB, periodA}}
	inB := map[string]any{"audit_period_ids": []any{periodB}}
	none := map[string]any{"audit_period_ids": []any{}}
	rec := "/v1/evidence/00000000-0000-0000-0000-000000000e01"

	cases := []struct {
		name string
		in   authz.Input
		want bool
	}{
		{"record in an assigned period", evidence("read", "GET", rec, auditor, inA), true},
		{"list of an assigned period's control", evidence("read", "GET", "/v1/evidence", auditor, inA), true},
		{"record only in an unassigned period", evidence("read", "GET", rec, auditor, inB), false},
		{"record in no period", evidence("read", "GET", rec, auditor, none), false},
		{"no hydrated attrs (list without control_id)", evidence("read", "GET", "/v1/evidence", auditor, nil), false},
		{"push with a matching period", evidence("write", "POST", "/v1/evidence:push", auditor, inA), false},
		{"push with a matching scope cell", withScopeCells(evidence("write", "POST", "/v1/evidence:push", auditor, map[string]any{"scope_cell_id": "c1"}), "c1"), false},
		{"viewer with a matching period", evidence("read", "GET", rec, viewer, inA), false},
	}
	for _, tc := range cases {
		if d := decide(t, tc.in); d.Allow != tc.want {
			t.Errorf("%s: allow=%v, want %v (%s)", tc.name, d.Allow, tc.want, d.Reason)
		}
	}
}

// withScopeCells gives the auditor input.user.attrs.scope_cell_ids, which
// nothing sets in production yet (scope_cells.rego).
func withScopeCells(in authz.Input, cells ...any) authz.Input {
	in.User.Attrs["scope_cell_ids"] = cells
	return in
}

// #2157: samples and populations, keyed on the single hydrated
// audit_period_id. The auditor reads both and writes samples (draw and
// annotate) in an assigned period only; it never creates a population.
func TestDecide2157_AuditorSamplesAndPopulationsByPeriod(t *testing.T) {
	t.Parallel()
	cases := []struct {
		action, resource, period string
		want                     bool
	}{
		{"read", "samples", periodA, true},
		{"write", "samples", periodA, true},
		{"read", "populations", periodA, true},
		{"read", "samples", periodB, false},
		{"write", "samples", periodB, false},
		{"read", "populations", periodB, false},
		{"read", "samples", "", false},
		{"read", "populations", "", false},
		{"write", "populations", periodA, false},
	}
	for _, tc := range cases {
		if d := decide(t, auditorInput(tc.action, tc.resource, tc.period)); d.Allow != tc.want {
			t.Errorf("auditor %s %s period=%q: allow=%v, want %v", tc.action, tc.resource, tc.period, d.Allow, tc.want)
		}
	}
}
