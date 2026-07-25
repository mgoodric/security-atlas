// Package bamboohrauth resolves the BambooHR source credential for the connector
// and documents the least-privilege read-only scope it requires.
//
// BambooHR authenticates API calls with an API key sent as the HTTP Basic
// username (any non-empty password). The API key is minted per-user under the
// BambooHR account; its data access is bounded by that user's permission level.
// The connector REQUIRES that the key belong to a user whose role grants
// READ-ONLY access to the worker-directory / employment-status fields ONLY — the
// minimum needed to read the roster. A role that can see compensation, SSN,
// bank, benefits, home address, or performance, or that can EDIT employees, is
// an over-collection / elevation risk (P0-491-2 / P0-491-3 / threat-model E) and
// must NEVER be used.
//
// The API key + the company subdomain are read from the environment, never a CLI
// flag (so they never land in shell history). The key is never logged or placed
// into an evidence record (P0-491-4 / AC-11): the resolved Credential redacts the
// key on every format path.
package bamboohrauth

import (
	"fmt"
	"os"
	"strings"
)

// Env var names carrying the BambooHR credential. Preferred over flags so the
// secret never appears in shell history.
const (
	// EnvAPIKey is the BambooHR API key (sent as the HTTP Basic username). Secret.
	EnvAPIKey = "BAMBOOHR_API_KEY"
	// EnvCompanyDomain is the BambooHR company subdomain (the {company} path
	// segment, e.g. "acme" for acme.bamboohr.com). Non-secret.
	EnvCompanyDomain = "BAMBOOHR_COMPANY_DOMAIN"
	// EnvBaseURL optionally overrides the BambooHR API base URL. Non-secret.
	EnvBaseURL = "BAMBOOHR_BASE_URL"

	// EnvWebhookSecret is the per-monitor shared secret BambooHR signs webhook
	// deliveries with (HMAC-SHA256 over the raw body). Secret. Required only for
	// the event-driven `subscribe` profile (slice 573).
	EnvWebhookSecret = "BAMBOOHR_WEBHOOK_SECRET"

	// DefaultBaseURL is the BambooHR REST API base.
	DefaultBaseURL = "https://api.bamboohr.com"

	// RequiredScope is the documented least-privilege scope the connector needs:
	// an API key belonging to a read-only worker-directory role only.
	RequiredScope = "API key for a READ-ONLY worker-directory role (roster + employment status); NEVER a role that can see compensation/SSN/bank/benefits/performance or EDIT employees"
)

// Credential is the resolved BambooHR auth material. The API key is kept off
// String() so accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	baseURL       string
	companyDomain string
	apiKey        string
}

// BaseURL returns the BambooHR API base URL. Non-secret.
func (c Credential) BaseURL() string { return c.baseURL }

// CompanyDomain returns the BambooHR company subdomain. Non-secret.
func (c Credential) CompanyDomain() string { return c.companyDomain }

// APIKey returns the BambooHR API key. Callers send it as the HTTP Basic
// username; it must never be logged.
func (c Credential) APIKey() string { return c.apiKey }

// String never reveals the key. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("bamboohrauth.Credential{base_url: %q, company_domain: %q, api_key: <redacted %d bytes>}",
		c.baseURL, c.companyDomain, len(c.apiKey))
}

// GoString mirrors String so %#v cannot leak the key either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its values through
// this so the package never imports cobra.
type ResolveOpts struct {
	// BaseURL overrides the API base. Empty falls back to BAMBOOHR_BASE_URL, then
	// DefaultBaseURL.
	BaseURL string
	// CompanyDomain overrides the subdomain. Empty falls back to
	// BAMBOOHR_COMPANY_DOMAIN.
	CompanyDomain string
	// APIKey overrides the key. Empty falls back to BAMBOOHR_API_KEY.
	APIKey string
}

// Resolve returns a live credential. The API key + company domain are required
// (after env fallback); the base URL defaults to the BambooHR production API.
func Resolve(opts ResolveOpts) (Credential, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(firstNonEmpty(opts.BaseURL, os.Getenv(EnvBaseURL), DefaultBaseURL)), "/")
	companyDomain := strings.TrimSpace(firstNonEmpty(opts.CompanyDomain, os.Getenv(EnvCompanyDomain)))
	if companyDomain == "" {
		return Credential{}, fmt.Errorf("bamboohrauth: company domain required (set %s)", EnvCompanyDomain)
	}
	apiKey := strings.TrimSpace(firstNonEmpty(opts.APIKey, os.Getenv(EnvAPIKey)))
	if apiKey == "" {
		return Credential{}, fmt.Errorf("bamboohrauth: API key required (set %s, for a read-only worker-directory role)", EnvAPIKey)
	}
	return Credential{baseURL: baseURL, companyDomain: companyDomain, apiKey: apiKey}, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
