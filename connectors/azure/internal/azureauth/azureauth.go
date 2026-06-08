// Package azureauth resolves Microsoft Entra credentials for the Azure
// connector and documents the least-privilege permission set.
//
// The connector authenticates to Azure with a read-only Entra
// app-registration. Two auth modes are supported:
//
//	client-credentials  — AZURE_TENANT_ID + AZURE_CLIENT_ID + AZURE_CLIENT_SECRET
//	managed-identity     — no secret; the runtime supplies the token
//
// The resolved Credential never reveals its secret: fmt.Sprintf("%v", cred)
// returns a redacted summary so accidental log/print paths cannot leak it. The
// unit test pins this behaviour.
//
// Anti-criterion: no log line in this package — or anywhere downstream of
// Resolve — may emit the client secret. DocumentedPermissions returns the
// canonical least-privilege permissions; the companion test rejects any future
// widening into write / manage / admin / Owner / Contributor permissions.
package azureauth

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Env var names carrying the Entra app-registration client credentials.
// Preferred over flags so the secret never appears in shell history.
const (
	EnvTenantID     = "AZURE_TENANT_ID"
	EnvClientID     = "AZURE_CLIENT_ID"
	EnvClientSecret = "AZURE_CLIENT_SECRET"
)

// AuthMode selects how the connector obtains an Azure token.
type AuthMode string

const (
	// ModeClientCredentials uses an Entra app-registration secret.
	ModeClientCredentials AuthMode = "client-credentials"
	// ModeManagedIdentity uses the runtime-provided managed identity (no secret).
	ModeManagedIdentity AuthMode = "managed-identity"
)

// Credential is the resolved auth material. The secret is kept off String() so
// accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	mode     AuthMode
	tenantID string
	clientID string
	secret   string
}

// Mode returns the resolved auth mode.
func (c Credential) Mode() AuthMode { return c.mode }

// TenantID returns the Entra tenant id. Non-secret.
func (c Credential) TenantID() string { return c.tenantID }

// ClientID returns the app-registration client id. Non-secret.
func (c Credential) ClientID() string { return c.clientID }

// String never reveals the secret. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("azureauth.Credential{mode: %s, tenant_id: %q, client_id: %q, secret: <redacted %d bytes>}",
		c.mode, c.tenantID, c.clientID, len(c.secret))
}

// GoString mirrors String so %#v cannot leak the secret either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its parsed flags
// through this so the package never imports cobra.
type ResolveOpts struct {
	Mode AuthMode
	// TenantID lets the caller pass an explicit tenant id. Empty falls back
	// to the AZURE_TENANT_ID env var.
	TenantID string
	// ClientID / ClientSecret apply only to client-credentials mode. Empty
	// falls back to the env vars.
	ClientID     string
	ClientSecret string
}

// Resolve returns a live credential. In client-credentials mode every field is
// required (after env fallback) or a descriptive error is returned. In
// managed-identity mode only the tenant id is required and no secret is read.
func Resolve(opts ResolveOpts) (Credential, error) {
	mode := opts.Mode
	if mode == "" {
		mode = ModeClientCredentials
	}
	tenantID := strings.TrimSpace(firstNonEmpty(opts.TenantID, os.Getenv(EnvTenantID)))
	if tenantID == "" {
		return Credential{}, fmt.Errorf("azureauth: tenant id required (set %s or pass --tenant-id)", EnvTenantID)
	}

	switch mode {
	case ModeManagedIdentity:
		return Credential{mode: ModeManagedIdentity, tenantID: tenantID}, nil
	case ModeClientCredentials:
		clientID := strings.TrimSpace(firstNonEmpty(opts.ClientID, os.Getenv(EnvClientID)))
		secret := strings.TrimSpace(firstNonEmpty(opts.ClientSecret, os.Getenv(EnvClientSecret)))
		if clientID == "" {
			return Credential{}, fmt.Errorf("azureauth: client id required (set %s or pass --client-id)", EnvClientID)
		}
		if secret == "" {
			return Credential{}, fmt.Errorf("azureauth: client secret required (set %s)", EnvClientSecret)
		}
		return Credential{mode: ModeClientCredentials, tenantID: tenantID, clientID: clientID, secret: secret}, nil
	default:
		return Credential{}, fmt.Errorf("azureauth: unknown auth mode %q (want %s or %s)",
			mode, ModeClientCredentials, ModeManagedIdentity)
	}
}

// Permission is one documented least-privilege grant the connector requires.
type Permission struct {
	Surface string // "Microsoft Graph" | "Azure Resource Manager"
	Name    string // permission / role name
	Access  string // always "Read" — the connector reads only
	Gates   string // which evidence kind the permission gates
}

// DocumentedPermissions returns the canonical least-privilege permissions the
// connector requires. The cmd help text and README both render this; keeping it
// programmatic lets the test pin the doc + the README in sync.
//
// Anti-criterion (P0-486-2): this list must NEVER include write / manage /
// admin / Owner / Contributor / Global Administrator. The unit test enforces it.
func DocumentedPermissions() []Permission {
	return []Permission{
		{
			Surface: "Microsoft Graph",
			Name:    "Directory.Read.All",
			Access:  "Read",
			Gates:   "azure.entra_role_assignment.v1 (directory-role / RBAC assignments)",
		},
		{
			Surface: "Microsoft Graph",
			Name:    "Application.Read.All",
			Access:  "Read",
			Gates:   "azure.entra_role_assignment.v1 (service-principal / app-registration inventory)",
		},
		{
			Surface: "Azure Resource Manager",
			Name:    "Reader (built-in role)",
			Access:  "Read",
			// The SAME ARM Reader role gates both ARM-sourced kinds — slice 519
			// adds the AKS managed-cluster kind WITHOUT widening the scope
			// (P0-519-2). Reader cannot call listClusterAdminCredential
			// (P0-519-1), so admin kubeconfig is unreachable by construction.
			Gates: "azure.storage_account_config.v1 (storage account configuration) + azure.aks_cluster_config.v1 (AKS managed-cluster configuration)",
		},
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ParseMode validates a mode string from the CLI.
func ParseMode(s string) (AuthMode, error) {
	switch AuthMode(strings.TrimSpace(s)) {
	case ModeClientCredentials:
		return ModeClientCredentials, nil
	case ModeManagedIdentity:
		return ModeManagedIdentity, nil
	case "":
		return ModeClientCredentials, nil
	default:
		return "", errors.New("azureauth: --auth-mode must be client-credentials or managed-identity")
	}
}
