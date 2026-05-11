package githubauth_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
)

func TestResolve_PATFromOpts(t *testing.T) {
	t.Setenv(githubauth.EnvPAT, "")
	c, err := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_xyz"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Mode != githubauth.ModePAT {
		t.Fatalf("Mode = %q; want %q", c.Mode, githubauth.ModePAT)
	}
}

func TestResolve_PATFromEnv(t *testing.T) {
	t.Setenv(githubauth.EnvPAT, "github_pat_env")
	c, err := githubauth.Resolve(githubauth.ResolveOpts{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Mode != githubauth.ModePAT {
		t.Fatalf("Mode = %q; want pat", c.Mode)
	}
}

func TestResolve_FailsOnMissingPAT(t *testing.T) {
	t.Setenv(githubauth.EnvPAT, "")
	if _, err := githubauth.Resolve(githubauth.ResolveOpts{}); err == nil {
		t.Fatal("expected error; got nil")
	}
}

func TestResolve_AppNotWired(t *testing.T) {
	t.Setenv(githubauth.EnvAppID, "12345")
	// Synthetic PEM-shaped fixture. detect-private-key flags real BEGIN
	// PRIVATE KEY markers in source, so we build the string at runtime
	// from harmless parts; the path under test does not parse the key.
	pem := strings.Join([]string{"-----BEGIN", "FAKE-KEY-----", "x", "-----END", "FAKE-KEY-----"}, " ")
	t.Setenv(githubauth.EnvAppPrivateKey, pem)
	_, err := githubauth.Resolve(githubauth.ResolveOpts{PreferAppMode: true})
	if err == nil {
		t.Fatal("expected ErrAppNotWired")
	}
	if !strings.Contains(err.Error(), "App auth not wired") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApply_AddsHeaders(t *testing.T) {
	c, err := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_apply"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r := httptest.NewRequest("GET", "http://example/", nil)
	c.Apply(r)
	if got := r.Header.Get("Authorization"); got != "Bearer github_pat_apply" {
		t.Fatalf("Authorization = %q; want Bearer github_pat_apply", got)
	}
	if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := r.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
		t.Fatalf("X-GitHub-Api-Version = %q", got)
	}
}

// Anti-criterion P0: secrets must not appear in any String/format output.
func TestCredential_StringRedacts(t *testing.T) {
	c, err := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_secret_token_value_AAAA"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	s := c.String()
	if strings.Contains(s, "github_pat_secret_token_value_AAAA") {
		t.Fatalf("Credential.String leaked token: %q", s)
	}
	if !strings.Contains(s, "<redacted") {
		t.Fatalf("Credential.String missing redaction marker: %q", s)
	}
	// Also exercise the %v format verb to confirm fmt does NOT bypass
	// the String() method and reach into the bearer field via reflection.
	if strings.Contains(fmt.Sprintf("%v", c), "github_pat_secret_token_value_AAAA") {
		t.Fatalf("Credential leaked token under %%v formatting")
	}
}

func TestDocumentedScopes_NoWriteOrDeleteAccess(t *testing.T) {
	scopes := githubauth.DocumentedScopes()
	if len(scopes) == 0 {
		t.Fatal("DocumentedScopes returned empty")
	}
	for _, sc := range scopes {
		// Anti-criterion P0: every documented scope must request Read
		// access only. The Name field can legitimately contain the
		// permission family ("Administration", "Members") — the *Access*
		// field is the binary read/write switch that matters. Bench-
		// marking against the Access string lets us catch "Write"-grade
		// requests without flagging GitHub's own permission family name.
		access := strings.ToLower(sc.Access)
		for _, banned := range []string{"write", "delete", "admin"} {
			if strings.Contains(access, banned) {
				t.Errorf("scope %q has Access %q containing banned keyword %q",
					sc.Name, sc.Access, banned)
			}
		}
		// Also reject classic admin:* / repo (full write) scope names.
		nameLower := strings.ToLower(sc.Name)
		for _, banned := range []string{"admin:", "repo (write)", "repo (full)", "delete_repo"} {
			if strings.Contains(nameLower, banned) {
				t.Errorf("scope name %q contains banned classic-PAT scope %q", sc.Name, banned)
			}
		}
	}
}

func TestDocumentedScopes_CoversAllThreeKinds(t *testing.T) {
	scopes := githubauth.DocumentedScopes()
	want := map[string]bool{
		"github.repo_protection.v1": false,
		"github.scim_user.v1":       false,
		"github.audit_event.v1":     false,
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
