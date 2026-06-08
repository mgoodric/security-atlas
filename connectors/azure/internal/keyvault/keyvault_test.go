package keyvault_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/keyvault"
)

// fakeARM is a faked Azure Resource Manager surface — NO live Azure in tests.
type fakeARM struct {
	vaults []keyvault.RawVault
	err    error
}

func (f *fakeARM) ListVaults(_ context.Context) ([]keyvault.RawVault, error) {
	return f.vaults, f.err
}

var fixedNow = func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

// hardened is a well-configured vault: RBAC mode, purge protection + soft-delete
// on, public network disabled.
func hardened(id, name string) keyvault.RawVault {
	return keyvault.RawVault{
		ID: id, Name: name, ResourceGroup: "rg", Location: "eastus",
		RBACAuthorization: true, PurgeProtection: true, SoftDeleteEnabled: true,
		PublicNetworkAccess: "Disabled",
		AccessEntries: []keyvault.AccessEntry{
			{PrincipalID: "00000000-0000-0000-0000-000000000001", PrincipalType: "rbac_role_assignment", RoleName: "Key Vault Reader"},
		},
	}
}

func TestInspect_PassWhenHardened(t *testing.T) {
	api := &fakeARM{vaults: []keyvault.RawVault{hardened("/sub/kv1", "kv1")}}
	got, err := keyvault.Inspect(context.Background(), api, "sub-1", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 || got[0].Result != keyvault.ResultPass {
		t.Fatalf("want 1 PASS; got %+v", got)
	}
	if got[0].SubscriptionID != "sub-1" {
		t.Errorf("subscription = %q; want sub-1", got[0].SubscriptionID)
	}
	if !got[0].RBACAuthorization || len(got[0].AccessEntries) != 1 {
		t.Errorf("config / access fields not preserved: %+v", got[0])
	}
}

func TestInspect_FailMatrix(t *testing.T) {
	cases := []struct {
		name   string
		vault  keyvault.RawVault
		reason string
	}{
		{
			"no-soft-delete",
			keyvault.RawVault{ID: "/s/v", Name: "v", PurgeProtection: true, SoftDeleteEnabled: false},
			"soft-delete",
		},
		{
			"no-purge-protection",
			keyvault.RawVault{ID: "/s/v", Name: "v", SoftDeleteEnabled: true, PurgeProtection: false},
			"purge protection",
		},
		{
			"public-open-no-deny",
			keyvault.RawVault{ID: "/s/v", Name: "v", SoftDeleteEnabled: true, PurgeProtection: true,
				PublicNetworkAccess: "Enabled", NetworkACLDefault: "Allow"},
			"public network",
		},
		{
			"public-enabled-no-acl",
			keyvault.RawVault{ID: "/s/v", Name: "v", SoftDeleteEnabled: true, PurgeProtection: true,
				PublicNetworkAccess: "Enabled", NetworkACLDefault: ""},
			"public network",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := keyvault.Inspect(context.Background(), &fakeARM{vaults: []keyvault.RawVault{tc.vault}}, "sub", fixedNow)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if got[0].Result != keyvault.ResultFail {
				t.Errorf("result = %q; want fail", got[0].Result)
			}
			if !strings.Contains(got[0].Reason, tc.reason) {
				t.Errorf("reason = %q; want substring %q", got[0].Reason, tc.reason)
			}
		})
	}
}

func TestInspect_PassWhenPublicEnabledButDenyDefault(t *testing.T) {
	// Public network access "Enabled" but a default-Deny network ACL means the
	// vault is firewalled — PASS.
	v := keyvault.RawVault{ID: "/s/v", Name: "v", SoftDeleteEnabled: true, PurgeProtection: true,
		PublicNetworkAccess: "Enabled", NetworkACLDefault: "Deny"}
	got, _ := keyvault.Inspect(context.Background(), &fakeARM{vaults: []keyvault.RawVault{v}}, "sub", fixedNow)
	if got[0].Result != keyvault.ResultPass {
		t.Errorf("result = %q; want pass (default-Deny network ACL firewalls the vault)", got[0].Result)
	}
}

