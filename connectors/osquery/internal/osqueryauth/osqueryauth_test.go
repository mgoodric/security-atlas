package osqueryauth_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryauth"
)

func TestResolve_FromOpts(t *testing.T) {
	t.Setenv(osqueryauth.EnvFleetAPIToken, "")
	c, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "fleet-test-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Mode != osqueryauth.ModeFleet {
		t.Fatalf("Mode = %q; want %q", c.Mode, osqueryauth.ModeFleet)
	}
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Authorization"); got != "Bearer fleet-test-token" {
		t.Fatalf("Authorization = %q; want Bearer fleet-test-token", got)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(osqueryauth.EnvFleetAPIToken, "env-fleet-token")
	c, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Authorization"); got != "Bearer env-fleet-token" {
		t.Fatalf("Authorization = %q; want Bearer env-fleet-token", got)
	}
}

func TestResolve_FailsOnMissingToken(t *testing.T) {
	t.Setenv(osqueryauth.EnvFleetAPIToken, "")
	if _, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{}); err == nil {
		t.Fatal("expected error; got nil")
	}
}

func TestResolve_LocalMode(t *testing.T) {
	// Local mode does not require a token — the security boundary is the
	// Unix socket's filesystem permission.
	t.Setenv(osqueryauth.EnvFleetAPIToken, "")
	c, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{PreferLocalMode: true})
	if err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}
	if c.Mode != osqueryauth.ModeLocal {
		t.Fatalf("Mode = %q; want %q", c.Mode, osqueryauth.ModeLocal)
	}
	// Apply must be a no-op in local mode.
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q; want empty in local mode", got)
	}
}

func TestApply_AddsAcceptHeader(t *testing.T) {
	c, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "abc"})
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
	// Test-shaped string, not a real Fleet token prefix.
	c, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "test-token-redaction-check"})
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
	if strings.Contains(fmt.Sprintf("%v", c), "test-token-redaction-check") {
		t.Fatalf("Credential leaked token under %%v formatting")
	}
	if strings.Contains(fmt.Sprintf("%+v", c), "test-token-redaction-check") {
		t.Fatalf("Credential leaked token under %%+v formatting")
	}
}

// Anti-criterion P0: every documented scope MUST be Read-only.
func TestDocumentedScopes_NoWriteOrDeleteOrAdminOrMaintainer(t *testing.T) {
	scopes := osqueryauth.DocumentedScopes()
	if len(scopes) == 0 {
		t.Fatal("DocumentedScopes returned empty")
	}
	for _, s := range scopes {
		access := strings.ToLower(s.Access)
		for _, banned := range []string{"write", "delete", "admin", "maintainer"} {
			if strings.Contains(access, banned) {
				t.Errorf("scope %q has Access %q containing banned keyword %q",
					s.Name, s.Access, banned)
			}
		}
		nameLower := strings.ToLower(s.Name)
		for _, banned := range []string{"admin", "maintainer", ".write", ".delete", ".manage"} {
			if strings.Contains(nameLower, banned) {
				t.Errorf("scope name %q contains banned high-privilege keyword %q", s.Name, banned)
			}
		}
	}
}

// AC-2 evidence_kind must have at least one gating scope documented.
func TestDocumentedScopes_GatesHostPosture(t *testing.T) {
	scopes := osqueryauth.DocumentedScopes()
	found := false
	for _, s := range scopes {
		if strings.Contains(s.Gates, "osquery.host_posture.v1") {
			found = true
		}
	}
	if !found {
		t.Error("no documented scope gates osquery.host_posture.v1")
	}
}
