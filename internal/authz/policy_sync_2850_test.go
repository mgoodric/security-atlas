package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// #2850: a policy-sync key derives no role, so no role rule admits it, and
// system.rego admits it to POST /v1/policies:sync only. An ordinary
// unbound key keeps its grc_engineer default and is not admitted there by
// the policy-sync rule.
func TestPolicySyncKey_OPAAdmitsSyncRouteOnly(t *testing.T) {
	t.Parallel()
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	tenant := "00000000-0000-0000-0000-000000000001"
	decide := func(cred credstore.Credential, method, path string) (authz.Input, bool) {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), cred))
		in := authz.BuildInput(r, nil)
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatalf("Decide %s %s: %v", method, path, err)
		}
		return in, d.Allow
	}
	sync := credstore.Credential{ID: "key_sync", TenantID: tenant, Purpose: credstore.PurposePolicySync}

	in, allowed := decide(sync, http.MethodPost, "/v1/policies:sync")
	if !allowed {
		t.Errorf("policy-sync key denied POST /v1/policies:sync")
	}
	if len(in.User.Roles) != 0 || in.User.Attrs["is_policy_sync_key"] != true {
		t.Errorf("policy-sync key input: roles %v, is_policy_sync_key %v", in.User.Roles, in.User.Attrs["is_policy_sync_key"])
	}
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/v1/policies:sync"},
		{http.MethodPost, "/v1/policies"},
		{http.MethodGet, "/v1/policies"},
		{http.MethodPost, "/v1/evidence:push"},
		{http.MethodGet, "/v1/controls"},
	} {
		if _, allowed := decide(sync, rt.method, rt.path); allowed {
			t.Errorf("policy-sync key allowed %s %s", rt.method, rt.path)
		}
	}

	ordinary := credstore.Credential{ID: "key_plain", TenantID: tenant}
	if in, _ := decide(ordinary, http.MethodPost, "/v1/policies:sync"); in.User.Attrs["is_policy_sync_key"] != false {
		t.Errorf("ordinary key is_policy_sync_key = %v", in.User.Attrs["is_policy_sync_key"])
	}
}
