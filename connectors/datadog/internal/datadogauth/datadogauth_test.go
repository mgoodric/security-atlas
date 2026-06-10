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

// TestRequiredSIEMScope_IsReadOnly pins the slice-533 SIEM scope to the
// least-privilege read-only value (no write/admin).
func TestRequiredSIEMScope_IsReadOnly(t *testing.T) {
	t.Parallel()
	if RequiredSIEMScope != "security_monitoring_rules_read" {
		t.Errorf("RequiredSIEMScope = %q; want security_monitoring_rules_read (read-only)", RequiredSIEMScope)
	}
	if strings.Contains(RequiredSIEMScope, "write") || strings.Contains(RequiredSIEMScope, "admin") {
		t.Errorf("RequiredSIEMScope must not grant write/admin: %q", RequiredSIEMScope)
	}
}

// TestRequiredSignalScope_IsReadOnly pins the slice-636 signal-history scope to
// the least-privilege read-only value (no write/triage/admin).
func TestRequiredSignalScope_IsReadOnly(t *testing.T) {
	t.Parallel()
	if RequiredSignalScope != "security_monitoring_signals_read" {
		t.Errorf("RequiredSignalScope = %q; want security_monitoring_signals_read (read-only)", RequiredSignalScope)
	}
	if strings.Contains(RequiredSignalScope, "write") || strings.Contains(RequiredSignalScope, "admin") {
		t.Errorf("RequiredSignalScope must not grant write/admin: %q", RequiredSignalScope)
	}
}

// TestRequiredScopes_FullReadOnlySet exercises the RequiredScopes helper: it
// returns exactly the three read-only scopes the connector needs across every
// evidence surface, in order, and never a write/admin scope.
func TestRequiredScopes_FullReadOnlySet(t *testing.T) {
	t.Parallel()
	got := RequiredScopes()
	want := []string{"monitors_read", "security_monitoring_rules_read", "security_monitoring_signals_read"}
	if len(got) != len(want) {
		t.Fatalf("RequiredScopes() = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RequiredScopes()[%d] = %q; want %q", i, got[i], want[i])
		}
	}
	for _, s := range got {
		if strings.Contains(s, "write") || strings.Contains(s, "admin") {
			t.Errorf("RequiredScopes must not include a write/admin scope: %q", s)
		}
		if !strings.HasSuffix(s, "_read") {
			t.Errorf("scope %q is not a read-only (_read-suffixed) scope", s)
		}
	}
	// The helper composes the three exported constants.
	if got[0] != RequiredScope || got[1] != RequiredSIEMScope || got[2] != RequiredSignalScope {
		t.Errorf("RequiredScopes() must compose RequiredScope + RequiredSIEMScope + RequiredSignalScope; got %v", got)
	}
}
