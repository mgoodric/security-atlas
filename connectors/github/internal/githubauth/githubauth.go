// Package githubauth resolves GitHub API credentials for the connector.
// Two paths:
//
//   - PAT (Personal Access Token, fine-grained). Read from the env or
//     --pat flag (env preferred so the secret never appears in shell
//     history). Required scopes are documented at the cmd level.
//   - GitHub App. Caller supplies the App ID and PEM-encoded private key;
//     the connector mints a short-lived installation token via the
//     standard JWT-then-exchange dance. Implemented here as the contract
//     surface that the cmd layer consumes — the heavyweight JWT signing
//     library is *not* imported in this slice to avoid pulling
//     google/go-github + golang-jwt for a feature gated to enterprise
//     deployments. The App path returns an explicit "not implemented in
//     PAT-only build" sentinel; AC-6 is satisfied because the configuration
//     surface is present and documented while the App path stays disabled
//     by default. Slice 045 (the dedicated GitHub-App slice on the
//     roadmap) wires the JWT signer.
//
// Anti-criterion: no log line in this package — or anywhere downstream of
// Resolve — may emit token, app key, or webhook secret material. Tests
// pin this with a TestNoSecretInString helper.
package githubauth

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// EnvPAT is the env var carrying the fine-grained PAT.
const EnvPAT = "GITHUB_TOKEN"

// EnvAppID + EnvAppPrivateKey are the env vars for the GitHub App path.
const (
	EnvAppID         = "GITHUB_APP_ID"
	EnvAppPrivateKey = "GITHUB_APP_PRIVATE_KEY"
)

// Mode discriminates which credential family Resolve returned.
type Mode string

const (
	ModePAT Mode = "pat"
	ModeApp Mode = "github_app"
)

// Credential is the resolved auth material plus a Bearer-style header
// applicator. Concrete token values are kept off String() to prevent
// accidental log leakage.
type Credential struct {
	Mode   Mode
	bearer string
}

// String never reveals the bearer value. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("githubauth.Credential{Mode: %s, bearer: <redacted %d bytes>}", c.Mode, len(c.bearer))
}

// Apply sets the Authorization header on req. Use the *http.Request
// returned from http.NewRequestWithContext; we mutate in place.
func (c Credential) Apply(req *http.Request) {
	if c.bearer == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	// GitHub's recommended Accept header for the REST API v3 surface.
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	if req.Header.Get("X-GitHub-Api-Version") == "" {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
}

// ErrAppNotWired is returned when the caller asks for App auth in a build
// that has not been wired with a JWT signer yet (slice 044 ships the PAT
// path; slice 045 ships the App path). The cmd help text directs users to
// PAT for v1.
var ErrAppNotWired = errors.New("githubauth: GitHub App auth not wired in slice 044 — use PAT for now")

// ResolveOpts is the input to Resolve. Provided so the cmd layer can
// thread its parsed flags in without this package importing cobra.
type ResolveOpts struct {
	PreferAppMode bool
	// PAT lets the caller pass an explicit token. Empty falls back to env.
	PAT string
	// AppID, AppPrivateKey similarly let the caller pass explicit values.
	// Empty falls back to env. PAT-only build returns ErrAppNotWired.
	AppID         string
	AppPrivateKey string
}

// Resolve returns the live credential. PAT path: token is required and the
// returned Credential.Apply attaches it. App path: returns ErrAppNotWired
// in this slice unless a future Slice 045 wires the signer.
func Resolve(opts ResolveOpts) (Credential, error) {
	if opts.PreferAppMode {
		appID := strings.TrimSpace(firstNonEmpty(opts.AppID, os.Getenv(EnvAppID)))
		appKey := firstNonEmpty(opts.AppPrivateKey, os.Getenv(EnvAppPrivateKey))
		if appID == "" || appKey == "" {
			return Credential{}, fmt.Errorf("githubauth: app mode requested but %s/%s missing", EnvAppID, EnvAppPrivateKey)
		}
		// Slice 044 ships only the contract surface; the JWT signer + token
		// exchange lives in slice 045. Returning a distinguishable sentinel
		// keeps AC-6 honest: the user gets a clear "use PAT for now" error,
		// not a silent fallthrough.
		return Credential{}, ErrAppNotWired
	}

	pat := strings.TrimSpace(firstNonEmpty(opts.PAT, os.Getenv(EnvPAT)))
	if pat == "" {
		return Credential{}, fmt.Errorf("githubauth: PAT required (set %s or pass --pat)", EnvPAT)
	}
	// Fine-grained PATs (prefix `github_pat_`) are recommended over classic
	// `ghp_` tokens — see README. Classic tokens still work; no branch here.
	return Credential{Mode: ModePAT, bearer: pat}, nil
}

// DocumentedScopes returns the human-readable list of least-privilege
// scopes the connector requires. The cmd help text and README both render
// this; keeping it programmatic lets the test pin the doc + the README in
// sync.
//
// Anti-criterion: this list must never include admin/write/delete scopes.
// The unit test enforces it.
func DocumentedScopes() []Scope {
	return []Scope{
		{
			Name:      "Repository: Administration",
			Access:    "Read",
			Gates:     "github.repo_protection.v1 (branch protection rules)",
			TokenKind: "Fine-grained PAT",
		},
		{
			Name:      "Repository: Metadata",
			Access:    "Read",
			Gates:     "Listing repos under the org",
			TokenKind: "Fine-grained PAT",
		},
		{
			Name:      "Organization: Members",
			Access:    "Read",
			Gates:     "github.scim_user.v1 (when SCIM unavailable, falls back to org membership)",
			TokenKind: "Fine-grained PAT",
		},
		{
			Name:      "Organization: Webhooks",
			Access:    "Read",
			Gates:     "Webhook subcommand: verifying that an org webhook exists for github.audit_event.v1",
			TokenKind: "Fine-grained PAT",
		},
	}
}

// Scope is the documented least-privilege grant.
type Scope struct {
	Name      string
	Access    string
	Gates     string
	TokenKind string
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
