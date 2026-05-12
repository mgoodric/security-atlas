package oktaauth_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

func TestResolve_FromOpts(t *testing.T) {
	t.Setenv(oktaauth.EnvAPIToken, "")
	c, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "00abc"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Apply must not error and must attach the SSWS header on a synthetic request.
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Authorization"); got != "SSWS 00abc" {
		t.Fatalf("Authorization = %q; want SSWS 00abc", got)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(oktaauth.EnvAPIToken, "env-token")
	c, err := oktaauth.Resolve(oktaauth.ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Authorization"); got != "SSWS env-token" {
		t.Fatalf("Authorization = %q; want SSWS env-token", got)
	}
}

func TestResolve_FailsOnMissingToken(t *testing.T) {
	t.Setenv(oktaauth.EnvAPIToken, "")
	if _, err := oktaauth.Resolve(oktaauth.ResolveOpts{}); err == nil {
		t.Fatal("expected error; got nil")
	}
}

func TestApply_AddsAcceptHeader(t *testing.T) {
	c, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "abc"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q; want application/json", got)
	}
}

// Anti-criterion P0: secrets must NOT appear in any String / format output.
func TestCredential_StringRedacts(t *testing.T) {
	// Use a benign test-shaped string here, not anything resembling a real
	// Okta token prefix — GitGuardian flags Okta-shaped literals in source.
	c, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token-redaction-check"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	s := c.String()
	if strings.Contains(s, "test-token-redaction-check") {
		t.Fatalf("Credential.String leaked token: %q", s)
	}
	if !strings.Contains(s, "<redacted") {
		t.Fatalf("Credential.String missing redaction marker: %q", s)
	}
	// %v must not bypass String() via reflection.
	if strings.Contains(fmt.Sprintf("%v", c), "test-token-redaction-check") {
		t.Fatalf("Credential leaked token under %%v formatting")
	}
	// %+v likewise.
	if strings.Contains(fmt.Sprintf("%+v", c), "test-token-redaction-check") {
		t.Fatalf("Credential leaked token under %%+v formatting")
	}
}

// Anti-criterion P0: every documented scope MUST be Read-only. Width-
// checked against the Access field (the canonical binary read/write
// switch).
func TestDocumentedScopes_NoWriteOrDeleteOrAdminAccess(t *testing.T) {
	scopes := oktaauth.DocumentedScopes()
	if len(scopes) == 0 {
		t.Fatal("DocumentedScopes returned empty")
	}
	for _, s := range scopes {
		access := strings.ToLower(s.Access)
		for _, banned := range []string{"write", "delete", "admin"} {
			if strings.Contains(access, banned) {
				t.Errorf("scope %q has Access %q containing banned keyword %q",
					s.Name, s.Access, banned)
			}
		}
		// Also reject Super Admin / Org Admin etc. in the scope NAME.
		nameLower := strings.ToLower(s.Name)
		for _, banned := range []string{"super", "org admin", ".write", ".delete", ".manage"} {
			if strings.Contains(nameLower, banned) {
				t.Errorf("scope name %q contains banned high-privilege keyword %q", s.Name, banned)
			}
		}
	}
}

// All three evidence kinds must have at least one gating scope.
func TestDocumentedScopes_CoversAllThreeKinds(t *testing.T) {
	scopes := oktaauth.DocumentedScopes()
	want := map[string]bool{
		"okta.mfa_policy.v1":     false,
		"okta.app_assignment.v1": false,
		"okta.user_lifecycle.v1": false,
	}
	for _, s := range scopes {
		for k := range want {
			if strings.Contains(s.Gates, k) {
				want[k] = true
			}
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("no documented scope gates %s", k)
		}
	}
}
