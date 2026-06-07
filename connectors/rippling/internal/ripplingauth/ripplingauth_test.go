package ripplingauth

import (
	"strings"
	"testing"
)

func TestResolve_FromOpts(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{BaseURL: "https://api.rippling.example/", APIToken: "test-rippling-key"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != "https://api.rippling.example" {
		t.Errorf("base URL not trimmed: %q", cred.BaseURL())
	}
	if cred.APIToken() != "test-rippling-key" {
		t.Errorf("token = %q", cred.APIToken())
	}
}

func TestResolve_DefaultBaseURL(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIToken, "")
	cred, err := Resolve(ResolveOpts{APIToken: "test-rippling-key"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != DefaultBaseURL {
		t.Errorf("base URL = %q; want default %q", cred.BaseURL(), DefaultBaseURL)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(EnvAPIToken, "test-rippling-key")
	t.Setenv(EnvBaseURL, "https://env.rippling.example")
	cred, err := Resolve(ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != "https://env.rippling.example" || cred.APIToken() != "test-rippling-key" {
		t.Errorf("env not honored: %+v", cred)
	}
}

func TestResolve_MissingToken(t *testing.T) {
	t.Setenv(EnvAPIToken, "")
	_, err := Resolve(ResolveOpts{})
	if err == nil || !strings.Contains(err.Error(), "API token required") {
		t.Fatalf("want token error; got %v", err)
	}
}

func TestCredential_RedactsToken(t *testing.T) {
	t.Parallel()
	const secret = "test-rippling-key-secret"
	cred, err := Resolve(ResolveOpts{APIToken: secret})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), secret) {
		t.Error("String() leaks the token")
	}
	if strings.Contains(cred.GoString(), secret) {
		t.Error("GoString() leaks the token")
	}
	if !strings.Contains(cred.String(), "redacted") {
		t.Error("String() should mark the token redacted")
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
