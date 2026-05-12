package jiraauth_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
)

func TestResolveJira_FromOpts(t *testing.T) {
	t.Setenv(jiraauth.EnvJiraToken, "")
	t.Setenv(jiraauth.EnvJiraEmail, "")
	c, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "abc.token.value"})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	if c.Platform != jiraauth.PlatformJira {
		t.Fatalf("Platform = %q; want jira", c.Platform)
	}
}

func TestResolveJira_FromEnv(t *testing.T) {
	t.Setenv(jiraauth.EnvJiraEmail, "ops@example.com")
	t.Setenv(jiraauth.EnvJiraToken, "abc.token.value")
	c, err := jiraauth.ResolveJira(jiraauth.JiraOpts{})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	if c.Platform != jiraauth.PlatformJira {
		t.Fatalf("Platform = %q; want jira", c.Platform)
	}
}

func TestResolveJira_FailsOnMissingToken(t *testing.T) {
	t.Setenv(jiraauth.EnvJiraToken, "")
	t.Setenv(jiraauth.EnvJiraEmail, "")
	if _, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com"}); err == nil {
		t.Fatal("expected error when token missing; got nil")
	}
}

func TestResolveJira_FailsOnMissingEmail(t *testing.T) {
	t.Setenv(jiraauth.EnvJiraToken, "")
	t.Setenv(jiraauth.EnvJiraEmail, "")
	if _, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Token: "abc"}); err == nil {
		t.Fatal("expected error when email missing; got nil")
	}
}

func TestResolveLinear_FromOpts(t *testing.T) {
	t.Setenv(jiraauth.EnvLinearKey, "")
	c, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "lin_api_xyz"})
	if err != nil {
		t.Fatalf("ResolveLinear: %v", err)
	}
	if c.Platform != jiraauth.PlatformLinear {
		t.Fatalf("Platform = %q; want linear", c.Platform)
	}
}

func TestResolveLinear_FromEnv(t *testing.T) {
	t.Setenv(jiraauth.EnvLinearKey, "lin_api_env")
	c, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{})
	if err != nil {
		t.Fatalf("ResolveLinear: %v", err)
	}
	if c.Platform != jiraauth.PlatformLinear {
		t.Fatalf("Platform = %q; want linear", c.Platform)
	}
}

func TestResolveLinear_FailsOnMissingKey(t *testing.T) {
	t.Setenv(jiraauth.EnvLinearKey, "")
	if _, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{}); err == nil {
		t.Fatal("expected error; got nil")
	}
}

func TestApplyJira_AddsBasicAuthAndAccept(t *testing.T) {
	c, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "abc.token.value"})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	// Jira Cloud uses HTTP Basic auth with email:apitoken — verify the
	// header is set and starts with "Basic ".
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("Authorization = %q; want Basic prefix", got)
	}
	// The Authorization header must NOT contain the plaintext token. It's
	// base64 of "email:token". The test confirms the plaintext token does
	// not appear after the "Basic " prefix.
	if strings.Contains(got, "abc.token.value") {
		t.Fatalf("Authorization header leaked plaintext token: %q", got)
	}
	if r.Header.Get("Accept") != "application/json" {
		t.Fatalf("Accept = %q; want application/json", r.Header.Get("Accept"))
	}
}

func TestApplyLinear_AddsBearerlessAuthorization(t *testing.T) {
	c, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "lin_api_apply"})
	if err != nil {
		t.Fatalf("ResolveLinear: %v", err)
	}
	r := httptest.NewRequest("POST", "http://example/", nil)
	c.Apply(r)
	// Linear's Authorization header carries the API key directly, without
	// a Bearer prefix (per Linear API docs).
	if got := r.Header.Get("Authorization"); got != "lin_api_apply" {
		t.Fatalf("Authorization = %q; want raw API key (no Bearer prefix)", got)
	}
	if r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q; want application/json", r.Header.Get("Content-Type"))
	}
}

// Anti-criterion P0: secrets must not appear in any String/format output.
func TestCredential_StringRedacts_Jira(t *testing.T) {
	c, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "jira_secret_token_AAAA"})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	s := c.String()
	if strings.Contains(s, "jira_secret_token_AAAA") {
		t.Fatalf("Credential.String leaked token: %q", s)
	}
	if !strings.Contains(s, "<redacted") {
		t.Fatalf("Credential.String missing redaction marker: %q", s)
	}
	// %v must also redact (fmt should not bypass String via reflection).
	if strings.Contains(fmt.Sprintf("%v", c), "jira_secret_token_AAAA") {
		t.Fatalf("Credential leaked token under %%v formatting")
	}
}

func TestCredential_StringRedacts_Linear(t *testing.T) {
	c, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "linear_secret_key_BBBB"})
	if err != nil {
		t.Fatalf("ResolveLinear: %v", err)
	}
	s := c.String()
	if strings.Contains(s, "linear_secret_key_BBBB") {
		t.Fatalf("Credential.String leaked key: %q", s)
	}
	if !strings.Contains(s, "<redacted") {
		t.Fatalf("Credential.String missing redaction marker: %q", s)
	}
}

func TestCredential_StringRedacts_Email_Jira(t *testing.T) {
	// Email is not as sensitive as a token, but it's still identifying
	// material and shouldn't appear in default String output. Operators
	// who want the email can read .Identity() (a future ergonomic — for
	// now the test pins that String alone never leaks it).
	c, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "sensitive-ops@example.com", Token: "tok"})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	if strings.Contains(c.String(), "sensitive-ops@example.com") {
		t.Fatalf("Credential.String leaked email: %q", c.String())
	}
}

func TestDocumentedScopes_NoWriteOrDeleteAccess(t *testing.T) {
	scopes := jiraauth.DocumentedScopes()
	if len(scopes) == 0 {
		t.Fatal("DocumentedScopes returned empty")
	}
	for _, sc := range scopes {
		access := strings.ToLower(sc.Access)
		for _, banned := range []string{"write", "delete", "admin"} {
			if strings.Contains(access, banned) {
				t.Errorf("scope %q (platform=%q) has Access %q containing banned keyword %q",
					sc.Name, sc.Platform, sc.Access, banned)
			}
		}
		nameLower := strings.ToLower(sc.Name)
		for _, banned := range []string{"manage", "admin", "write:", "delete"} {
			if strings.Contains(nameLower, banned) {
				t.Errorf("scope name %q (platform=%q) contains banned write/admin keyword %q",
					sc.Name, sc.Platform, banned)
			}
		}
	}
}

func TestDocumentedScopes_CoversBothPlatforms(t *testing.T) {
	scopes := jiraauth.DocumentedScopes()
	want := map[jiraauth.Platform]bool{
		jiraauth.PlatformJira:   false,
		jiraauth.PlatformLinear: false,
	}
	for _, s := range scopes {
		if _, ok := want[s.Platform]; ok {
			want[s.Platform] = true
		}
	}
	for p, ok := range want {
		if !ok {
			t.Errorf("no documented scope for platform %q", p)
		}
	}
}

func TestDocumentedScopes_GatesEvidenceKind(t *testing.T) {
	// Every documented scope row should reference the evidence_kind it
	// gates, so the table maps cleanly from "what capability does the
	// connector use?" to "what scope unlocks it?".
	scopes := jiraauth.DocumentedScopes()
	hit := false
	for _, s := range scopes {
		if strings.Contains(s.Gates, "jira.ticket_evidence.v1") {
			hit = true
		}
	}
	if !hit {
		t.Error("no documented scope mentions jira.ticket_evidence.v1 in Gates")
	}
}
