package intuneauth

import (
	"strings"
	"testing"
)

func TestResolve_FromOpts(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{TenantID: "tenant-1", ClientID: "client-1", ClientSecret: "fake-graph-secret"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.TenantID() != "tenant-1" || cred.ClientID() != "client-1" || cred.ClientSecret() != "fake-graph-secret" {
		t.Errorf("creds wrong: %+v", cred)
	}
	if cred.GraphHost() != DefaultGraphHost || cred.LoginHost() != DefaultLoginHost {
		t.Errorf("default hosts wrong: %+v", cred)
	}
}

func TestResolve_DerivedURLs(t *testing.T) {
	t.Parallel()
	cred, _ := Resolve(ResolveOpts{TenantID: "t-1", ClientID: "c-1", ClientSecret: "fake-graph-secret"})
	if cred.GraphBaseURL() != "https://graph.microsoft.com/v1.0" {
		t.Errorf("graph base = %q", cred.GraphBaseURL())
	}
	if cred.TokenURL() != "https://login.microsoftonline.com/t-1/oauth2/v2.0/token" {
		t.Errorf("token url = %q", cred.TokenURL())
	}
	if cred.Scope() != "https://graph.microsoft.com/.default" {
		t.Errorf("scope = %q", cred.Scope())
	}
}

func TestResolve_SovereignHostOverride(t *testing.T) {
	t.Parallel()
	cred, _ := Resolve(ResolveOpts{TenantID: "t", ClientID: "c", ClientSecret: "fake-graph-secret", GraphHost: "graph.microsoft.us", LoginHost: "login.microsoftonline.us"})
	if cred.GraphHost() != "graph.microsoft.us" || cred.LoginHost() != "login.microsoftonline.us" {
		t.Errorf("override not applied: %+v", cred)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(EnvTenantID, "env-tenant")
	t.Setenv(EnvClientID, "env-client")
	t.Setenv(EnvClientSecret, "fake-env-secret")
	cred, err := Resolve(ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.TenantID() != "env-tenant" || cred.ClientID() != "env-client" {
		t.Errorf("env not read: %+v", cred)
	}
}

func TestResolve_MissingFields(t *testing.T) {
	t.Setenv(EnvTenantID, "")
	t.Setenv(EnvClientID, "")
	t.Setenv(EnvClientSecret, "")
	cases := []struct {
		name string
		opts ResolveOpts
		want string
	}{
		{"no tenant", ResolveOpts{}, "tenant id"},
		{"no client id", ResolveOpts{TenantID: "t"}, "client id"},
		{"no secret", ResolveOpts{TenantID: "t", ClientID: "c"}, "client secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Resolve(c.opts)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q; got %v", c.want, err)
			}
		})
	}
}

func TestCredential_StringRedactsSecret(t *testing.T) {
	t.Parallel()
	const secret = "fake-graph-secret-no-log"
	cred, _ := Resolve(ResolveOpts{TenantID: "t", ClientID: "c", ClientSecret: secret})
	if strings.Contains(cred.String(), secret) || strings.Contains(cred.GoString(), secret) {
		t.Fatal("String/GoString leaks the client secret — AC-11 violation")
	}
	if !strings.Contains(cred.String(), "redacted") {
		t.Errorf("String should mark redaction: %q", cred.String())
	}
}

func TestRequiredPermission_IsReadOnly(t *testing.T) {
	t.Parallel()
	lower := strings.ToLower(RequiredPermission)
	if !strings.Contains(lower, "read.all") {
		t.Errorf("RequiredPermission should be a Read.All permission: %q", RequiredPermission)
	}
	// The Graph permission name legitimately contains "ManagedDevices"; the
	// write-grant markers we must NOT see are the ReadWrite / PrivilegedOperations
	// scopes (P0-490-2).
	for _, banned := range []string{"readwrite", "privilegedoperations"} {
		if strings.Contains(lower, banned) {
			t.Errorf("RequiredPermission must not grant %q: %q (P0-490-2)", banned, RequiredPermission)
		}
	}
}

func TestResolveClientState_FromOpt(t *testing.T) {
	t.Parallel()
	got, err := ResolveClientState("test-client-state-value")
	if err != nil {
		t.Fatalf("ResolveClientState: %v", err)
	}
	if got != "test-client-state-value" {
		t.Errorf("clientState = %q", got)
	}
}

func TestResolveClientState_FromEnv(t *testing.T) {
	t.Setenv(EnvClientState, "test-env-client-state")
	got, err := ResolveClientState("")
	if err != nil {
		t.Fatalf("ResolveClientState: %v", err)
	}
	if got != "test-env-client-state" {
		t.Errorf("clientState = %q", got)
	}
}

func TestResolveClientState_MissingErrors(t *testing.T) {
	t.Setenv(EnvClientState, "")
	if _, err := ResolveClientState(""); err == nil {
		t.Fatal("want error when clientState unset")
	}
}
