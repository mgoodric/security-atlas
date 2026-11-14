package calendar

// #2130 — the risk and action_plan event types are shown only to a caller
// OPA lets read risks / action plans. These run the real rego bundle with
// a fixed-role resolver, the search handler's per-type admit pattern.

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

type fixedRoles []authz.Role

func (f fixedRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return f, nil
}

// requestAs is a GET /v1/calendar from a human user holding role, the
// role arriving through the resolver as it does from user_roles.
func requestAs(t *testing.T, role authz.Role) func(string) (string, bool) {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), fixedRoles{role})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cred := credstore.Credential{
		ID:         "c",
		TenantID:   "11111111-1111-1111-1111-111111111111",
		UserID:     "22222222-2222-2222-2222-222222222222",
		OwnerRoles: []string{"security_lead"},
	}
	req := httptest.NewRequest("GET", "/v1/calendar", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	h := New(nil, nil).WithAuthz(engine)
	return func(filter string) (string, bool) { return h.readableTypes(req, filter) }
}

func TestReadableTypes_PerRole(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role         authz.Role
		risk, action bool
	}{
		{authz.RoleAdmin, true, true},
		{authz.RoleGRCEngineer, true, true},
		{authz.RoleViewer, true, false},
		{authz.RoleAuditor, false, false},
		{authz.RoleControlOwner, false, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			t.Parallel()
			readable := requestAs(t, tc.role)
			got, ok := readable("")
			if !ok {
				t.Fatalf("no type left for %s", tc.role)
			}
			if got == "" { // nothing dropped: the empty filter is every type
				got = validEventTypes
			}
			set := map[string]bool{}
			for _, ty := range splitCSV(got) {
				set[ty] = true
			}
			if set["risk"] != tc.risk || set["action_plan"] != tc.action {
				t.Errorf("%s: risk=%v action_plan=%v, want risk=%v action_plan=%v (filter %q)",
					tc.role, set["risk"], set["action_plan"], tc.risk, tc.action, got)
			}
			for _, always := range []string{"audit", "exception", "policy", "vendor", "control", "policy_ack"} {
				if !set[always] {
					t.Errorf("%s: %s dropped from %q", tc.role, always, got)
				}
			}
		})
	}
}

func TestReadableTypes_OnlyGatedRequestedAndDenied(t *testing.T) {
	t.Parallel()
	readable := requestAs(t, authz.RoleAuditor)
	if got, ok := readable("risk,action_plan"); ok || got != "" {
		t.Errorf("auditor asking only for risk,action_plan: got %q ok=%v, want nothing", got, ok)
	}
	if got, ok := readable("risk,exception"); !ok || got != "exception" {
		t.Errorf("auditor asking for risk,exception: got %q ok=%v, want exception", got, ok)
	}
}

func TestWithoutTypes(t *testing.T) {
	t.Parallel()
	if got, ok := withoutTypes("audit,risk", nil); !ok || got != "audit,risk" {
		t.Errorf("no drop: %q %v", got, ok)
	}
	if got, ok := withoutTypes("", gatedEventTypes); !ok || got != "audit,exception,policy,vendor,control,policy_ack" {
		t.Errorf("all minus gated: %q %v", got, ok)
	}
	if _, ok := withoutTypes("risk", gatedEventTypes); ok {
		t.Error("only a gated type requested: want nothing left")
	}
}

func splitCSV(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
