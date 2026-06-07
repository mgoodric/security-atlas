package datadogauth

import (
	"strings"
	"testing"
)

func TestResolve_HappyPath(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{APIKey: "test-datadog-api-key", AppKey: "test-datadog-app-key", Site: "datadoghq.eu"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.APIKey() != "test-datadog-api-key" || cred.AppKey() != "test-datadog-app-key" {
		t.Error("keys not preserved")
	}
	if cred.Site() != "datadoghq.eu" {
		t.Errorf("site = %q", cred.Site())
	}
	if cred.BaseURL() != "https://api.datadoghq.eu" {
		t.Errorf("baseURL = %q", cred.BaseURL())
	}
}

func TestResolve_DefaultSite(t *testing.T) {
	t.Setenv(EnvSite, "")
	cred, err := Resolve(ResolveOpts{APIKey: "test-datadog-api-key", AppKey: "test-datadog-app-key"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Site() != DefaultSite {
		t.Errorf("site = %q; want default %q", cred.Site(), DefaultSite)
	}
}

func TestResolve_MissingAPIKey(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	if _, err := Resolve(ResolveOpts{AppKey: "test-datadog-app-key"}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("want API key error; got %v", err)
	}
}

func TestResolve_MissingAppKey(t *testing.T) {
	t.Setenv(EnvAppKey, "")
	if _, err := Resolve(ResolveOpts{APIKey: "test-datadog-api-key"}); err == nil || !strings.Contains(err.Error(), "Application key") {
		t.Fatalf("want Application key error; got %v", err)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(EnvAPIKey, "test-env-api-key")
	t.Setenv(EnvAppKey, "test-env-app-key")
	t.Setenv(EnvSite, "us3.datadoghq.com")
	cred, err := Resolve(ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.APIKey() != "test-env-api-key" || cred.AppKey() != "test-env-app-key" || cred.Site() != "us3.datadoghq.com" {
		t.Errorf("env fallback failed: %+v", cred)
	}
}

// TestCredential_NeverLeaksKeys pins P0-488-4 / AC-11: no formatting path may
// reveal either key.
func TestCredential_NeverLeaksKeys(t *testing.T) {
	t.Parallel()
	const apiKey = "test-datadog-api-key-never-log"
	const appKey = "test-datadog-app-key-never-log"
	cred, err := Resolve(ResolveOpts{APIKey: apiKey, AppKey: appKey})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, s := range []string{cred.String(), cred.GoString()} {
		if strings.Contains(s, apiKey) || strings.Contains(s, appKey) {
			t.Fatalf("formatted credential leaked a key: %q", s)
		}
		if !strings.Contains(s, "redacted") {
			t.Errorf("formatted credential should mark the keys redacted: %q", s)
		}
	}
}

func TestRequiredScope_IsReadOnly(t *testing.T) {
	t.Parallel()
	if RequiredScope != "monitors_read" {
		t.Errorf("RequiredScope = %q; want monitors_read (read-only, P0-488-2)", RequiredScope)
	}
	if strings.Contains(RequiredScope, "write") || strings.Contains(RequiredScope, "admin") {
		t.Errorf("RequiredScope must not grant write/admin: %q", RequiredScope)
	}
}
