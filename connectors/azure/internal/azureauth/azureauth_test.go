package azureauth_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
)

func TestResolve_ClientCredentialsFromOpts(t *testing.T) {
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{
		Mode:         azureauth.ModeClientCredentials,
		TenantID:     "tenant-1",
		ClientID:     "client-1",
		ClientSecret: "test-azure-client-secret",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Mode() != azureauth.ModeClientCredentials {
		t.Errorf("mode = %q; want client-credentials", cred.Mode())
	}
	if cred.TenantID() != "tenant-1" {
		t.Errorf("tenant = %q; want tenant-1", cred.TenantID())
	}
	if cred.ClientID() != "client-1" {
		t.Errorf("client = %q; want client-1", cred.ClientID())
	}
}

func TestResolve_DefaultsToClientCredentials(t *testing.T) {
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{
		TenantID: "tenant-1", ClientID: "client-1", ClientSecret: "test-azure-client-secret",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Mode() != azureauth.ModeClientCredentials {
		t.Errorf("mode = %q; want default client-credentials", cred.Mode())
	}
}

func TestResolve_ClientCredentialsFromEnv(t *testing.T) {
	t.Setenv(azureauth.EnvTenantID, "env-tenant")
	t.Setenv(azureauth.EnvClientID, "env-client")
	t.Setenv(azureauth.EnvClientSecret, "test-env-secret")
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.TenantID() != "env-tenant" || cred.ClientID() != "env-client" {
		t.Errorf("env fallback failed: %+v", cred)
	}
}

func TestResolve_ManagedIdentityNeedsNoSecret(t *testing.T) {
	t.Setenv(azureauth.EnvClientSecret, "")
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{
		Mode:     azureauth.ModeManagedIdentity,
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("Resolve managed-identity: %v", err)
	}
	if cred.Mode() != azureauth.ModeManagedIdentity {
		t.Errorf("mode = %q; want managed-identity", cred.Mode())
	}
}

func TestResolve_FailsOnMissingTenant(t *testing.T) {
	t.Setenv(azureauth.EnvTenantID, "")
	_, err := azureauth.Resolve(azureauth.ResolveOpts{Mode: azureauth.ModeManagedIdentity})
	if err == nil {
		t.Fatal("expected error for missing tenant id")
	}
	if !strings.Contains(err.Error(), "tenant") {
		t.Errorf("err = %v; want tenant mention", err)
	}
}

func TestResolve_FailsOnMissingClientID(t *testing.T) {
	t.Setenv(azureauth.EnvClientID, "")
	t.Setenv(azureauth.EnvClientSecret, "")
	_, err := azureauth.Resolve(azureauth.ResolveOpts{TenantID: "tenant-1"})
	if err == nil {
		t.Fatal("expected error for missing client id")
	}
	if !strings.Contains(err.Error(), "client id") {
		t.Errorf("err = %v; want client id mention", err)
	}
}

func TestResolve_FailsOnMissingSecret(t *testing.T) {
	t.Setenv(azureauth.EnvClientSecret, "")
	_, err := azureauth.Resolve(azureauth.ResolveOpts{TenantID: "tenant-1", ClientID: "client-1"})
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !strings.Contains(err.Error(), "secret") {
		t.Errorf("err = %v; want secret mention", err)
	}
}

func TestResolve_UnknownModeRejected(t *testing.T) {
	_, err := azureauth.Resolve(azureauth.ResolveOpts{Mode: azureauth.AuthMode("nope"), TenantID: "t"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

// P0-486-4: the client secret must NOT appear in any String / format output.
func TestCredential_StringRedacts(t *testing.T) {
	const secret = "test-azure-secret-redaction-check"
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{
		TenantID: "tenant-1", ClientID: "client-1", ClientSecret: secret,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, s := range []string{
		cred.String(),
		fmt.Sprintf("%v", cred),
		fmt.Sprintf("%+v", cred),
		fmt.Sprintf("%#v", cred),
	} {
		if strings.Contains(s, secret) {
			t.Fatalf("secret leaked in format output: %q", s)
		}
	}
	if !strings.Contains(cred.String(), "<redacted") {
		t.Errorf("String missing redaction marker: %q", cred.String())
	}
}

// P0-486-2: DocumentedPermissions must NEVER include write / manage / admin /
// Owner / Contributor / Global Administrator.
func TestDocumentedPermissions_ReadOnly(t *testing.T) {
	perms := azureauth.DocumentedPermissions()
	if len(perms) == 0 {
		t.Fatal("DocumentedPermissions empty")
	}
	banned := []string{"write", "manage", "owner", "contributor", "administrator", "delete", "readwrite"}
	for _, p := range perms {
		if p.Access != "Read" {
			t.Errorf("permission %q has Access=%q; want Read", p.Name, p.Access)
		}
		low := strings.ToLower(p.Name)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("permission %q contains banned substring %q (over-privilege)", p.Name, b)
			}
		}
	}
}

func TestDocumentedPermissions_CoversBothSurfaces(t *testing.T) {
	perms := azureauth.DocumentedPermissions()
	surfaces := map[string]bool{}
	for _, p := range perms {
		surfaces[p.Surface] = true
	}
	if !surfaces["Microsoft Graph"] {
		t.Error("missing Microsoft Graph permissions (gates the Entra kind)")
	}
	if !surfaces["Azure Resource Manager"] {
		t.Error("missing Azure Resource Manager role (gates the Storage kind)")
	}
}

func TestParseMode(t *testing.T) {
	cases := map[string]struct {
		want azureauth.AuthMode
		err  bool
	}{
		"client-credentials": {azureauth.ModeClientCredentials, false},
		"managed-identity":   {azureauth.ModeManagedIdentity, false},
		"":                   {azureauth.ModeClientCredentials, false},
		"bogus":              {"", true},
	}
	for in, tc := range cases {
		got, err := azureauth.ParseMode(in)
		if tc.err {
			if err == nil {
				t.Errorf("ParseMode(%q): want error", in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q): %v", in, err)
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %q; want %q", in, got, tc.want)
		}
	}
}
