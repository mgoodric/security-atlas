// Package intuneauth resolves the Microsoft Intune / Graph source credential
// for the connector and documents the least-privilege read-only Graph
// permission it requires.
//
// Intune is managed via Microsoft Graph. The connector authenticates with an
// Entra (Azure AD) app registration using the OAuth2 client_credentials grant
// (tenant id + client id + client secret) against the Microsoft identity
// platform, then reads the Intune device-management endpoints. The app
// registration must hold ONLY the read-only application permission
// `DeviceManagementManagedDevices.Read.All` (admin-consented). A write
// permission (`...ManagedDevices.PrivilegedOperations.All`,
// `...ManagedDevices.ReadWrite.All`) is a remote-wipe / config-push risk
// (P0-490-2 / threat-model E) and must NEVER be granted.
//
// The client secret is read from the environment, never a CLI flag (so it never
// lands in shell history), and is never logged or placed into an evidence
// record (P0-490-4 / AC-11): the resolved Credential redacts the secret on
// every format path.
package intuneauth

import (
	"fmt"
	"os"
	"strings"
)

// Env var names carrying the Intune / Graph credential. Preferred over flags so
// the secret never appears in shell history.
const (
	// EnvTenantID is the Entra (Azure AD) tenant id. Non-secret.
	EnvTenantID = "INTUNE_TENANT_ID"
	// EnvClientID is the Entra app-registration (application) id. Non-secret.
	EnvClientID = "INTUNE_CLIENT_ID"
	// EnvClientSecret is the Entra app-registration client secret.
	EnvClientSecret = "INTUNE_CLIENT_SECRET"
	// EnvGraphHost overrides the Graph host (e.g. for sovereign clouds:
	// graph.microsoft.us). Defaults to graph.microsoft.com.
	EnvGraphHost = "INTUNE_GRAPH_HOST"
	// EnvLoginHost overrides the identity-platform host (e.g.
	// login.microsoftonline.us). Defaults to login.microsoftonline.com.
	EnvLoginHost = "INTUNE_LOGIN_HOST"

	// DefaultGraphHost is the commercial-cloud Graph host.
	DefaultGraphHost = "graph.microsoft.com"
	// DefaultLoginHost is the commercial-cloud identity-platform host.
	DefaultLoginHost = "login.microsoftonline.com"

	// RequiredPermission is the single read-only Graph application permission the
	// connector needs. Documented as the minimum; the connector issues only
	// managed-device reads.
	RequiredPermission = "DeviceManagementManagedDevices.Read.All (application, admin-consented, read-only)"
)

// Credential is the resolved Intune / Graph auth material. The client secret is
// kept off String() so accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	tenantID     string
	clientID     string
	clientSecret string
	graphHost    string
	loginHost    string
}

// TenantID returns the Entra tenant id. Non-secret.
func (c Credential) TenantID() string { return c.tenantID }

// ClientID returns the Entra app-registration id. Non-secret.
func (c Credential) ClientID() string { return c.clientID }

// ClientSecret returns the Entra app-registration client secret. Callers
// exchange it for a token; it must never be logged.
func (c Credential) ClientSecret() string { return c.clientSecret }

// GraphHost returns the Graph host. Non-secret.
func (c Credential) GraphHost() string { return c.graphHost }

// LoginHost returns the identity-platform host. Non-secret.
func (c Credential) LoginHost() string { return c.loginHost }

// GraphBaseURL returns the Graph v1.0 API base URL.
func (c Credential) GraphBaseURL() string { return "https://" + c.graphHost + "/v1.0" }

// TokenURL returns the OAuth2 token endpoint for the tenant.
func (c Credential) TokenURL() string {
	return "https://" + c.loginHost + "/" + c.tenantID + "/oauth2/v2.0/token"
}

// Scope returns the client_credentials scope (the Graph resource + /.default).
func (c Credential) Scope() string { return "https://" + c.graphHost + "/.default" }

// String never reveals the secret. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("intuneauth.Credential{tenant_id: %q, client_id: %q, graph_host: %q, client_secret: <redacted %d bytes>}",
		c.tenantID, c.clientID, c.graphHost, len(c.clientSecret))
}

// GoString mirrors String so %#v cannot leak the secret either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its values through
// this so the package never imports cobra.
type ResolveOpts struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	GraphHost    string
	LoginHost    string
}

// Resolve returns a live credential. Tenant id, client id, and client secret are
// all required (after env fallback). The Graph + login hosts default to the
// commercial cloud.
func Resolve(opts ResolveOpts) (Credential, error) {
	tenantID := strings.TrimSpace(firstNonEmpty(opts.TenantID, os.Getenv(EnvTenantID)))
	if tenantID == "" {
		return Credential{}, fmt.Errorf("intuneauth: tenant id required (set %s)", EnvTenantID)
	}
	clientID := strings.TrimSpace(firstNonEmpty(opts.ClientID, os.Getenv(EnvClientID)))
	if clientID == "" {
		return Credential{}, fmt.Errorf("intuneauth: client id required (set %s)", EnvClientID)
	}
	clientSecret := strings.TrimSpace(firstNonEmpty(opts.ClientSecret, os.Getenv(EnvClientSecret)))
	if clientSecret == "" {
		return Credential{}, fmt.Errorf("intuneauth: client secret required (set %s, app granted %s)", EnvClientSecret, RequiredPermission)
	}
	graphHost := strings.TrimSpace(firstNonEmpty(opts.GraphHost, os.Getenv(EnvGraphHost), DefaultGraphHost))
	loginHost := strings.TrimSpace(firstNonEmpty(opts.LoginHost, os.Getenv(EnvLoginHost), DefaultLoginHost))
	return Credential{
		tenantID:     tenantID,
		clientID:     clientID,
		clientSecret: clientSecret,
		graphHost:    graphHost,
		loginHost:    loginHost,
	}, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
