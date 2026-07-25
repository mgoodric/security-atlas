package bamboohrauth

import (
	"strings"
	"testing"
)

func TestResolve_FromOpts(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{BaseURL: "https://api.bamboohr.example/", CompanyDomain: "acme", APIKey: "fake-bamboo-secret"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != "https://api.bamboohr.example" {
		t.Errorf("base URL not trimmed: %q", cred.BaseURL())
	}
	if cred.CompanyDomain() != "acme" {
		t.Errorf("company domain = %q", cred.CompanyDomain())
	}
	if cred.APIKey() != "fake-bamboo-secret" {
		t.Errorf("key = %q", cred.APIKey())
	}
}

func TestResolve_DefaultBaseURL(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvCompanyDomain, "")
	cred, err := Resolve(ResolveOpts{CompanyDomain: "acme", APIKey: "fake-bamboo-secret"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != DefaultBaseURL {
		t.Errorf("base URL = %q; want default %q", cred.BaseURL(), DefaultBaseURL)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(EnvAPIKey, "fake-bamboo-secret")
	t.Setenv(EnvCompanyDomain, "acme-env")
	cred, err := Resolve(ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.CompanyDomain() != "acme-env" || cred.APIKey() != "fake-bamboo-secret" {
		t.Errorf("env not honored: %+v", cred)
	}
}

func TestResolve_MissingCompanyDomain(t *testing.T) {
	t.Setenv(EnvCompanyDomain, "")
	t.Setenv(EnvAPIKey, "fake-bamboo-secret")
	_, err := Resolve(ResolveOpts{APIKey: "fake-bamboo-secret"})
	if err == nil || !strings.Contains(err.Error(), "company domain required") {
		t.Fatalf("want company domain error; got %v", err)
	}
}

func TestResolve_MissingAPIKey(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	_, err := Resolve(ResolveOpts{CompanyDomain: "acme"})
	if err == nil || !strings.Contains(err.Error(), "API key required") {
		t.Fatalf("want key error; got %v", err)
	}
}

func TestCredential_RedactsKey(t *testing.T) {
	t.Parallel()
	const secret = "fake-bamboo-secret-value"
	cred, err := Resolve(ResolveOpts{CompanyDomain: "acme", APIKey: secret})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), secret) {
		t.Error("String() leaks the key")
	}
	if strings.Contains(cred.GoString(), secret) {
		t.Error("GoString() leaks the key")
	}
	if !strings.Contains(cred.String(), "redacted") {
		t.Error("String() should mark the key redacted")
	}
}

func TestRequiredScope_IsReadOnly(t *testing.T) {
	t.Parallel()
	lower := strings.ToLower(RequiredScope)
	if !strings.Contains(lower, "read-only") {
		t.Errorf("RequiredScope must be read-only: %q", RequiredScope)
	}
	if !strings.Contains(lower, "never") {
		t.Errorf("RequiredScope must warn against full-PII/write: %q", RequiredScope)
	}
}