func TestInspect_InconclusiveOnReadError(t *testing.T) {
	v := hardened("/sub/kv", "kv")
	v.ReadError = "throttled"
	got, err := keyvault.Inspect(context.Background(), &fakeARM{vaults: []keyvault.RawVault{v}}, "sub", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != keyvault.ResultInconclusive {
		t.Errorf("result = %q; want inconclusive", got[0].Result)
	}
}

func TestInspect_SkipsIncompleteVaults(t *testing.T) {
	api := &fakeARM{vaults: []keyvault.RawVault{
		{ID: "", Name: "x"},
		{ID: "y", Name: ""},
		hardened("/sub/ok", "ok"),
	}}
	got, _ := keyvault.Inspect(context.Background(), api, "sub", fixedNow)
	if len(got) != 1 || got[0].VaultName != "ok" {
		t.Fatalf("expected 1 valid vault; got %+v", got)
	}
}

func TestInspect_PropagatesListError(t *testing.T) {
	sentinel := errors.New("arm 403")
	_, err := keyvault.Inspect(context.Background(), &fakeARM{err: sentinel}, "sub", fixedNow)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

func TestInspect_NilAPIRejected(t *testing.T) {
	_, err := keyvault.Inspect(context.Background(), nil, "sub", nil)
	if err == nil {
		t.Fatal("expected error for nil API")
	}
}

// P0-521-2 (structural over-collection guard): the VaultConfig / AccessEntry /
// RawVault structs must carry CONFIGURATION + access METADATA ONLY — never a
// secret, key, or certificate VALUE. This test reflects over every struct's
// field names and FAILS if any field name even hints at a secret-material
// surface, so a future field that opens a data-plane over-collection door trips
// the build.
func TestStructs_MetadataOnly_NoSecretValueFields(t *testing.T) {
	banned := []string{
		"value", "secret", "password", "credential", "privatekey",
		"private_key", "keymaterial", "key_material", "pem", "pfx",
		"certificatecontents", "certificate_contents", "passphrase", "token",
		"connectionstring", "connection_string",
	}
	// allow names that legitimately contain a banned token as a substring but
	// carry no secret material (permission/role/posture metadata).
	allow := map[string]bool{
		"NetworkACLDefault": true, // "default" — not a secret
	}
	check := func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i).Name
			if allow[field] {
				continue
			}
			name := strings.ToLower(field)
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: field name contains banned secret-material token %q — metadata-only struct must not carry secret/key/cert values",
						typ.Name(), field, b)
				}
			}
		}
	}
	check(reflect.TypeOf(keyvault.VaultConfig{}))
	check(reflect.TypeOf(keyvault.AccessEntry{}))
	check(reflect.TypeOf(keyvault.RawVault{}))
}

// TestRBACRoleAssignment_MetadataOnly_PinsNewPath (slice 615) pins the RBAC
// second-read path through the SAME metadata-only struct as the access-policy
// path: an rbac_role_assignment entry carries a principal id + a role NAME and
// nothing else. The AccessEntry struct has no field capable of holding a
// secret/key/certificate value, so the RBAC path cannot over-collect by
// construction (the structural guard above covers the type; this pins the path
// that now populates it). The merge is exercised end-to-end through Inspect.
func TestRBACRoleAssignment_MetadataOnly_PinsNewPath(t *testing.T) {
	v := keyvault.RawVault{
		ID: "/sub/kv", Name: "kv", ResourceGroup: "rg", Location: "eastus",
		RBACAuthorization: true, PurgeProtection: true, SoftDeleteEnabled: true,
		PublicNetworkAccess: "Disabled",
		AccessEntries: []keyvault.AccessEntry{
			{PrincipalID: "00000000-0000-0000-0000-000000000001", PrincipalType: "rbac_role_assignment", RoleName: "Key Vault Reader"},
			{PrincipalID: "00000000-0000-0000-0000-000000000002", PrincipalType: "rbac_role_assignment", RoleName: "Key Vault Secrets User"},
		},
	}
	got, _ := keyvault.Inspect(context.Background(), &fakeARM{vaults: []keyvault.RawVault{v}}, "sub", fixedNow)
	if len(got) != 1 || len(got[0].AccessEntries) != 2 {
		t.Fatalf("RBAC role assignments not preserved through Inspect: %+v", got)
	}
	for _, e := range got[0].AccessEntries {
		if e.PrincipalType != "rbac_role_assignment" || e.RoleName == "" {
			t.Errorf("rbac entry malformed: %+v", e)
		}
		// An RBAC role assignment carries NO access-policy permission verbs —
		// the metadata is principal + role name only.
		if len(e.Permissions) != 0 {
			t.Errorf("rbac entry must carry no permission verbs: %+v", e)
		}
	}
}

// TestMetadataOnly_AccessFieldsPreserved pins that the documented access /
// config fields survive the Inspect transform (the positive companion to the
// structural guard).
func TestMetadataOnly_AccessFieldsPreserved(t *testing.T) {
	v := keyvault.RawVault{
		ID: "/sub/kv", Name: "kv", ResourceGroup: "rg", Location: "eastus",
		RBACAuthorization: false, PurgeProtection: true, SoftDeleteEnabled: true,
		PublicNetworkAccess: "Disabled", NetworkACLDefault: "Deny",
		AccessEntries: []keyvault.AccessEntry{
			{PrincipalID: "p-1", PrincipalType: "access_policy", Permissions: []string{"secrets:get", "keys:list"}},
		},
	}
	got, _ := keyvault.Inspect(context.Background(), &fakeARM{vaults: []keyvault.RawVault{v}}, "sub", fixedNow)
	c := got[0]
	if c.ResourceGroup != "rg" || c.Location != "eastus" || !c.PurgeProtection ||
		!c.SoftDeleteEnabled || c.PublicNetworkAccess != "Disabled" || c.NetworkACLDefault != "Deny" {
		t.Errorf("config fields not preserved: %+v", c)
	}
	if len(c.AccessEntries) != 1 {
		t.Fatalf("access entries not preserved: %+v", c.AccessEntries)
	}
	e := c.AccessEntries[0]
	if e.PrincipalID != "p-1" || e.PrincipalType != "access_policy" ||
		len(e.Permissions) != 2 || e.Permissions[0] != "secrets:get" {
		t.Errorf("access entry fields not preserved: %+v", e)
	}
}
