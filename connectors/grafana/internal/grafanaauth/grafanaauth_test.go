package grafanaauth

import (
	"strings"
	"testing"
)

func TestResolve_HappyPath(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{BaseURL: "https://grafana.example.com/", Token: "test-grafana-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token() != "test-grafana-token" {
		t.Error("token not preserved")
	}
	if cred.BaseURL() != "https://grafana.example.com" {
		t.Errorf("baseURL = %q (trailing slash should be trimmed)", cred.BaseURL())
	}
}

func TestResolve_MissingBaseURL(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	if _, err := Resolve(ResolveOpts{Token: "t"}); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("want base URL error; got %v", err)
	}
}

func TestResolve_MissingToken(t *testing.T) {
	t.Setenv(EnvToken, "")
	if _, err := Resolve(ResolveOpts{BaseURL: "https://g"}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("want token error; got %v", err)
	}
}

func TestResolve_FromEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://env-grafana")
	t.Setenv(EnvToken, "test-env-token")
	cred, err := Resolve(ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != "https://env-grafana" || cred.Token() != "test-env-token" {
		t.Errorf("env fallback failed: %+v", cred)
	}
}

// TestCredential_NeverLeaksToken pins P0-488-4 / AC-11.
func TestCredential_NeverLeaksToken(t *testing.T) {
	t.Parallel()
	const token = "test-grafana-token-never-log"
	cred, err := Resolve(ResolveOpts{BaseURL: "https://g", Token: token})
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

func TestRequiredRole_IsViewer(t *testing.T) {
	t.Parallel()
	if RequiredRole != "Viewer" {
		t.Errorf("RequiredRole = %q; want Viewer (read-only, P0-488-2)", RequiredRole)
	}
}

// TestRequiredAccessConfigScopes pins the slice-534 access-config scope set: the
// precise additional READ permissions beyond Viewer, and that none of them is a
// write/admin action (P0-534 / threat-model E — read-only least privilege; never
// grant Admin "to be safe").
func TestRequiredAccessConfigScopes(t *testing.T) {
	t.Parallel()
	scopes := RequiredAccessConfigScopes()
	if len(scopes) != 2 {
		t.Fatalf("RequiredAccessConfigScopes len = %d; want 2 (sso-settings + access-control)", len(scopes))
	}
	if scopes[0] != SSOSettingsReadPermission {
		t.Errorf("scopes[0] = %q; want SSOSettingsReadPermission", scopes[0])
	}
	if scopes[1] != AccessControlReadPermission {
		t.Errorf("scopes[1] = %q; want AccessControlReadPermission", scopes[1])
	}
	// Every documented permission must be read-only — never a write/admin action.
	bannedActions := []string{"settings:write", "roles:write", "users.roles:add",
		"users.roles:remove", "teams.roles:add", ":write", "admin", "Admin"}
	for _, s := range scopes {
		// The fixed-role names embed "read"; assert the read intent is present and
		// no write/admin action leaked into the documented minimum.
		if !strings.Contains(s, "read") {
			t.Errorf("documented scope %q is not a read permission", s)
		}
		for _, b := range bannedActions {
			if strings.Contains(s, b) {
				t.Errorf("documented scope %q contains a non-read action %q (over-privilege)", s, b)
			}
		}
	}
}
