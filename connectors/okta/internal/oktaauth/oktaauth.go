// Package oktaauth resolves Okta API credentials for the connector.
//
// Okta's API uses a vendor-specific Authorization scheme:
//
//	Authorization: SSWS <api_token>
//
// (Note: SSWS, not Bearer — Okta calls these "API tokens"; documentation
// at developer.okta.com/docs/reference/core-okta-api/.)
//
// The Credential is constructed by Resolve from either the OKTA_API_TOKEN
// env var (preferred so the secret never appears in shell history) or an
// explicit --token flag. Credential.String() never reveals the token —
// fmt.Sprintf("%v", cred) returns a redacted summary so accidental
// log/print paths cannot leak it. The unit test pins this behaviour.
//
// Anti-criterion: no log line in this package — or anywhere downstream of
// Resolve — may emit the API token. DocumentedScopes returns the canonical
// least-privilege Okta admin permissions required; the companion test
// rejects any future widening into write/admin/delete scopes.
package oktaauth

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// EnvAPIToken is the env var carrying the Okta API token. Preferred over
// the --token flag so the secret never appears in shell history.
const EnvAPIToken = "OKTA_API_TOKEN"

// Credential is the resolved auth material plus a header applicator. The
// token value is kept off String() so accidental %v / %+v formatting
// paths cannot leak it.
type Credential struct {
	token string
}

// String never reveals the token. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("oktaauth.Credential{token: <redacted %d bytes>}", len(c.token))
}

// Apply sets Authorization: SSWS <token> on req. Use the *http.Request
// returned from http.NewRequestWithContext; we mutate in place. Also sets
// the Accept and Content-Type headers Okta's API expects.
func (c Credential) Apply(req *http.Request) {
	if c.token == "" {
		return
	}
	req.Header.Set("Authorization", "SSWS "+c.token)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if req.Header.Get("Content-Type") == "" && req.Method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
}

// ResolveOpts is the input to Resolve. The cmd layer threads its parsed
// flags through this so the package never imports cobra.
type ResolveOpts struct {
	// Token lets the caller pass an explicit token. Empty falls back to env.
	Token string
}

// Resolve returns a live credential. Empty token (after env fallback)
// returns a descriptive error. The returned Credential.Apply is the only
// supported path to attach the token to an outgoing request.
func Resolve(opts ResolveOpts) (Credential, error) {
	token := strings.TrimSpace(firstNonEmpty(opts.Token, os.Getenv(EnvAPIToken)))
	if token == "" {
		return Credential{}, fmt.Errorf("oktaauth: API token required (set %s or pass --token)", EnvAPIToken)
	}
	return Credential{token: token}, nil
}

// Scope is the documented least-privilege grant.
type Scope struct {
	Name      string
	Access    string
	Gates     string
	TokenKind string
}

// DocumentedScopes returns the canonical least-privilege Okta admin
// permissions the connector requires. The cmd help text and README both
// render this; keeping it programmatic lets the test pin the doc + the
// README in sync.
//
// Anti-criterion: this list must NEVER include write / delete / admin
// access. The unit test enforces it (write/delete/admin substring in the
// Access field is a hard failure).
//
// Okta API tokens are granted at the admin-role level; the canonical
// "least-privilege" path is the built-in **Read-only Administrator** role
// scoped to the resources the connector reads. The granular permissions
// below mirror what that role grants on the standard Okta admin console.
func DocumentedScopes() []Scope {
	return []Scope{
		{
			Name:      "okta.users.read",
			Access:    "Read",
			Gates:     "okta.user_lifecycle.v1 (GET /api/v1/users + /api/v1/users/{id}/factors)",
			TokenKind: "API token (Read-only Admin role)",
		},
		{
			Name:      "okta.groups.read",
			Access:    "Read",
			Gates:     "okta.app_assignment.v1 (GET /api/v1/apps/{id}/groups)",
			TokenKind: "API token (Read-only Admin role)",
		},
		{
			Name:      "okta.apps.read",
			Access:    "Read",
			Gates:     "okta.app_assignment.v1 (GET /api/v1/apps)",
			TokenKind: "API token (Read-only Admin role)",
		},
		{
			Name:      "okta.policies.read",
			Access:    "Read",
			Gates:     "okta.mfa_policy.v1 (GET /api/v1/policies?type=MFA_ENROLL)",
			TokenKind: "API token (Read-only Admin role)",
		},
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
