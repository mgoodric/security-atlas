// Package datadogauth resolves the Datadog source credential for the connector
// and documents the least-privilege read-only scope it requires.
//
// Datadog authenticates API calls with a PAIR of keys: an API key (DD-API-KEY)
// and an Application key (DD-APPLICATION-KEY). The Application key carries the
// authorization scopes; the connector requires ONLY the read-only
// `monitors_read` scope (P0-488-2 / threat-model E). Both keys are read from
// the environment, never a CLI flag (so they never land in shell history), and
// neither is ever logged or placed into an evidence record (P0-488-4 / AC-11):
// the resolved Credential redacts both keys on every format path.
package datadogauth

import (
	"fmt"
	"os"
	"strings"
)

// Env var names carrying the Datadog credential. Preferred over flags so the
// keys never appear in shell history.
const (
	// EnvAPIKey is the Datadog API key (DD-API-KEY header).
	EnvAPIKey = "DATADOG_API_KEY"
	// EnvAppKey is the Datadog Application key (DD-APPLICATION-KEY header),
	// scoped read-only (monitors_read).
	EnvAppKey = "DATADOG_APP_KEY"
	// EnvSite is the Datadog site (e.g. datadoghq.com, datadoghq.eu,
	// us3.datadoghq.com). Defaults to datadoghq.com.
	EnvSite = "DATADOG_SITE"

	// DefaultSite is the US1 site, used when EnvSite is unset.
	DefaultSite = "datadoghq.com"

	// RequiredScope is the read-only Application-key scope the monitor-inventory
	// surface (monitoring.alert_config.v1) needs. Documented as the minimum; the
	// connector issues only monitor reads against /api/v1/monitor.
	RequiredScope = "monitors_read"

	// RequiredSIEMScope is the read-only Application-key scope the Cloud-SIEM
	// detection-rule surface (datadog.siem_rule.v1, slice 533) needs. The
	// connector issues only GETs against /api/v2/security_monitoring/rules.
	RequiredSIEMScope = "security_monitoring_rules_read"
)

// RequiredScopes is the full least-privilege read-only scope set the connector
// requires across both evidence surfaces. NEVER grant a write/admin scope.
func RequiredScopes() []string {
	return []string{RequiredScope, RequiredSIEMScope}
}

// Credential is the resolved Datadog auth material. Both keys are kept off
// String() so accidental %v / %+v formatting paths cannot leak them.
type Credential struct {
	apiKey string
	appKey string
	site   string
}

// APIKey returns the Datadog API key. Callers pass it to the DD-API-KEY header;
// it must never be logged.
func (c Credential) APIKey() string { return c.apiKey }

// AppKey returns the Datadog Application key. Callers pass it to the
// DD-APPLICATION-KEY header; it must never be logged.
func (c Credential) AppKey() string { return c.appKey }

// Site returns the Datadog site host. Non-secret.
func (c Credential) Site() string { return c.site }

// BaseURL returns the API base URL for the resolved site.
func (c Credential) BaseURL() string { return "https://api." + c.site }

// String never reveals the keys. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("datadogauth.Credential{site: %q, api_key: <redacted %d bytes>, app_key: <redacted %d bytes>}",
		c.site, len(c.apiKey), len(c.appKey))
}

// GoString mirrors String so %#v cannot leak the keys either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its values through
// this so the package never imports cobra.
type ResolveOpts struct {
	// APIKey overrides the API key. Empty falls back to DATADOG_API_KEY.
	APIKey string
	// AppKey overrides the Application key. Empty falls back to DATADOG_APP_KEY.
	AppKey string
	// Site overrides the Datadog site. Empty falls back to DATADOG_SITE, then
	// DefaultSite.
	Site string
}

// Resolve returns a live credential. Both keys are required (after env
// fallback). The site defaults to datadoghq.com.
func Resolve(opts ResolveOpts) (Credential, error) {
	apiKey := strings.TrimSpace(firstNonEmpty(opts.APIKey, os.Getenv(EnvAPIKey)))
	if apiKey == "" {
		return Credential{}, fmt.Errorf("datadogauth: API key required (set %s)", EnvAPIKey)
	}
	appKey := strings.TrimSpace(firstNonEmpty(opts.AppKey, os.Getenv(EnvAppKey)))
	if appKey == "" {
		return Credential{}, fmt.Errorf("datadogauth: Application key required (set %s, scoped %s)", EnvAppKey, RequiredScope)
	}
	site := strings.TrimSpace(firstNonEmpty(opts.Site, os.Getenv(EnvSite), DefaultSite))
	return Credential{apiKey: apiKey, appKey: appKey, site: site}, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
