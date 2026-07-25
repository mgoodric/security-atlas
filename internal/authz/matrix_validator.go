// Slice 378: production-code matrix validator. The slice-026 authz
// matrix is the load-bearing gate that the bundle hot-reload path runs
// against a CANDIDATE prepared query BEFORE the atomic swap. If the
// matrix fails on the candidate, the reload is rejected and the
// engine keeps serving the prior bundle. Without this gate a
// permissive bundle could reach the request-time authz path (slice
// 378 P0-2).
//
// The matrix table lives here as production code (not under a build
// tag) so two surfaces can read the same source of truth: the
// integration test in `matrix_integration_test.go` (kept in lockstep
// via a TestMatrixCanonicalSubset check) and the runtime reload
// validator wired by `internal/api/adminauthzbundle`.

package authz

import (
	"context"
	"fmt"

	"github.com/open-policy-agent/opa/v1/rego"
)

// MatrixCase is one row in the role × endpoint × expected-outcome
// table. Tests + the production reload validator share this shape.
type MatrixCase struct {
	Role        Role
	Action      string
	Resource    string
	ExpectAllow bool
	Notes       string
}

// CanonicalMatrix is the slice-026 authz matrix in production-code
// form. The integration test in matrix_integration_test.go SHOULD
// include every case here; new policy invariants land here first
// (so the reload-validator sees them) and the test adds the matching
// assertion in the same PR. Tests + reload share one source of truth.
//
// Each row exercises one (role, action, resource) cell that the
// bundle MUST honour. A reload that breaks ANY cell is rejected.
func CanonicalMatrix() []MatrixCase {
	return []MatrixCase{
		// admin: full tenant write surface
		{RoleAdmin, "write", "risks", true, "admin: write any tenant-scoped resource"},
		{RoleAdmin, "write", "controls", true, "admin: write controls"},
		{RoleAdmin, "approve", "policies", true, "admin: governance approve"},
		{RoleAdmin, "publish", "policies", true, "admin: publish policy"},
		{RoleAdmin, "read", "evidence", true, "admin: read evidence"},

		// grc_engineer: GRC operator surface
		{RoleGRCEngineer, "write", "risks", true, "grc_engineer: write risks"},
		{RoleGRCEngineer, "write", "policies", true, "grc_engineer: write policies"},
		{RoleGRCEngineer, "approve", "framework-scopes", true, "grc_engineer: approve scopes"},
		{RoleGRCEngineer, "publish", "policies", true, "grc_engineer: publish policy"},
		{RoleGRCEngineer, "write", "evidence", true, "grc_engineer: write evidence (push)"},
		{RoleGRCEngineer, "read", "samples", true, "grc_engineer: read samples"},

		// control_owner: attests + reads only
		{RoleControlOwner, "write", "evidence", true, "control_owner: submit attestation evidence"},
		{RoleControlOwner, "read", "controls", true, "control_owner: read controls"},
		{RoleControlOwner, "write", "risks", false, "control_owner: NOT allowed to write risks"},
		{RoleControlOwner, "approve", "policies", false, "control_owner: NOT allowed to approve policies"},
		{RoleControlOwner, "publish", "policies", false, "control_owner: NOT allowed to publish"},

		// auditor: read-only with period gate
		{RoleAuditor, "read", "controls", true, "auditor: read controls"},
		{RoleAuditor, "read", "policies", true, "auditor: read policies"},
		{RoleAuditor, "write", "evidence", false, "auditor: NOT allowed to push evidence"},
		{RoleAuditor, "write", "risks", false, "auditor: NOT allowed to write risks"},
		{RoleAuditor, "read", "framework-scopes", true, "auditor: read scopes"},

		// viewer: read-only catalog + dashboard
		{RoleViewer, "read", "controls", true, "viewer: read controls"},
		{RoleViewer, "read", "policies", true, "viewer: read policies"},
		{RoleViewer, "write", "risks", false, "viewer: NOT allowed to write risks"},
		{RoleViewer, "write", "controls", false, "viewer: NOT allowed to write controls"},
		{RoleViewer, "approve", "policies", false, "viewer: NOT allowed to approve"},

		// slice 269 — dashboard snapshot export admit set
		{RoleAdmin, "read", "dashboard", true, "slice 269: admin admitted to dashboard export"},
		{RoleGRCEngineer, "read", "dashboard", true, "slice 269: grc_engineer admitted to dashboard export"},
		{RoleAuditor, "read", "dashboard", true, "slice 269: auditor admitted to dashboard export"},
		{RoleControlOwner, "read", "dashboard", false, "slice 269: control_owner NOT admitted"},
		{RoleViewer, "read", "dashboard", false, "slice 269: viewer NOT admitted"},

		// slice 468 — per-user saved filter-views are a self-service
		// surface: EVERY authenticated role may read + write their OWN
		// views (per-user isolation is at the query layer, not rego).
		// Note viewer WRITE is admitted here (unlike controls write) —
		// this is deliberate: a viewer owns their own saved views.
		{RoleViewer, "write", "saved-views", true, "slice 468: viewer manages own saved views"},
		{RoleAuditor, "write", "saved-views", true, "slice 468: auditor manages own saved views"},
		{RoleControlOwner, "read", "saved-views", true, "slice 468: control_owner reads own saved views"},
		{RoleAdmin, "write", "saved-views", true, "slice 468: admin manages own saved views"},
	}
}

// ValidateMatrix evaluates every CanonicalMatrix cell against the
// supplied candidate query. Returns nil when every cell produces the
// expected allow / deny outcome; returns an error citing the first
// failing case otherwise. The error wraps a stable
// "matrix case failed:" prefix so callers can match for log filtering.
//
// The candidate is evaluated via Eval with the same JSON-input shape
// that Decide constructs from authz.Input. A nil candidate returns an
// error (defence-in-depth — the reload code path always supplies a
// real candidate, but a nil check avoids a panic on a bad caller).
func ValidateMatrix(ctx context.Context, candidate *rego.PreparedEvalQuery) error {
	if candidate == nil {
		return fmt.Errorf("ValidateMatrix: candidate query is nil")
	}
	for _, mc := range CanonicalMatrix() {
		in := Input{
			User: UserInput{
				ID:    "matrix-validator-" + string(mc.Role),
				Roles: []Role{mc.Role},
			},
			TenantID: "00000000-0000-0000-0000-000000000099",
			Action:   mc.Action,
			Resource: ResourceInput{Type: mc.Resource},
			Request:  RequestInput{Method: "POST", Path: "/v1/" + mc.Resource},
		}
		results, err := candidate.Eval(ctx, rego.EvalInput(toRegoInput(in)))
		if err != nil {
			return fmt.Errorf("matrix case failed (%s): eval error: %w", mc.Notes, err)
		}
		var allow bool
		if len(results) > 0 {
			if v, ok := results[0].Expressions[0].Value.(bool); ok {
				allow = v
			}
		}
		if allow != mc.ExpectAllow {
			return fmt.Errorf("matrix case failed (%s): role=%s action=%s resource=%s expected_allow=%v got_allow=%v",
				mc.Notes, mc.Role, mc.Action, mc.Resource, mc.ExpectAllow, allow)
		}
	}
	return nil
}
