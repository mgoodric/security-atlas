// Package osqueryauth resolves credentials for the osquery/Fleet
// connector. Two modes:
//
//   - Fleet API token. The connector hits Fleet's REST surface
//     (developer docs at fleetdm.com/docs/rest-api/rest-api). Fleet uses
//     standard `Authorization: Bearer <token>` on every request. The
//     token is read from FLEET_API_TOKEN (preferred so it never appears
//     in shell history) or the hidden --token flag for ad-hoc shells.
//
//   - Local osqueryd extension socket. No auth — the security boundary is
//     filesystem permission on the Unix-domain socket (root-owned by
//     default). The connector is a read-only consumer; it does not bind,
//     listen, or proxy the socket. The credential surface here returns an
//     empty Credential and a sentinel Mode = ModeLocal so downstream code
//     can branch on it without re-reading the runtime flags.
//
// Anti-criterion (slice 047 P0): no log line in this package — or anywhere
// downstream of Resolve — may emit the Fleet token. Credential.String()
// redacts. The unit test pins both %s and %v formatting paths.
//
// DocumentedScopes returns the canonical least-privilege Fleet role list.
// The companion test rejects any future widening into admin / maintainer /
// write / delete keywords.
package osqueryauth

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// EnvFleetAPIToken is the env var carrying the Fleet API token. Preferred
// over the --token flag so the secret never appears in shell history.
const EnvFleetAPIToken = "FLEET_API_TOKEN"

// Mode discriminates which credential family Resolve returned.
type Mode string

const (
	// ModeFleet means: Fleet REST API + token Bearer auth.
	ModeFleet Mode = "fleet"
	// ModeLocal means: local osqueryd extension socket; no auth material.
	ModeLocal Mode = "local"
)

// Credential is the resolved auth material plus a header applicator. The
// token value is kept off String() so accidental %v / %+v formatting paths
// cannot leak it.
type Credential struct {
	Mode  Mode
	token string
}

// String never reveals the token. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("osqueryauth.Credential{Mode: %s, token: <redacted %d bytes>}", c.Mode, len(c.token))
}

// Apply sets Authorization: Bearer <token> on req. Use the *http.Request
// returned from http.NewRequestWithContext; we mutate in place. Also sets
// the Accept / Content-Type headers Fleet expects.
//
// In ModeLocal Apply is a no-op — there is no header to attach to a Unix
// socket dial.
func (c Credential) Apply(req *http.Request) {
	if c.Mode != ModeFleet || c.token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if req.Header.Get("Content-Type") == "" && req.Method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
}

// ResolveOpts is the input to Resolve. The cmd layer threads its parsed
// flags through this so the package never imports cobra.
type ResolveOpts struct {
	// PreferLocalMode flips Resolve into local-socket auth path.
	PreferLocalMode bool
	// Token lets the caller pass an explicit Fleet token. Empty falls back
	// to FLEET_API_TOKEN env.
	Token string
}

// Resolve returns a live credential.
//
//   - ModeFleet: token required (env-or-flag); empty after trim returns a
//     descriptive error.
//   - ModeLocal: empty Credential with Mode=ModeLocal; no token validated.
func Resolve(opts ResolveOpts) (Credential, error) {
	if opts.PreferLocalMode {
		return Credential{Mode: ModeLocal}, nil
	}
	token := strings.TrimSpace(firstNonEmpty(opts.Token, os.Getenv(EnvFleetAPIToken)))
	if token == "" {
		return Credential{}, fmt.Errorf("osqueryauth: Fleet API token required (set %s or pass --token)", EnvFleetAPIToken)
	}
	return Credential{Mode: ModeFleet, token: token}, nil
}

// Scope is the documented least-privilege grant.
type Scope struct {
	Name      string
	Access    string
	Gates     string
	TokenKind string
}

// DocumentedScopes returns the canonical least-privilege Fleet roles the
// connector requires. The cmd help text and README both render this;
// keeping it programmatic lets the test pin the doc + the README in sync.
//
// Anti-criterion P0: this list must NEVER include write / delete / admin /
// maintainer access. The unit test enforces it.
//
// Fleet's RBAC model: admin, maintainer, observer_plus, observer. The
// observer / observer_plus roles are read-only and sufficient for the host
// listing + per-host detail endpoints this connector consumes.
//
// Fleet REST API reference: https://fleetdm.com/docs/rest-api/rest-api
func DocumentedScopes() []Scope {
	return []Scope{
		{
			Name:      "observer (global)",
			Access:    "Read",
			Gates:     "osquery.host_posture.v1 (GET /api/v1/fleet/hosts + /api/v1/fleet/hosts/{id})",
			TokenKind: "Fleet API token (observer role)",
		},
		{
			Name:      "observer_plus (per-team)",
			Access:    "Read",
			Gates:     "osquery.host_posture.v1 when scoped to a single Fleet team rather than global",
			TokenKind: "Fleet API token (observer_plus role)",
		},
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
