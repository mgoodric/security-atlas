package calendar

// #2130 — the risk and action_plan event types are shown only to a caller
// OPA lets read risks / action plans. These run the real rego bundle with
// a fixed-role resolver, the search handler's per-type admit pattern.

import (
	"context"
	"errors"
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
	// #2232: vendor and exception follow the vendors and exceptions list
	// endpoints' OPA reads: the viewer reads vendors, the auditor reads
	// exceptions, the control owner reads neither.
	cases := []struct {
		role                      authz.Role
		risk, action, offboarding bool
		vendor, exception         bool
	}{
		{authz.RoleAdmin, true, true, true, true, true},
		{authz.RoleGRCEngineer, true, true, true, true, true},
		{authz.RoleViewer, true, false, false, true, false},
		{authz.RoleAuditor, false, false, false, false, true},
		{authz.RoleControlOwner, false, false, false, false, false},
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
			// #2215: offboarding events need a personnel-security read.
			if set["offboarding"] != tc.offboarding {
				t.Errorf("%s: offboarding=%v, want %v (filter %q)", tc.role, set["offboarding"], tc.offboarding, got)
			}
			if set["vendor"] != tc.vendor || set["exception"] != tc.exception {
				t.Errorf("%s: vendor=%v exception=%v, want vendor=%v exception=%v (filter %q)",
					tc.role, set["vendor"], set["exception"], tc.vendor, tc.exception, got)
			}
			for _, always := range []string{"audit", "policy", "control", "policy_ack"} {
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
	if got, ok := readable("offboarding"); ok || got != "" {
		t.Errorf("auditor asking only for offboarding: got %q ok=%v, want nothing", got, ok)
	}
	if got, ok := readable("vendor"); ok || got != "" {
		t.Errorf("auditor asking only for vendor: got %q ok=%v, want nothing", got, ok)
	}
	owner := requestAs(t, authz.RoleControlOwner)
	if got, ok := owner("vendor,exception,control"); !ok || got != "control" {
		t.Errorf("control owner asking for vendor,exception,control: got %q ok=%v, want control", got, ok)
	}
}

func TestWithoutTypes(t *testing.T) {
	t.Parallel()
	if got, ok := withoutTypes("audit,risk", nil); !ok || got != "audit,risk" {
		t.Errorf("no drop: %q %v", got, ok)
	}
	if got, ok := withoutTypes("", gatedEventTypes); !ok || got != "audit,policy,control,policy_ack" {
		t.Errorf("all minus gated: %q %v", got, ok)
	}
	if _, ok := withoutTypes("risk", gatedEventTypes); ok {
		t.Error("only a gated type requested: want nothing left")
	}
}

type failingRoles struct{}

func (failingRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return nil, errors.New("user_roles unreachable")
}

func mustEngine(t *testing.T, r authz.RolesResolver) *authz.Engine {
	t.Helper()
	e, err := authz.NewEngine(context.Background(), r)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// #2331: the ICS feed decides gated types as its subscriber. Each role
// gets what OPA gives it; no engine, no subscriber, no live role or a
// failed role lookup leaves every gated type out (the error is returned
// for the caller to log).
func TestReadableTypesFor(t *testing.T) {
	const tenant = "11111111-1111-1111-1111-111111111111"
	const user = "22222222-2222-2222-2222-222222222222"
	const ungated = "audit,policy,control,policy_ack"
	ctx := context.Background()
	for _, tc := range []struct {
		role authz.Role
		want string
	}{
		{authz.RoleAdmin, ""}, // nothing denied: "" is every type
		{authz.RoleGRCEngineer, ""},
		{authz.RoleViewer, "audit,policy,vendor,control,risk,policy_ack"},
		{authz.RoleAuditor, "audit,exception,policy,control,policy_ack"},
		{authz.RoleControlOwner, ungated},
	} {
		got, left, err := ReadableTypesFor(ctx, mustEngine(t, fixedRoles{tc.role}), tenant, user, "")
		if err != nil || !left || got != tc.want {
			t.Errorf("%s: ReadableTypesFor = %q, %v, %v; want %q", tc.role, got, left, err, tc.want)
		}
	}

	for _, tc := range []struct {
		name    string
		engine  *authz.Engine
		user    string
		wantErr bool
	}{
		{"no engine", nil, user, false},
		{"no subscriber", mustEngine(t, fixedRoles{authz.RoleAdmin}), "", false},
		{"no live role", mustEngine(t, fixedRoles{}), user, false},
		{"role lookup fails", mustEngine(t, failingRoles{}), user, true},
	} {
		got, left, err := ReadableTypesFor(ctx, tc.engine, tenant, tc.user, "")
		if got != ungated || !left || (err != nil) != tc.wantErr {
			t.Errorf("%s: ReadableTypesFor = %q, %v, %v; want %q, err=%v", tc.name, got, left, err, ungated, tc.wantErr)
		}
	}

	// Asking only for gated types the role cannot read leaves nothing.
	co := mustEngine(t, fixedRoles{authz.RoleControlOwner})
	if got, left, _ := ReadableTypesFor(ctx, co, tenant, user, "risk,vendor"); left || got != "" {
		t.Errorf("control_owner asking risk,vendor = %q, %v; want none", got, left)
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
