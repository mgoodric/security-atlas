package jamfauth

import (
	"strings"
	"testing"
)

func TestResolve_FromOpts(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{BaseURL: "https://org.jamfcloud.com/", ClientID: "client-1", ClientSecret: "fake-jamf-secret"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != "https://org.jamfcloud.com" {
		t.Errorf("base url not trimmed: %q", cred.BaseURL())
	}
	if cred.ClientID() != "client-1" || cred.ClientSecret() != "fake-jamf-secret" {
		t.Errorf("creds wrong: %+v", cred)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://env.jamfcloud.com")
	t.Setenv(EnvClientID, "env-client")
	t.Setenv(EnvClientSecret, "fake-env-secret")
	cred, err := Resolve(ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != "https://env.jamfcloud.com" || cred.ClientID() != "env-client" {
		t.Errorf("env not read: %+v", cred)
	}
}

func TestResolve_MissingFields(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvClientID, "")
	t.Setenv(EnvClientSecret, "")
	cases := []struct {
		name string
		opts ResolveOpts
		want string
	}{
		{"no base url", ResolveOpts{}, "base URL"},
		{"no client id", ResolveOpts{BaseURL: "https://x"}, "client id"},
		{"no secret", ResolveOpts{BaseURL: "https://x", ClientID: "c"}, "client secret"},
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
	const secret = "fake-jamf-secret-no-log"
	cred, _ := Resolve(ResolveOpts{BaseURL: "https://x", ClientID: "c", ClientSecret: secret})
	if strings.Contains(cred.String(), secret) || strings.Contains(cred.GoString(), secret) {
		t.Fatal("String/GoString leaks the client secret — AC-11 violation")
	}
	if !strings.Contains(cred.String(), "redacted") {
		t.Errorf("String should mark redaction: %q", cred.String())
	}
}

func TestRequiredRole_IsReadOnly(t *testing.T) {
	t.Parallel()
	lower := strings.ToLower(RequiredRole)
	if !strings.Contains(lower, "read") {
		t.Errorf("RequiredRole should be read-only: %q", RequiredRole)
	}
	for _, banned := range []string{"write", "delete", "remote wipe", "management", "privileged"} {
		if strings.Contains(lower, banned) {
			t.Errorf("RequiredRole must not grant %q: %q (P0-490-2)", banned, RequiredRole)
		}
	}
}
