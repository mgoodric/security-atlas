// Package opauth resolves 1Password API credentials for the connector.
//
// 1Password Business exposes its API via Service Accounts. A Service
// Account is a non-human identity that ships with a single bearer
// token (the Service Account JWT) and per-vault grants (`read_items`,
// `write_items`,
// `manage_vault`). Slice 046 is read-only by construction — the
// DocumentedScopes test rejects any non-read keyword.
//
// Anti-criterion: no log line in this package — or anywhere downstream of
// Resolve — may emit token material. TestCredential_StringRedacts pins
// this; %v / %s formatting flows through the String method which only
// reveals the byte length of the bearer.
package opauth

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// EnvServiceAccountToken is the canonical env var carrying the
// 1Password Business Service Account bearer.
const EnvServiceAccountToken = "ONEPASSWORD_SERVICE_ACCOUNT_TOKEN"

// Mode discriminates which credential family Resolve returned. v1 ships
// only the Service Account path; the constant exists so a future Mode
// (e.g. Connect server token) slots in without breaking callers.
type Mode string

// ModeServiceAccount is the only Mode slice 046 ships.
const ModeServiceAccount Mode = "service_account"

// Credential is the resolved auth material plus a Bearer-style header
// applicator. The bearer value is kept off String to prevent accidental
// log leakage.
type Credential struct {
	Mode   Mode
	bearer string
}

// String never reveals the bearer value. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("opauth.Credential{Mode: %s, bearer: <redacted %d bytes>}", c.Mode, len(c.bearer))
}

// Apply sets the Authorization + Accept headers on req. Use the
// *http.Request returned from http.NewRequestWithContext; we mutate in
// place.
func (c Credential) Apply(req *http.Request) {
	if c.bearer == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
}

// ResolveOpts is the input to Resolve. The cmd layer parses cobra flags
// then threads them through opts so this package never imports cobra.
type ResolveOpts struct {
	// Token lets the caller pass an explicit Service Account token.
	// Empty falls back to env. The env path is preferred so the secret
	// never appears in shell history or process listings.
	Token string
}

// Resolve returns the live credential. Service Account path: token is
// required and the returned Credential.Apply attaches it.
func Resolve(opts ResolveOpts) (Credential, error) {
	tok := strings.TrimSpace(firstNonEmpty(opts.Token, os.Getenv(EnvServiceAccountToken)))
	if tok == "" {
		return Credential{}, fmt.Errorf("opauth: Service Account token required (set %s or pass --token)", EnvServiceAccountToken)
	}
	return Credential{Mode: ModeServiceAccount, bearer: tok}, nil
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

// DocumentedScopes returns the canonical least-privilege grants this
// connector needs. 1Password Service Accounts are scoped per-vault.
// The connector requires only the vaults that carry org-policy posture
// (typically the "Private" / "Employee" / org-management vault), with
// read_items only. Anti-criterion: any grant containing a write/manage/
// admin keyword fails TestDocumentedScopes_NoWriteOrManageOrAdmin.
func DocumentedScopes() []Scope {
	return []Scope{
		{
			Name:      "vault:read_items",
			Access:    "Read",
			Gates:     "1password.org_policy.v1 (org id, 2FA-required, minimum password length, domain restrictions, active members)",
			TokenKind: "Service Account",
		},
		{
			Name:      "account:read",
			Access:    "Read",
			Gates:     "1password.org_policy.v1 (account metadata — org_id, active_members count)",
			TokenKind: "Service Account",
		},
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
