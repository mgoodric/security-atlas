// Package opauth resolves 1Password API credentials for the connector.
//
// #2968: the connector reads two documented 1Password APIs, and each has
// its own credential:
//
//   - the Events API, with the bearer token of an Events Reporting
//     integration (www.1password.dev/events-api/authorization.md);
//   - the Users API, with an OAuth application's client ID and client
//     secret, exchanged for a 15-minute access token by the client
//     credentials grant (www.1password.dev/users-api/authorization.md).
//
// All three values come from the environment only. No flag carries a
// secret, so none reaches `ps` or shell history (#2947).
//
// Anti-criterion: no log line in this package — or anywhere downstream of
// Resolve — may emit token material. TestCredentials_StringRedacts pins
// this; %v / %s formatting flows through the String method which only
// reveals byte lengths.
package opauth

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
)

// The environment variables carrying the connector's 1Password secrets.
const (
	EnvEventsToken       = "ONEPASSWORD_EVENTS_TOKEN"
	EnvUsersClientID     = "ONEPASSWORD_USERS_CLIENT_ID"
	EnvUsersClientSecret = "ONEPASSWORD_USERS_CLIENT_SECRET"
)

// Credentials is the resolved auth material. Every value is kept off
// String so accidental %v / %+v formatting cannot leak it.
type Credentials struct {
	eventsToken  string
	clientID     string
	clientSecret string
}

// String never reveals a secret. Tests rely on this.
func (c Credentials) String() string {
	return fmt.Sprintf("opauth.Credentials{events_token: <redacted %d bytes>, users_client_id: <redacted %d bytes>, users_client_secret: <redacted %d bytes>}",
		len(c.eventsToken), len(c.clientID), len(c.clientSecret))
}

// GoString keeps %#v from printing the struct fields.
func (c Credentials) GoString() string { return c.String() }

// ApplyEvents sets the Events Reporting bearer on req.
func (c Credentials) ApplyEvents(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.eventsToken)
	req.Header.Set("Accept", "application/json")
}

// ApplyUsersClient sets HTTP Basic client authentication on a Users API
// token request, the method 1Password's docs recommend.
func (c Credentials) ApplyUsersClient(req *http.Request) {
	req.SetBasicAuth(c.clientID, c.clientSecret)
}

// Resolve reads the three secrets from the environment. Every one is
// required; the error names the missing variables, never a value.
func Resolve() (Credentials, error) {
	c := Credentials{
		eventsToken:  strings.TrimSpace(os.Getenv(EnvEventsToken)),
		clientID:     strings.TrimSpace(os.Getenv(EnvUsersClientID)),
		clientSecret: strings.TrimSpace(os.Getenv(EnvUsersClientSecret)),
	}
	var missing []string
	if c.eventsToken == "" {
		missing = append(missing, EnvEventsToken)
	}
	if c.clientID == "" {
		missing = append(missing, EnvUsersClientID)
	}
	if c.clientSecret == "" {
		missing = append(missing, EnvUsersClientSecret)
	}
	if len(missing) > 0 {
		return Credentials{}, fmt.Errorf("opauth: set %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// ErrUnsafeRedirect is returned for a redirect that changes host or scheme.
var ErrUnsafeRedirect = errors.New("refused redirect")

// CheckRedirect is an http.Client CheckRedirect that refuses a redirect to
// another host (port included) or another scheme, so a request carrying a
// 1Password bearer or client secret is only ever followed on its own
// origin. It stops after 10 redirects, as the default does. Mirrors the
// Confluence (#2947) and Datadog (#2935) connectors' guard.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	orig := via[0].URL
	if !strings.EqualFold(req.URL.Host, orig.Host) {
		return fmt.Errorf("%w: from host %q to %q", ErrUnsafeRedirect, orig.Host, req.URL.Host)
	}
	if req.URL.Scheme != orig.Scheme {
		return fmt.Errorf("%w: from %s to %s", ErrUnsafeRedirect, orig.Scheme, req.URL.Scheme)
	}
	return nil
}

// The documented regional hosts of each API: 1password.com, .ca, .eu and
// ent.1password.com (www.1password.dev/events-api/reference/introspect.md
// servers block; users-api/authorization.md "Authorization endpoints").
var (
	EventsHosts = []string{"events.1password.com", "events.1password.ca", "events.1password.eu", "events.ent.1password.com"}
	UsersHosts  = []string{"api.1password.com", "api.1password.ca", "api.1password.eu", "api.ent.1password.com"}
)

// CheckEventsBaseURL refuses an Events API base URL that would send the
// Events Reporting bearer anywhere but a documented Events host.
func CheckEventsBaseURL(raw string) error { return checkBaseURL("--events-base-url", raw, EventsHosts) }

// CheckUsersBaseURL refuses a Users API base URL that would send the OAuth
// client secret anywhere but a documented Users API host.
func CheckUsersBaseURL(raw string) error { return checkBaseURL("--users-base-url", raw, UsersHosts) }

// checkBaseURL accepts only https://<host> or https://<host>/ with host an
// exact match from hosts: no userinfo, port, path, query or fragment, so no
// spelling of the flag can move the credential to another origin or onto
// plain http (the same intent as the Datadog connector's CheckSite, #2986).
func checkBaseURL(flag, raw string, hosts []string) error {
	bad := fmt.Errorf("opauth: %s %q is not a 1Password API base URL (one of https://%s)", flag, raw, strings.Join(hosts, ", https://"))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Port() != "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return bad
	}
	if !slices.Contains(hosts, u.Host) {
		return bad
	}
	return nil
}

// Scope is one documented least-privilege grant. The cmd `scopes`
// subcommand and the README both render this list; keeping it
// programmatic lets the test pin the doc + the README in sync.
type Scope struct {
	Name      string
	Access    string
	Gates     string
	TokenKind string
}

// DocumentedScopes returns the grants this connector needs, named as
// 1Password names them. Anti-criterion: any grant containing a
// write/manage/admin/suspend keyword fails
// TestDocumentedScopes_NoWriteOrManageOrAdmin.
func DocumentedScopes() []Scope {
	return []Scope{
		{
			Name:      "auditevents",
			Access:    "Read",
			Gates:     "1password.org_policy.v2 (org_id, audit_events_enabled via GET /api/v2/auth/introspect)",
			TokenKind: "Events Reporting integration",
		},
		{
			Name:      "users.view",
			Access:    "Read",
			Gates:     "1password.org_policy.v2 (active_members, suspended_members via GET /v1/accounts/{account}/users)",
			TokenKind: "Users API OAuth application",
		},
	}
}
