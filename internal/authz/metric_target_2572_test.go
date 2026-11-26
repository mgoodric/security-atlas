package authz_test

// #2572 — PUT /v1/metrics/{id}/target derives action "write" on "metrics".
// admin and grc_engineer may set a metric target, as they set a framework
// posture target (#2136); every other role is denied. The grc_engineer
// admit is bound to the target path: manual input
// (POST /v1/metrics/{id}/inputs) stays admin's.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func metricInput2572(t *testing.T, method, path string) authz.Input {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r = r.WithContext(authctx.WithCredential(r.Context(),
		credstore.Credential{ID: "jwt:2572", TenantID: "00000000-0000-4000-8000-000000002572", UserID: "user-2572"}))
	return authz.BuildInput(r, nil)
}

func TestMetricTarget2572_AdmitsAdminAndGRCEngineer(t *testing.T) {
	t.Parallel()
	e := engine(t)
	in := metricInput2572(t, http.MethodPut, "/v1/metrics/control_pass_rate/target")
	if in.Action != "write" || in.Resource.Type != "metrics" {
		t.Fatalf("PUT target derives %s on %s, want write on metrics", in.Action, in.Resource.Type)
	}
	for _, c := range []struct {
		name  string
		roles []authz.Role
		want  bool
	}{
		{"admin", []authz.Role{authz.RoleAdmin}, true},
		{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
		{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
		{"auditor", []authz.Role{authz.RoleAuditor}, false},
		{"viewer", []authz.Role{authz.RoleViewer}, false},
		{"no_role", []authz.Role{}, false},
	} {
		in.User.Roles = c.roles
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatalf("%s: Decide: %v", c.name, err)
		}
		if d.Allow != c.want {
			t.Errorf("%s: allow=%v, want %v (reason=%s)", c.name, d.Allow, c.want, d.Reason)
		}
	}
}

func TestMetricTarget2572_GRCAdmitStaysOnTheTarget(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/metrics/control_pass_rate/inputs"},
		{http.MethodPost, "/v1/metrics/control_pass_rate/target"},
		{http.MethodPut, "/v1/metrics/control_pass_rate/target/extra"},
	} {
		in := metricInput2572(t, c.method, c.path)
		in.User.Roles = []authz.Role{authz.RoleGRCEngineer}
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatalf("%s %s: Decide: %v", c.method, c.path, err)
		}
		if d.Allow {
			t.Errorf("grc_engineer %s %s allowed; the #2572 admit must stay on PUT .../target", c.method, c.path)
		}
	}
}
