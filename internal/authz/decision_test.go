package authz_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// engine returns a fresh Engine for unit tests. NoopRolesResolver so the
// roles in the input are taken at face value.
func engine(t *testing.T) *authz.Engine {
	t.Helper()
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	return e
}

func TestDecide_AdminAllowsWrite(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-1",
			Roles: []authz.Role{authz.RoleAdmin},
		},
		TenantID: "00000000-0000-0000-0000-000000000001",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "risks"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/risks"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allow {
		t.Fatalf("expected allow, got deny: %s", d.Reason)
	}
}

func TestDecide_ViewerDeniedWrite(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-2",
			Roles: []authz.Role{authz.RoleViewer},
		},
		TenantID: "00000000-0000-0000-0000-000000000002",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "risks"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/risks"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("expected deny, got allow")
	}
}

func TestDecide_DefaultDenyEmptyRoles(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-3",
			Roles: nil,
		},
		TenantID: "00000000-0000-0000-0000-000000000003",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "controls"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/controls"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("expected default-deny on empty roles, got allow")
	}
}

func TestDecide_AuditorReadSamplesWithinPeriod(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-4",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{
				"audit_period_ids": []interface{}{"period-1"},
			},
		},
		TenantID: "00000000-0000-0000-0000-000000000004",
		Action:   "read",
		Resource: authz.ResourceInput{
			Type: "samples",
			ID:   "sample-9",
			Attrs: map[string]interface{}{
				"audit_period_id": "period-1",
			},
		},
		Request: authz.RequestInput{Method: "GET", Path: "/v1/samples/sample-9"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allow {
		t.Fatalf("expected auditor allow within their period, got deny: %s", d.Reason)
	}
}

func TestDecide_AuditorReadSamplesOutsidePeriodDenied(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-5",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{
				"audit_period_ids": []interface{}{"period-1"},
			},
		},
		TenantID: "00000000-0000-0000-0000-000000000005",
		Action:   "read",
		Resource: authz.ResourceInput{
			Type: "samples",
			ID:   "sample-9",
			Attrs: map[string]interface{}{
				"audit_period_id": "period-OUTSIDE",
			},
		},
		Request: authz.RequestInput{Method: "GET", Path: "/v1/samples/sample-9"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("expected auditor deny outside their period, got allow")
	}
}

func TestDecide_PublicCatalogReadAllowed(t *testing.T) {
	t.Parallel()
	e := engine(t)
	// A viewer reading the SCF anchor catalog: should be allowed by the
	// defaults.rego catalog_resources rule (anchors).
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-6",
			Roles: []authz.Role{authz.RoleViewer},
		},
		TenantID: "00000000-0000-0000-0000-000000000006",
		Action:   "read",
		Resource: authz.ResourceInput{Type: "anchors"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/anchors"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allow {
		t.Fatalf("expected catalog read allow, got deny: %s", d.Reason)
	}
}
