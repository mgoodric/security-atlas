package authz_test

// #2431: a labelled API key bound to a user holds its owner's live roles
// capped by its label. Each label entry gives the role it derives on an
// unbound key (a free-text owner-role label gives control_owner); a live admin
// owner gets the whole ceiling; an empty label is not capped. The cap reaches
// both OPA and Decision.LiveRoles, which authzmw hands the handler gates.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const (
	tenant2431 = "00000000-0000-4000-8000-000000002431"
	owner2431  = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2431"
)

// liveRoles2431 returns the LiveRoles of an allowed GET /v1/controls as cred
// against an owner holding live, which every canonical role reads.
func liveRoles2431(t *testing.T, cred credstore.Credential, live []authz.Role) ([]authz.Role, bool) {
	t.Helper()
	e, err := authz.NewEngine(context.Background(), tenantResolver{{tenant2431, owner2431}: live})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), cred))
	d, err := e.Decide(context.Background(), authz.BuildInput(r, nil))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d.LiveRoles, d.Allow
}

func TestDecide2431_BoundKeyCeiling(t *testing.T) {
	t.Parallel()
	key := func(labels ...string) credstore.Credential {
		return credstore.Credential{ID: "key_2431", TenantID: tenant2431, UserID: owner2431, OwnerRoles: labels}
	}
	R := func(rs ...authz.Role) []authz.Role { return rs }
	for _, c := range []struct {
		name string
		cred credstore.Credential
		live []authz.Role
		want []authz.Role // nil: denied, no roles
	}{
		{"viewer label, admin owner", key("viewer"), R(authz.RoleAdmin), R(authz.RoleViewer)},
		{"viewer label, viewer owner", key("viewer"), R(authz.RoleViewer), R(authz.RoleViewer)},
		{"viewer label, grc_engineer owner", key("viewer"), R(authz.RoleGRCEngineer), nil},
		{"viewer label, owner has no roles", key("viewer"), nil, nil},
		{"control_owner label, admin+viewer owner", key("control_owner"), R(authz.RoleViewer, authz.RoleAdmin), R(authz.RoleControlOwner)},
		{"admin label, admin owner", key("admin"), R(authz.RoleAdmin), R(authz.RoleAdmin)},
		{"admin label, viewer owner", key("admin"), R(authz.RoleViewer), nil},
		{"free-text label, control_owner owner", key("platform-eng"), R(authz.RoleControlOwner), R(authz.RoleControlOwner)},
		{"free-text label, viewer owner", key("platform-eng"), R(authz.RoleViewer), nil},
		{"mixed label, viewer+auditor owner", key("viewer", "control_owner"), R(authz.RoleViewer, authz.RoleAuditor), R(authz.RoleViewer)},
		{"mixed label, admin owner", key("viewer", "platform-eng"), R(authz.RoleAdmin), R(authz.RoleViewer, authz.RoleControlOwner)},
		{"no label, admin owner: not capped", key(), R(authz.RoleAdmin), R(authz.RoleAdmin)},
		// No label: exactly the owner's live roles, no grc_engineer default.
		{"no label, viewer owner", key(), R(authz.RoleViewer), R(authz.RoleViewer)},
		{"no label, owner has no roles", key(), nil, nil},
	} {
		got, allow := liveRoles2431(t, c.cred, c.live)
		if allow != (c.want != nil) {
			t.Errorf("%s: allow = %v, want %v", c.name, allow, c.want != nil)
			continue
		}
		if allow && !slices.Equal(got, c.want) {
			t.Errorf("%s: LiveRoles = %v, want %v", c.name, got, c.want)
		}
	}
}

// The admin and approver flags keep their own derivation (out of scope), and
// an unbound key is untouched: its label is its grant.
func TestDecide2431_NotCapped(t *testing.T) {
	t.Parallel()
	for _, cred := range []credstore.Credential{
		{ID: "key_2431a", TenantID: tenant2431, UserID: owner2431, IsAdmin: true, OwnerRoles: []string{"viewer"}},
		{ID: "key_2431u", TenantID: tenant2431, UserID: "key_2431u", OwnerRoles: []string{"viewer"}},
	} {
		if _, allow := liveRoles2431(t, cred, nil); !allow {
			t.Errorf("%s: GET /v1/controls refused; want the derivation it had before #2431", cred.ID)
		}
	}
}

// An unbound key with no label keeps the grc_engineer default (#2431 changes
// only keys bound to a user): it still writes controls, which grc_engineer
// grants, while an unlabelled key bound to a user with no roles does not.
func TestDecide2431_UnlabelledUnboundKeyKeepsGRCEngineer(t *testing.T) {
	t.Parallel()
	e, err := authz.NewEngine(context.Background(), tenantResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for _, c := range []struct {
		cred credstore.Credential
		want bool
	}{
		{credstore.Credential{ID: "key_2431e", TenantID: tenant2431, UserID: "key_2431e"}, true},
		{credstore.Credential{ID: "key_2431f", TenantID: tenant2431, UserID: owner2431}, false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/controls", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), c.cred))
		d, err := e.Decide(context.Background(), authz.BuildInput(r, nil))
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Allow != c.want {
			t.Errorf("%s (UserID %s), no label: POST /v1/controls allow = %v, want %v", c.cred.ID, c.cred.UserID, d.Allow, c.want)
		}
	}
}

// The handler gates read the capped set: a viewer key owned by an admin is
// not a tenant admin there.
func TestDecide2431_HandlerGatesSeeTheCap(t *testing.T) {
	t.Parallel()
	cred := credstore.Credential{ID: "key_2431", TenantID: tenant2431, UserID: owner2431, OwnerRoles: []string{"viewer"}}
	live, _ := liveRoles2431(t, cred, []authz.Role{authz.RoleAdmin})
	for _, r := range live {
		cred.TenantRoles = append(cred.TenantRoles, string(r))
	}
	if authz.IsTenantAdmin(cred) || authz.IsTenantApprover(cred) {
		t.Errorf("viewer key owned by an admin passes an admin or approver gate with TenantRoles %v", cred.TenantRoles)
	}
	if !authz.IsTenantRiskReader(cred) {
		t.Errorf("viewer key owned by an admin fails the viewer risk-read gate with TenantRoles %v", cred.TenantRoles)
	}
}
