package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// rolelessSelfReadPaths are the two reads the "no role in this
// organisation" page makes (defaults.rego roleless_self_read_paths).
var rolelessSelfReadPaths = map[string]bool{
	"/v1/me":         true,
	"/v1/me/tenants": true,
}

// #2494: a signed-in user with no role reads its own profile and tenant
// list, so the web can show the no-role page and a tenant switcher.
func TestNoTenantRole2494_RolelessReadsProfileAndTenants(t *testing.T) {
	t.Parallel()
	for p := range rolelessSelfReadPaths {
		if d := decide(t, meReadInput(nil, p)); !d.Allow {
			t.Errorf("roleless user denied GET %s: %s", p, d.Reason)
		}
	}
}

// #2494: the roleless admit is reads of those two exact paths, for a
// credential naming a user in a tenant. Nothing else opens.
func TestNoTenantRole2494_RolelessStaysDeniedElsewhere(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/v1/me/preferences", "/v1/me/tenants/extra", "/v1/me/", "/v1/me/sessions"} {
		if d := decide(t, meReadInput(nil, p)); d.Allow {
			t.Errorf("roleless user admitted to GET %s", p)
		}
	}
	for p := range rolelessSelfReadPaths {
		write := meReadInput(nil, p)
		write.Action = "write"
		write.Request.Method = "PATCH"
		if d := decide(t, write); d.Allow {
			t.Errorf("roleless user admitted to PATCH %s", p)
		}
		noTenant := meReadInput(nil, p)
		noTenant.TenantID = ""
		if d := decide(t, noTenant); d.Allow {
			t.Errorf("credential with no tenant admitted to GET %s", p)
		}
		noUser := meReadInput(nil, p)
		noUser.User.ID = ""
		if d := decide(t, noUser); d.Allow {
			t.Errorf("credential naming no user admitted to GET %s", p)
		}
	}
}

func TestHasTenantRole2494(t *testing.T) {
	t.Parallel()
	const userID = "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name string
		cred credstore.Credential
		live []string
		want bool
	}{
		{"user session, no live role", credstore.Credential{ID: "jwt:a", TenantID: tenant, UserID: "user:" + userID}, nil, false},
		{"user session, live viewer", credstore.Credential{ID: "jwt:a", TenantID: tenant, UserID: "user:" + userID}, []string{"viewer"}, true},
		{"user session, stale claim, no live role", credstore.Credential{ID: "jwt:a", TenantID: tenant, UserID: "user:" + userID, OwnerRoles: []string{"admin"}}, nil, false},
		{"super-admin session, no live role", credstore.Credential{ID: "jwt:a", TenantID: tenant, UserID: "user:" + userID, IsAdmin: true}, nil, true},
		{"client_credentials token", credstore.Credential{ID: "jwt:a", TenantID: tenant, UserID: oauth.MachineSubjectPrefix + "c1"}, nil, true},
		{"unbound API key", credstore.Credential{ID: "key_k1", TenantID: tenant, UserID: "key_k1"}, nil, true},
		{"user-bound viewer API key, owner has no live role", credstore.Credential{ID: "key_k1", TenantID: tenant, UserID: userID, OwnerRoles: []string{"viewer"}}, nil, false},
	}
	for _, c := range cases {
		if got := authz.HasTenantRole(c.cred, c.live); got != c.want {
			t.Errorf("%s: HasTenantRole = %v, want %v", c.name, got, c.want)
		}
	}
}

// #2494 (from the #2487 review): input.go matches client_credentials
// tokens by the literal "oauth_client:", which must stay the prefix the
// token endpoint stamps. authz keeps the literal rather than import the
// token endpoint's package, so this pins the two together: a token minted with oauth.MachineSubjectPrefix is a machine
// actor and keeps its default role with an empty claim.
func TestMachineSubjectPrefixPinned2494(t *testing.T) {
	t.Parallel()
	req := buildRequestWithCredential(credstore.Credential{
		ID:       "jwt:pin",
		TenantID: tenant,
		UserID:   oauth.MachineSubjectPrefix + "0e3b4a7e-1111-2222-3333-444455556666",
	})
	in := authz.BuildInput(req, nil)
	if got, _ := in.User.Attrs["is_machine_actor"].(bool); !got {
		t.Errorf("is_machine_actor = false for a %q subject; input.go's literal has drifted from oauth.MachineSubjectPrefix", oauth.MachineSubjectPrefix)
	}
	if len(in.User.Roles) == 0 {
		t.Errorf("a %q token derived no roles; input.go's machineJWT literal has drifted from oauth.MachineSubjectPrefix", oauth.MachineSubjectPrefix)
	}
}
