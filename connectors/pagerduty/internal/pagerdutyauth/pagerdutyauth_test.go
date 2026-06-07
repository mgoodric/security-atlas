package pagerdutyauth

import (
	"strings"
	"testing"
)

func TestResolve_HappyPath(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{Token: "test-pagerduty-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token() != "test-pagerduty-token" {
		t.Errorf("token not preserved: %q", cred.Token())
	}
	if cred.BaseURL() != BaseURL {
		t.Errorf("baseURL = %q; want %q", cred.BaseURL(), BaseURL)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(EnvToken, "test-env-pagerduty-token")
	cred, err := Resolve(ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token() != "test-env-pagerduty-token" {
		t.Errorf("env fallback failed: %q", cred.Token())
	}
}

func TestResolve_MissingToken(t *testing.T) {
	t.Setenv(EnvToken, "")
	if _, err := Resolve(ResolveOpts{}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("want token error; got %v", err)
	}
}

// TestCredential_NeverLeaksToken pins P0-489-4 / AC-11: no formatting path may
// reveal the token.
func TestCredential_NeverLeaksToken(t *testing.T) {
	t.Parallel()
	const token = "test-pagerduty-token-never-log"
	cred, err := Resolve(ResolveOpts{Token: token})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, s := range []string{cred.String(), cred.GoString()} {
		if strings.Contains(s, token) {
			t.Fatalf("formatted credential leaked the token: %q", s)
		}
		if !strings.Contains(s, "redacted") {
			t.Errorf("formatted credential should mark the token redacted: %q", s)
		}
	}
}

func TestRequiredScope_IsReadOnly(t *testing.T) {
	t.Parallel()
	if RequiredScope != "read-only" {
		t.Errorf("RequiredScope = %q; want read-only (P0-489-2)", RequiredScope)
	}
	if strings.Contains(RequiredScope, "write") || strings.Contains(RequiredScope, "admin") {
		t.Errorf("RequiredScope must not grant write/admin: %q", RequiredScope)
	}
}
