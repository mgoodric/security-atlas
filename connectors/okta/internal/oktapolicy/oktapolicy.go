// Package oktapolicy pulls Okta MFA-enrollment policies, producing one
// PolicyState per discovered policy (okta.mfa_policy.v2).
//
// Endpoints:
//   - GET /api/v1/policies?type=MFA_ENROLL (every status)
//   - GET /api/v1/groups?search=type eq "BUILT_IN" (resolves the id of
//     Okta's default Everyone group, so a policy's group ids can be
//     compared against it)
//
// MFA_ENROLL settings come in two mutually exclusive shapes (Okta's
// "Understand authenticator enrollment policy API changes after the
// upgrade" guide): Classic Engine and pre-upgrade policies carry a
// `settings.factors` map keyed by factor name; Identity Engine policies
// carry `settings.type: AUTHENTICATORS` and a `settings.authenticators[]`
// list keyed by `key`. Both are parsed, and the FIDO keys of either shape
// are normalised to the one canonical value `webauthn` (see
// canonicalFactorKey).
//
// The package is built around a small API interface so tests can swap in
// an httptest.NewServer-backed transport without depending on the
// upstream Okta SDK (which we deliberately avoid pulling in to keep the
// connector's go.sum lean — slice 044 made the same decision for
// google/go-github).
package oktapolicy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapage"
)

// Result mirrors the schema's pass/fail intent.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
	// ResultNA marks a policy that is not ACTIVE: Okta does not apply it,
	// so it neither satisfies nor violates an MFA requirement.
	ResultNA Result = "na"
)

// StatusActive is the Okta policy status for a policy Okta applies.
const StatusActive = "ACTIVE"

// FactorWebAuthn is the canonical key for a FIDO authenticator, whichever
// key the policy used for it.
const FactorWebAuthn = "webauthn"

// PolicyState is one record the cmd layer turns into an evidence record.
// Field names map 1:1 to the okta.mfa_policy.v2 schema.
type PolicyState struct {
	PolicyID   string
	PolicyName string
	// Status is Okta's policy status, ACTIVE or INACTIVE.
	Status string
	// Priority is Okta's evaluation order (1 is evaluated first); 0 when
	// Okta did not report one.
	Priority    int
	MFARequired bool
	// FactorsAllowed holds the canonical keys of every factor the policy
	// sets to REQUIRED or OPTIONAL, sorted.
	FactorsAllowed []string
	// AppliesToGroups holds the Okta group ids in the policy's
	// conditions.people.groups.include, as Okta returned them.
	AppliesToGroups []string
	// AppliesToEveryone is true when AppliesToGroups contains the id of
	// the org's Everyone group.
	AppliesToEveryone bool
	Result            Result
	Reason            string
	ObservedAt        time.Time
}

// API is the narrow surface Pull depends on. The concrete REST client
// satisfies it; tests inject a fake.
type API interface {
	ListMFAPolicies(ctx context.Context) ([]rawPolicy, error)
	// EveryoneGroupID returns the id of the org's Everyone group, or ""
	// when Okta did not return it.
	EveryoneGroupID(ctx context.Context) (string, error)
}

// ErrEveryoneNotFound is returned when the org's Everyone group can't be
// resolved. Okta omits it from the groups list for a token whose admin
// role is Group Administrator. Without it applies_to_everyone can't be
// stated, so the pull stops rather than report every policy as not
// covering everyone.
var ErrEveryoneNotFound = errors.New("everyone group not found among BUILT_IN groups (Okta hides it from a Group Administrator token; use a Read-only Administrator token)")

// Pull lists every MFA_ENROLL policy in the org and returns canonical
// PolicyState rows. Errors fetching the policy list or resolving the
// Everyone group abort the run.
func Pull(ctx context.Context, api API, now func() time.Time) ([]PolicyState, error) {
	if api == nil {
		return nil, errors.New("oktapolicy: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	policies, err := api.ListMFAPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list mfa policies: %w", err)
	}
	everyoneID, err := api.EveryoneGroupID(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve everyone group: %w", err)
	}
	if everyoneID == "" {
		return nil, ErrEveryoneNotFound
	}
	out := make([]PolicyState, 0, len(policies))
	for _, p := range policies {
		factors := extractFactors(p)
		groups := extractAppliesToGroups(p)
		state := PolicyState{
			PolicyID:          p.ID,
			PolicyName:        p.Name,
			Status:            strings.ToUpper(p.Status),
			Priority:          p.Priority,
			MFARequired:       mfaRequired(factors),
			FactorsAllowed:    factorsAllowed(factors),
			AppliesToGroups:   groups,
			AppliesToEveryone: contains(groups, everyoneID),
			ObservedAt:        now(),
		}
		if state.PolicyID == "" || state.PolicyName == "" || state.Status == "" {
			// Schema requires all three; skip rather than emit invalid records.
			continue
		}
		state.Result, state.Reason = evaluate(state)
		out = append(out, state)
	}
	return out, nil
}

// evaluate computes pass/fail per a deliberately shallow rule: an active
// MFA policy that requires at least one factor PASSES; an inactive policy
// is not applicable; everything else fails. Controls decide on the
// payload (ADR 0028); the connector surfaces descriptive evidence.
func evaluate(s PolicyState) (Result, string) {
	if s.Status != StatusActive {
		return ResultNA, "status=" + s.Status
	}
	if s.MFARequired && len(s.FactorsAllowed) > 0 {
		return ResultPass, fmt.Sprintf("mfa_required=true factors=%d", len(s.FactorsAllowed))
	}
	if !s.MFARequired {
		return ResultFail, "mfa_required=false"
	}
	return ResultInconclusive, "mfa_required=true but no factors configured"
}

