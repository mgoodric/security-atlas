// Package jiraauth resolves Jira and Linear API credentials for the
// connector. Two paths, one Credential type so the cmd layer treats
// them uniformly:
//
//   - Jira Cloud: email + API token, HTTP Basic auth on every request.
//     Email is the Atlassian account that minted the token; the token is
//     a personal API token (https://id.atlassian.com/manage-profile/
//     security/api-tokens). Required to access /rest/api/3/search.
//   - Linear: API key only, sent as the Authorization header (no Bearer
//     prefix, per Linear API docs). Sourced via the workspace settings.
//
// Anti-criterion P0: no log line in this package — or anywhere
// downstream of Resolve* — may emit token, key, or email. Credential
// values are kept off String() to prevent accidental log leakage.
// Tests pin this with TestCredential_StringRedacts_*.
package jiraauth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Platform discriminates which API family the credential targets.
type Platform string

const (
	PlatformJira   Platform = "jira"
	PlatformLinear Platform = "linear"
)

// Env vars for credential lookup. The CLI also accepts flags, but env
// is the recommended path so secrets never appear in shell history.
const (
	EnvJiraEmail = "JIRA_EMAIL"
	EnvJiraToken = "JIRA_API_TOKEN"
	EnvLinearKey = "LINEAR_API_KEY"
)

// Credential is the resolved auth material plus a request-applicator.
// Concrete token / key / email values are kept off String() to prevent
// accidental log leakage.
type Credential struct {
	Platform Platform
	// secret carries the live key (Linear) or the base64 of "email:token"
	// (Jira). Never logged.
	secret string
	// emailLen records the original email length so String can quote a
	// redaction width without ever exposing the value.
	emailLen int
}

// String never reveals the secret or email. Tests rely on this.
func (c Credential) String() string {
	if c.Platform == PlatformJira {
		return fmt.Sprintf("jiraauth.Credential{Platform: %s, email: <redacted %d bytes>, secret: <redacted %d bytes>}",
			c.Platform, c.emailLen, len(c.secret))
	}
	return fmt.Sprintf("jiraauth.Credential{Platform: %s, secret: <redacted %d bytes>}",
		c.Platform, len(c.secret))
}

// Apply sets the Authorization header on req. Mutates in place.
// Use the *http.Request returned from http.NewRequestWithContext.
func (c Credential) Apply(req *http.Request) {
	if c.secret == "" {
		return
	}
	switch c.Platform {
	case PlatformJira:
		// Already base64("email:token").
		req.Header.Set("Authorization", "Basic "+c.secret)
		if req.Header.Get("Accept") == "" {
			req.Header.Set("Accept", "application/json")
		}
	case PlatformLinear:
		// Linear docs: raw API key in Authorization, no Bearer prefix.
		req.Header.Set("Authorization", c.secret)
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if req.Header.Get("Accept") == "" {
			req.Header.Set("Accept", "application/json")
		}
	}
}

// JiraOpts is the input to ResolveJira. Provided so the cmd layer can
// thread its parsed flags in without this package importing cobra.
type JiraOpts struct {
	Email string
	Token string
}

// LinearOpts is the input to ResolveLinear.
type LinearOpts struct {
	APIKey string
}

// ResolveJira returns a Credential for Jira Cloud. Email + token are
// both required. Empty opts fall back to env.
func ResolveJira(opts JiraOpts) (Credential, error) {
	email := strings.TrimSpace(firstNonEmpty(opts.Email, os.Getenv(EnvJiraEmail)))
	token := strings.TrimSpace(firstNonEmpty(opts.Token, os.Getenv(EnvJiraToken)))
	if email == "" {
		return Credential{}, fmt.Errorf("jiraauth: Jira email required (set %s or pass --jira-email)", EnvJiraEmail)
	}
	if token == "" {
		return Credential{}, fmt.Errorf("jiraauth: Jira API token required (set %s or pass --jira-token)", EnvJiraToken)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(email + ":" + token))
	return Credential{Platform: PlatformJira, secret: encoded, emailLen: len(email)}, nil
}

// ResolveLinear returns a Credential for Linear.
func ResolveLinear(opts LinearOpts) (Credential, error) {
	key := strings.TrimSpace(firstNonEmpty(opts.APIKey, os.Getenv(EnvLinearKey)))
	if key == "" {
		return Credential{}, fmt.Errorf("jiraauth: Linear API key required (set %s or pass --linear-key)", EnvLinearKey)
	}
	return Credential{Platform: PlatformLinear, secret: key}, nil
}

// Scope is the documented least-privilege grant for either platform.
type Scope struct {
	Platform Platform
	Name     string
	Access   string
	Gates    string
}

// DocumentedScopes returns the human-readable list of least-privilege
// scopes the connector requires across both platforms. The cmd help
// text and README both render this; keeping it programmatic lets tests
// pin doc + README in sync.
//
// Anti-criterion: this list must never include admin/write/delete
// scopes. The unit test enforces it.
func DocumentedScopes() []Scope {
	return []Scope{
		// Jira Cloud: API tokens inherit the minting user's permissions
		// rather than declaring OAuth scopes. The connector requires
		// "Browse projects" on every project it pulls from; that grants
		// read access to issues via /rest/api/3/search. No other Jira
		// permission is required.
		{
			Platform: PlatformJira,
			Name:     "Project permission: Browse projects",
			Access:   "Read",
			Gates:    "jira.ticket_evidence.v1 (Jira issues via /rest/api/3/search)",
		},
		// Linear: read-only API keys can be minted in the workspace
		// settings. The connector requires only the read scope of the
		// key — Linear's role model attaches scopes to the key itself,
		// not per-team, so the operator picks a read-only key.
		{
			Platform: PlatformLinear,
			Name:     "API key: Read-only access",
			Access:   "Read",
			Gates:    "jira.ticket_evidence.v1 (Linear issues via GraphQL `issues` query)",
		},
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
