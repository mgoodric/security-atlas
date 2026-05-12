// Package oktapolicy pulls Okta MFA-enrollment policies, producing one
// PolicyState per discovered policy.
//
// Endpoint: GET /api/v1/policies?type=MFA_ENROLL
//
// The package is built around a small API interface so tests can swap in
// an httptest.NewServer-backed transport without depending on the
// upstream Okta SDK (which we deliberately avoid pulling in to keep the
// connector's go.sum lean — slice 044 made the same decision for
// google/go-github).
package oktapolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

// Result mirrors the schema's pass/fail intent.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// PolicyState is one record the cmd layer turns into an evidence record.
// Field names map 1:1 to okta.mfa_policy.v1 schema.
type PolicyState struct {
	PolicyID        string
	PolicyName      string
	MFARequired     bool
	FactorsAllowed  []string
	AppliesToGroups []string
	Result          Result
	Reason          string
	ObservedAt      time.Time
}

// API is the narrow surface Pull depends on. The concrete REST client
// satisfies it; tests inject a fake.
type API interface {
	ListMFAPolicies(ctx context.Context) ([]rawPolicy, error)
}

// Pull lists every MFA_ENROLL policy in the org and returns canonical
// PolicyState rows. Errors fetching the policy list abort the run.
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
	out := make([]PolicyState, 0, len(policies))
	for _, p := range policies {
		state := PolicyState{
			PolicyID:        p.ID,
			PolicyName:      p.Name,
			MFARequired:     extractMFARequired(p),
			FactorsAllowed:  extractFactorsAllowed(p),
			AppliesToGroups: extractAppliesToGroups(p),
			ObservedAt:      now(),
		}
		if state.PolicyID == "" || state.PolicyName == "" {
			// Schema requires both; skip rather than emit invalid records.
			continue
		}
		state.Result, state.Reason = evaluate(state)
		out = append(out, state)
	}
	return out, nil
}

// evaluate computes pass/fail per a deliberately shallow rule: an MFA
// policy that requires at least one factor PASSES; everything else fails.
// The evaluator (slice 015) owns the policy ladder — the connector
// surfaces descriptive evidence.
func evaluate(s PolicyState) (Result, string) {
	if s.MFARequired && len(s.FactorsAllowed) > 0 {
		return ResultPass, fmt.Sprintf("mfa_required=true factors=%d", len(s.FactorsAllowed))
	}
	if !s.MFARequired {
		return ResultFail, "mfa_required=false"
	}
	return ResultInconclusive, "mfa_required=true but no factors configured"
}

// ---- REST client ----

// rawPolicy is the subset of Okta's policy resource we consume. Okta
// embeds factor configuration under settings.factors and the group
// targeting under conditions.people.groups.
type rawPolicy struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Settings *struct {
		Factors map[string]struct {
			Enroll struct {
				Self string `json:"self"`
			} `json:"enroll"`
		} `json:"factors"`
	} `json:"settings,omitempty"`
	Conditions *struct {
		People *struct {
			Groups *struct {
				Include []string `json:"include"`
			} `json:"groups,omitempty"`
		} `json:"people,omitempty"`
	} `json:"conditions,omitempty"`
}

func extractMFARequired(p rawPolicy) bool {
	if p.Settings == nil {
		return false
	}
	for _, f := range p.Settings.Factors {
		if strings.EqualFold(f.Enroll.Self, "REQUIRED") {
			return true
		}
	}
	return false
}

func extractFactorsAllowed(p rawPolicy) []string {
	if p.Settings == nil {
		return nil
	}
	out := make([]string, 0, len(p.Settings.Factors))
	for name, f := range p.Settings.Factors {
		s := strings.ToUpper(f.Enroll.Self)
		if s == "REQUIRED" || s == "OPTIONAL" {
			out = append(out, name)
		}
	}
	return out
}

func extractAppliesToGroups(p rawPolicy) []string {
	if p.Conditions == nil || p.Conditions.People == nil || p.Conditions.People.Groups == nil {
		return nil
	}
	return p.Conditions.People.Groups.Include
}

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
	u := c.BaseURL + "/api/v1/policies?type=MFA_ENROLL"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var out []rawPolicy
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode policies: %w", err)
	}
	return out, nil
}

// APIError carries Okta REST error context. The Body is bounded.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("okta: HTTP %d", e.Status)
	}
	return fmt.Sprintf("okta: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
