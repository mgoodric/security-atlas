// Package jamfauth resolves the Jamf Pro source credential for the connector
// and documents the least-privilege read-only scope it requires.
//
// Jamf Pro authenticates API calls with an OAuth client_credentials grant: an
// API client (a client id + client secret created under Settings > API Roles
// and Clients) exchanges its credentials for a short-lived bearer token. The
// API client must be bound to an API ROLE that grants ONLY read privileges on
// computer/mobile inventory (the "Read Computers" / "Read Mobile Devices"
// privileges). A management/write privilege is a remote-wipe / config-push risk
// (P0-490-2 / threat-model E) and must NEVER be granted.
//
// The client id + secret are read from the environment, never a CLI flag (so
// they never land in shell history), and neither is ever logged or placed into
// an evidence record (P0-490-4 / AC-11): the resolved Credential redacts the
// secret on every format path.
package jamfauth

import (
	"fmt"
	"os"
	"strings"
)

// Env var names carrying the Jamf Pro credential. Preferred over flags so the
// secret never appears in shell history.
const (
	// EnvBaseURL is the Jamf Pro instance base URL (e.g.
	// https://yourorg.jamfcloud.com). Non-secret.
	EnvBaseURL = "JAMF_BASE_URL"
	// EnvClientID is the Jamf Pro API client id.
	EnvClientID = "JAMF_CLIENT_ID"
	// EnvClientSecret is the Jamf Pro API client secret.
	EnvClientSecret = "JAMF_CLIENT_SECRET"

	// RequiredRole is the documented least-privilege API-role privilege set the
	// connector needs. Read-only inventory access only; no management/write
	// privilege.
	RequiredRole = "Read Computers, Read Mobile Devices (read-only inventory privileges)"
)

// Credential is the resolved Jamf Pro auth material. The client secret is kept
// off String() so accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	baseURL      string
	clientID     string
	clientSecret string
}

// BaseURL returns the Jamf Pro instance base URL. Non-secret.
func (c Credential) BaseURL() string { return c.baseURL }

// ClientID returns the Jamf Pro API client id. Non-secret (an identifier), but
// kept off String() alongside the secret for caution.
func (c Credential) ClientID() string { return c.clientID }

// ClientSecret returns the Jamf Pro API client secret. Callers exchange it for
// a bearer token; it must never be logged.
func (c Credential) ClientSecret() string { return c.clientSecret }

// String never reveals the secret. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("jamfauth.Credential{base_url: %q, client_id: <redacted %d bytes>, client_secret: <redacted %d bytes>}",
		c.baseURL, len(c.clientID), len(c.clientSecret))
}

// GoString mirrors String so %#v cannot leak the secret either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its values through
// this so the package never imports cobra.
type ResolveOpts struct {
	// BaseURL overrides the instance URL. Empty falls back to JAMF_BASE_URL.
	BaseURL string
	// ClientID overrides the client id. Empty falls back to JAMF_CLIENT_ID.
	ClientID string
	// ClientSecret overrides the client secret. Empty falls back to
	// JAMF_CLIENT_SECRET.
	ClientSecret string
}

// Resolve returns a live credential. The base URL, client id, and client secret
// are all required (after env fallback).
func Resolve(opts ResolveOpts) (Credential, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(firstNonEmpty(opts.BaseURL, os.Getenv(EnvBaseURL))), "/")
	if baseURL == "" {
		return Credential{}, fmt.Errorf("jamfauth: base URL required (set %s)", EnvBaseURL)
	}
	clientID := strings.TrimSpace(firstNonEmpty(opts.ClientID, os.Getenv(EnvClientID)))
	if clientID == "" {
		return Credential{}, fmt.Errorf("jamfauth: client id required (set %s)", EnvClientID)
	}
	clientSecret := strings.TrimSpace(firstNonEmpty(opts.ClientSecret, os.Getenv(EnvClientSecret)))
	if clientSecret == "" {
		return Credential{}, fmt.Errorf("jamfauth: client secret required (set %s, bound to a read-only API role)", EnvClientSecret)
	}
	return Credential{baseURL: baseURL, clientID: clientID, clientSecret: clientSecret}, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
