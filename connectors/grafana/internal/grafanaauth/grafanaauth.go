// Package grafanaauth resolves the Grafana source credential for the connector
// and documents the least-privilege read-only role it requires.
//
// Grafana authenticates API calls with a service-account token (Bearer). The
// connector requires ONLY the read-only **Viewer** role (P0-488-2 /
// threat-model E): Viewer can list alert rules + contact points without any
// write or admin capability. The token is read from the environment, never a
// CLI flag (so it never lands in shell history), and never logged or placed
// into an evidence record (P0-488-4 / AC-11): the resolved Credential redacts
// the token on every format path.
package grafanaauth

import (
	"fmt"
	"os"
	"strings"
)

// Env var names carrying the Grafana credential. Preferred over flags so the
// token never appears in shell history.
const (
	// EnvBaseURL is the Grafana base URL (e.g. https://grafana.example.com).
	EnvBaseURL = "GRAFANA_URL"
	// EnvToken carries the read-only (Viewer-role) service-account token.
	EnvToken = "GRAFANA_TOKEN"

	// RequiredRole is the single read-only Grafana role the alert-rule + contact-
	// point inventory surface (slice 488) needs.
	RequiredRole = "Viewer"

	// SSOSettingsReadPermission is the PRECISE additional fixed-role permission the
	// access-config surface (slice 534) needs beyond the Viewer baseline:
	// reading `GET /api/v1/sso-settings` requires the `settings:read` action
	// scoped to `settings:auth.*` — carried by Grafana's built-in fixed role
	// `fixed:settings:reader`. This is strictly read-only and is NOT the Admin
	// (org-admin / grafana-admin) basic role. Granting Admin "to be safe" is an
	// over-privilege the connector explicitly warns against (P0-534 / threat-model E).
	SSOSettingsReadPermission = "settings:read (scope settings:auth.*) — fixed:settings:reader"

	// AccessControlReadPermission is the read-only permission needed to enumerate
	// RBAC role assignments via `GET /api/access-control/...`: the
	// `roles:read` + `users.roles:read` + `teams.roles:read` actions, carried by
	// the built-in fixed role `fixed:roles:reader`. Read-only; never a write
	// action (`roles:write`, `users.roles:add`, etc.).
	AccessControlReadPermission = "roles:read + users.roles:read + teams.roles:read — fixed:roles:reader"
)

// RequiredAccessConfigScopes returns the precise least-privilege read-only
// permission set the slice-534 access-config surface requires BEYOND the Viewer
// baseline. The list is documentation-grade: it names the exact fixed-role
// read permissions an operator must grant so they do NOT reach for Admin. NEVER
// grant a write/admin action — read-only is sufficient for every API this
// surface calls.
func RequiredAccessConfigScopes() []string {
	return []string{SSOSettingsReadPermission, AccessControlReadPermission}
}

// Credential is the resolved Grafana auth material. The token is kept off
// String() so accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	baseURL string
	token   string
}

// BaseURL returns the Grafana base URL. Non-secret.
func (c Credential) BaseURL() string { return c.baseURL }

// Token returns the service-account token. Callers pass it to the Authorization
// header; it must never be logged.
func (c Credential) Token() string { return c.token }

// String never reveals the token. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("grafanaauth.Credential{base_url: %q, token: <redacted %d bytes>}",
		c.baseURL, len(c.token))
}

// GoString mirrors String so %#v cannot leak the token either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its values through
// this so the package never imports cobra.
type ResolveOpts struct {
	// BaseURL overrides the Grafana base URL. Empty falls back to GRAFANA_URL.
	BaseURL string
	// Token overrides the service-account token. Empty falls back to
	// GRAFANA_TOKEN.
	Token string
}

// Resolve returns a live credential. Both the base URL and the token are
// required (after env fallback).
func Resolve(opts ResolveOpts) (Credential, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(firstNonEmpty(opts.BaseURL, os.Getenv(EnvBaseURL))), "/")
	if baseURL == "" {
		return Credential{}, fmt.Errorf("grafanaauth: base URL required (set %s)", EnvBaseURL)
	}
	token := strings.TrimSpace(firstNonEmpty(opts.Token, os.Getenv(EnvToken)))
	if token == "" {
		return Credential{}, fmt.Errorf("grafanaauth: service-account token required (set %s, %s role)", EnvToken, RequiredRole)
	}
	return Credential{baseURL: baseURL, token: token}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
