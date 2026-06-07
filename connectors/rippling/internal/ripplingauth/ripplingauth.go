// Package ripplingauth resolves the Rippling source credential for the connector
// and documents the least-privilege read-only scope it requires.
//
// Rippling authenticates API calls with a bearer API token (an API key minted
// under the Rippling admin console, scoped to specific read groups). The token
// must be scoped to the READ-ONLY employee-directory / worker-lifecycle field
// group ONLY — the minimum needed to read the roster + employment status. A
// full-PII read group (compensation, SSN, bank, benefits) or any WRITE scope is
// an over-collection / elevation risk (P0-491-2 / P0-491-3 / threat-model E)
// and must NEVER be granted.
//
// The API token is read from the environment, never a CLI flag (so it never
// lands in shell history), and is never logged or placed into an evidence record
// (P0-491-4 / AC-11): the resolved Credential redacts the token on every format
// path.
package ripplingauth

import (
	"fmt"
	"os"
	"strings"
)

// Env var names carrying the Rippling credential. Preferred over flags so the
// secret never appears in shell history.
const (
	// EnvAPIToken is the Rippling API token (bearer). Secret.
	EnvAPIToken = "RIPPLING_API_TOKEN"
	// EnvBaseURL optionally overrides the Rippling API base URL. Non-secret.
	EnvBaseURL = "RIPPLING_BASE_URL"

	// DefaultBaseURL is the Rippling REST API base.
	DefaultBaseURL = "https://api.rippling.com"

	// RequiredScope is the documented least-privilege scope the connector needs:
	// a read-only employee-directory / worker-lifecycle field group only.
	RequiredScope = "read-only employee-directory worker-lifecycle scope (roster + employment status); NEVER a full-PII or write scope"
)

// Credential is the resolved Rippling auth material. The API token is kept off
// String() so accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	baseURL  string
	apiToken string
}

// BaseURL returns the Rippling API base URL. Non-secret.
func (c Credential) BaseURL() string { return c.baseURL }

// APIToken returns the Rippling API token. Callers send it as a bearer; it must
// never be logged.
func (c Credential) APIToken() string { return c.apiToken }

// String never reveals the token. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("ripplingauth.Credential{base_url: %q, api_token: <redacted %d bytes>}",
		c.baseURL, len(c.apiToken))
}

// GoString mirrors String so %#v cannot leak the token either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its values through
// this so the package never imports cobra.
type ResolveOpts struct {
	// BaseURL overrides the API base. Empty falls back to RIPPLING_BASE_URL, then
	// DefaultBaseURL.
	BaseURL string
	// APIToken overrides the token. Empty falls back to RIPPLING_API_TOKEN.
	APIToken string
}

// Resolve returns a live credential. The API token is required (after env
// fallback); the base URL defaults to the Rippling production API.
func Resolve(opts ResolveOpts) (Credential, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(firstNonEmpty(opts.BaseURL, os.Getenv(EnvBaseURL), DefaultBaseURL)), "/")
	apiToken := strings.TrimSpace(firstNonEmpty(opts.APIToken, os.Getenv(EnvAPIToken)))
	if apiToken == "" {
		return Credential{}, fmt.Errorf("ripplingauth: API token required (set %s, scoped read-only to the worker-lifecycle field group)", EnvAPIToken)
	}
	return Credential{baseURL: baseURL, apiToken: apiToken}, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
