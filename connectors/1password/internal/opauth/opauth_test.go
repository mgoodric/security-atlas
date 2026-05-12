package opauth_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
)

// Test tokens avoid the literal `ops_` prefix used by real 1Password
// Service Account JWTs — GitGuardian's secret scanner flags any source
// string matching that pattern, even in test files. The connector's
// runtime behaviour does not depend on a specific prefix.
func TestResolve_TokenFromOpts(t *testing.T) {
	t.Setenv(opauth.EnvServiceAccountToken, "")
	c, err := opauth.Resolve(opauth.ResolveOpts{Token: "test-service-account-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Mode != opauth.ModeServiceAccount {
		t.Fatalf("Mode = %q; want %q", c.Mode, opauth.ModeServiceAccount)
	}
}

func TestResolve_TokenFromEnv(t *testing.T) {
	t.Setenv(opauth.EnvServiceAccountToken, "test-env-token")
	c, err := opauth.Resolve(opauth.ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Mode != opauth.ModeServiceAccount {
		t.Fatalf("Mode = %q; want service_account", c.Mode)
	}
}

func TestResolve_FailsOnMissingToken(t *testing.T) {
	t.Setenv(opauth.EnvServiceAccountToken, "")
	if _, err := opauth.Resolve(opauth.ResolveOpts{}); err == nil {
		t.Fatal("expected error; got nil")
	}
}

// Anti-criterion P0: Service Account tokens must never appear in any
// String/format output. The credential type holds the bearer privately
// and the String method redacts.
func TestCredential_StringRedacts(t *testing.T) {
	const secretValue = "test-redaction-input-AAAA"
	c, err := opauth.Resolve(opauth.ResolveOpts{Token: secretValue})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	s := c.String()
	if strings.Contains(s, secretValue) {
		t.Fatalf("Credential.String leaked token: %q", s)
	}
	if !strings.Contains(s, "<redacted") {
		t.Fatalf("Credential.String missing redaction marker: %q", s)
	}
	// Exercise the %v format verb to confirm fmt does NOT bypass the
	// String() method and reach the bearer field via reflection.
	if strings.Contains(fmt.Sprintf("%v", c), secretValue) {
		t.Fatalf("Credential leaked token under %%v formatting")
	}
}

func TestApply_AddsHeaders(t *testing.T) {
	c, err := opauth.Resolve(opauth.ResolveOpts{Token: "test-apply-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Authorization"); got != "Bearer test-apply-token" {
		t.Fatalf("Authorization = %q; want Bearer test-apply-token", got)
	}
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q; want application/json", got)
	}
}

// Anti-criterion P0: documented scopes must be read-only and per-vault
// only. Reject any keyword that implies write/manage/admin access.
func TestDocumentedScopes_NoWriteOrManageOrAdmin(t *testing.T) {
	scopes := opauth.DocumentedScopes()
	if len(scopes) == 0 {
		t.Fatal("DocumentedScopes returned empty")
	}
	for _, sc := range scopes {
		access := strings.ToLower(sc.Access)
		for _, banned := range []string{"write", "manage", "admin", "delete"} {
			if strings.Contains(access, banned) {
				t.Errorf("scope %q has Access %q containing banned keyword %q",
					sc.Name, sc.Access, banned)
			}
		}
		nameLower := strings.ToLower(sc.Name)
		for _, banned := range []string{"write_items", "manage_vault", "admin"} {
			if strings.Contains(nameLower, banned) {
				t.Errorf("scope name %q contains banned scope identifier %q", sc.Name, banned)
			}
		}
	}
}

func TestDocumentedScopes_CoversOrgPolicyKind(t *testing.T) {
	scopes := opauth.DocumentedScopes()
	found := false
	for _, s := range scopes {
		if strings.Contains(s.Gates, "1password.org_policy.v1") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no documented scope gates 1password.org_policy.v1")
	}
}