// ---- settings parsing ----

// rawEnroll is the `enroll` object both settings shapes share.
type rawEnroll struct {
	Self string `json:"self"`
}

// rawPolicy is the subset of Okta's policy resource we consume.
type rawPolicy struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Priority int    `json:"priority"`
	Settings *struct {
		Type    string `json:"type"`
		Factors map[string]struct {
			Enroll rawEnroll `json:"enroll"`
		} `json:"factors"`
		Authenticators []struct {
			Key    string    `json:"key"`
			Enroll rawEnroll `json:"enroll"`
		} `json:"authenticators"`
	} `json:"settings,omitempty"`
	Conditions *struct {
		People *struct {
			Groups *struct {
				Include []string `json:"include"`
			} `json:"groups,omitempty"`
		} `json:"people,omitempty"`
	} `json:"conditions,omitempty"`
}

// factor is one entry of either settings shape: its canonical key and its
// upper-cased enroll.self (REQUIRED, OPTIONAL or NOT_ALLOWED).
type factor struct {
	key    string
	enroll string
}

// extractFactors reads `settings.authenticators[]` when the policy uses
// the authenticator shape and the `settings.factors` map otherwise; Okta
// documents the two as mutually exclusive. The password is dropped from
// both: it is the first factor, so requiring it is not multi-factor.
func extractFactors(p rawPolicy) []factor {
	if p.Settings == nil {
		return nil
	}
	var out []factor
	add := func(key string, e rawEnroll) {
		k := canonicalFactorKey(key)
		if k == "" || k == "okta_password" {
			return
		}
		out = append(out, factor{key: k, enroll: strings.ToUpper(e.Self)})
	}
	if len(p.Settings.Authenticators) > 0 || strings.EqualFold(p.Settings.Type, "AUTHENTICATORS") {
		for _, a := range p.Settings.Authenticators {
			add(a.Key, a.Enroll)
		}
		return out
	}
	for name, f := range p.Settings.Factors {
		add(name, f.Enroll)
	}
	return out
}

// canonicalFactorKey maps the FIDO keys of both shapes to `webauthn`:
// Classic `fido_webauthn` and `fido_u2f`, the Identity Engine enrollment
// policy key `webauthn`, and `security_key`, which Okta's factor-to-
// authenticator mapping table gives for `fido_webauthn`. Every other key
// passes through lower-cased.
func canonicalFactorKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	switch k {
	case "fido_webauthn", "fido_u2f", "webauthn", "security_key":
		return FactorWebAuthn
	}
	return k
}

func mfaRequired(fs []factor) bool {
	for _, f := range fs {
		if f.enroll == "REQUIRED" {
			return true
		}
	}
	return false
}

func factorsAllowed(fs []factor) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fs {
		if (f.enroll == "REQUIRED" || f.enroll == "OPTIONAL") && !seen[f.key] {
			seen[f.key] = true
			out = append(out, f.key)
		}
	}
	sort.Strings(out)
	return out
}

func extractAppliesToGroups(p rawPolicy) []string {
	if p.Conditions == nil || p.Conditions.People == nil || p.Conditions.People.Groups == nil {
		return nil
	}
	return p.Conditions.People.Groups.Include
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ---- REST client ----

// Client is a thin wrapper around http.Client carrying Okta auth + JSON
// decoding. Tests construct one against an httptest server.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   oktaauth.Credential
}

// NewClient builds a Client. baseURL is typically
// `https://{org}.okta.com`; tests override.
func NewClient(httpClient *http.Client, baseURL string, creds oktaauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// ListMFAPolicies fetches MFA_ENROLL policies for the org.
func (c *Client) ListMFAPolicies(ctx context.Context) ([]rawPolicy, error) {
	out, err := oktapage.List[rawPolicy](ctx, c.HTTP, c.BaseURL, c.Creds, "/api/v1/policies?type=MFA_ENROLL")
	if err != nil {
		return nil, fmt.Errorf("policies: %w", err)
	}
	return out, nil
}

// rawGroup is the subset of Okta's group resource EveryoneGroupID reads.
type rawGroup struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
}

// EveryoneGroupID finds the org's Everyone group among its BUILT_IN
// groups (Okta's GroupType: "managed by Okta and can't be modified"),
// using the `search` parameter Okta's List all groups operation
// recommends. It returns "" when no BUILT_IN group is named Everyone.
func (c *Client) EveryoneGroupID(ctx context.Context) (string, error) {
	q := "/api/v1/groups?search=" + url.QueryEscape(`type eq "BUILT_IN"`)
	groups, err := oktapage.List[rawGroup](ctx, c.HTTP, c.BaseURL, c.Creds, q)
	if err != nil {
		return "", fmt.Errorf("groups: %w", err)
	}
	for _, g := range groups {
		if strings.EqualFold(g.Type, "BUILT_IN") && g.Profile.Name == "Everyone" {
			return g.ID, nil
		}
	}
	return "", nil
}

// APIError carries Okta REST error context. The Body is bounded.
type APIError = oktapage.APIError
