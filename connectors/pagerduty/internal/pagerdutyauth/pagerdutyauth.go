// Package pagerdutyauth resolves the PagerDuty source credential for the
// connector and documents the least-privilege read-only scope it requires.
//
// PagerDuty authenticates REST API calls with a single API token sent in the
// Authorization header as "Token token=<token>". The connector requires a
// READ-ONLY token (a "Read-only API key" / a read-only-scoped REST API key)
// — never a full-access or write/admin token (P0-489-2 / threat-model E). The
// token is read from the environment, never a CLI flag (so it never lands in
// shell history), and is never logged or placed into an evidence record
// (P0-489-4 / AC-11): the resolved Credential redacts the token on every
// format path.
package pagerdutyauth

import (
	"fmt"
	"os"
	"strings"
)

const (
	// EnvToken is the PagerDuty REST API token (read-only). Preferred over a
	// flag so the token never appears in shell history.
	EnvToken = "PAGERDUTY_TOKEN"

	// EnvWebhookSecret is the PagerDuty v3 webhook per-subscription SIGNING
	// SECRET used by the `subscribe` profile's receiver to verify the
	// X-PagerDuty-Signature HMAC. Like the token, it is read from the
	// environment (never a CLI flag, so it never lands in shell history) and is
	// never logged or placed into an evidence record (P0-540).
	EnvWebhookSecret = "PAGERDUTY_WEBHOOK_SECRET"

	// BaseURL is the PagerDuty REST API base. PagerDuty's REST API is a single
	// global host (region is a property of the account, not the host).
	BaseURL = "https://api.pagerduty.com"

	// RequiredScope names the least-privilege credential the connector needs.
	// Documented as the minimum; the connector issues only GETs.
	RequiredScope = "read-only"
)

// Credential is the resolved PagerDuty auth material. The token is kept off
// String() so accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	token string
}

// Token returns the PagerDuty API token. Callers pass it to the Authorization
// header; it must never be logged.
func (c Credential) Token() string { return c.token }

// BaseURL returns the PagerDuty REST API base URL. Non-secret.
func (c Credential) BaseURL() string { return BaseURL }

// String never reveals the token. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("pagerdutyauth.Credential{token: <redacted %d bytes>}", len(c.token))
}

// GoString mirrors String so %#v cannot leak the token either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its values
// through this so the package never imports cobra.
type ResolveOpts struct {
	// Token overrides the API token. Empty falls back to PAGERDUTY_TOKEN.
	Token string
}

// Resolve returns a live credential. The token is required (after env
// fallback).
func Resolve(opts ResolveOpts) (Credential, error) {
	token := strings.TrimSpace(firstNonEmpty(opts.Token, os.Getenv(EnvToken)))
	if token == "" {
		return Credential{}, fmt.Errorf("pagerdutyauth: API token required (set %s, scoped %s)", EnvToken, RequiredScope)
	}
	return Credential{token: token}, nil
}

// ResolveWebhookSecret returns the PagerDuty v3 webhook signing secret for the
// `subscribe` profile. It is required (after env fallback). The returned string
// is the raw secret; callers pass it to webhook.NewHMACVerifier and must never
// log it. opt overrides the env var (used by tests); empty falls back to
// PAGERDUTY_WEBHOOK_SECRET.
func ResolveWebhookSecret(opt string) (string, error) {
	secret := strings.TrimSpace(firstNonEmpty(opt, os.Getenv(EnvWebhookSecret)))
	if secret == "" {
		return "", fmt.Errorf("pagerdutyauth: webhook signing secret required (set %s)", EnvWebhookSecret)
	}
	return secret, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
