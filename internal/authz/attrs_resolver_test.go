package authz_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// stubAttrsResolver records calls + returns canned attributes.
type stubAttrsResolver struct {
	calls int
	out   map[string]interface{}
	err   error
}

func (s *stubAttrsResolver) AttrsFor(_ context.Context, _, _ string, _ []authz.Role) (map[string]interface{}, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.out, nil
}

// TestAttrsResolver_OnlyAuditorTrafficHitsResolver: the resolver MUST NOT
// be called for grc_engineer, admin, viewer, or control_owner. Slice 025's
// design promise -- non-auditor requests get the same latency as before.
func TestAttrsResolver_OnlyAuditorTrafficHitsResolver(t *testing.T) {
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	stub := &stubAttrsResolver{}
	e = e.WithAttrsResolver(stub)

	tenant := "00000000-0000-0000-0000-000000000001"
	for _, role := range []authz.Role{
		authz.RoleAdmin,
		authz.RoleGRCEngineer,
		authz.RoleControlOwner,
		authz.RoleViewer,
	} {
		_, err := e.Decide(context.Background(), authz.Input{
			TenantID: tenant,
			User: authz.UserInput{
				ID:    "user-1",
				Roles: []authz.Role{role},
				Attrs: map[string]interface{}{},
			},
			Action:   "read",
			Resource: authz.ResourceInput{Type: "controls"},
			Request:  authz.RequestInput{Method: "GET", Path: "/v1/controls"},
		})
		if err != nil {
			t.Fatalf("Decide(%s): %v", role, err)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("AttrsResolver called %d times for non-auditor roles; want 0", stub.calls)
	}
}

// TestAttrsResolver_AuditorTriggersResolver: when a request carries
// auditor role and no pre-populated audit_period_ids, the resolver is
// called exactly once.
func TestAttrsResolver_AuditorTriggersResolver(t *testing.T) {
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	stub := &stubAttrsResolver{
		out: map[string]interface{}{
			"audit_period_ids": []interface{}{"period-A"},
		},
	}
	e = e.WithAttrsResolver(stub)

	_, err = e.Decide(context.Background(), authz.Input{
		TenantID: "00000000-0000-0000-0000-000000000002",
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{},
		},
		Action:   "read",
		Resource: authz.ResourceInput{Type: "controls"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/controls"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("AttrsResolver calls = %d; want 1", stub.calls)
	}
}

// TestAttrsResolver_AuditorWithPrePopulatedAttrsSkipsResolver: the matrix
// integration test pre-populates input.user.attrs.audit_period_ids
// directly; the resolver must respect that and NOT overwrite it.
func TestAttrsResolver_AuditorWithPrePopulatedAttrsSkipsResolver(t *testing.T) {
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	stub := &stubAttrsResolver{
		out: map[string]interface{}{
			"audit_period_ids": []interface{}{"period-X"},
		},
	}
	e = e.WithAttrsResolver(stub)

	_, err = e.Decide(context.Background(), authz.Input{
		TenantID: "00000000-0000-0000-0000-000000000003",
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{
				"audit_period_ids": []interface{}{"period-A"},
			},
		},
		Action:   "read",
		Resource: authz.ResourceInput{Type: "samples", Attrs: map[string]interface{}{"audit_period_id": "period-A"}},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/samples"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("AttrsResolver was called despite pre-populated attrs; want 0 got %d", stub.calls)
	}
}

// TestAttrsResolver_NilResolverIsNoop: legacy callers that don't wire a
// resolver via WithAttrsResolver get the same behaviour as before slice
// 025.
func TestAttrsResolver_NilResolverIsNoop(t *testing.T) {
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// No WithAttrsResolver call -- e.attrsResolver is nil.
	_, err = e.Decide(context.Background(), authz.Input{
		TenantID: "00000000-0000-0000-0000-000000000004",
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{},
		},
		Action:   "read",
		Resource: authz.ResourceInput{Type: "controls"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/controls"},
	})
	if err != nil {
		t.Fatalf("Decide with nil resolver: %v", err)
	}
}
