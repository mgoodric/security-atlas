// Package opaccount pulls org-level policy state from the 1Password
// Business public API and turns it into a canonical PolicyState that the
// cmd layer transforms into a 1password.org_policy.v1 evidence record.
//
// 1Password's Service Account-scoped public API exposes account metadata
// at GET /v1/account. The fields we consume map 1:1 to the bundled
// 1password.org_policy/1.0.0 schema: org_id, two_factor_required,
// minimum_password_length, domain_restrictions_enabled, active_members.
//
// The package is intentionally read-only. No PATCH/DELETE call paths
// are wired — the constitutional anti-criterion forbids them.
//
// Tests against this package use httptest.NewServer to replay realistic
// API responses. The connector binary itself talks to the real 1Password
// API in production, but the contract surface and the JSON decoding are
// the same.
package opaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
)

// Result mirrors the schema's pass/fail intent. Slice 044 documents the
// convention; we keep it shallow so the evaluator (slice 015) owns the
// real policy ladder.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// PolicyState is the canonical record one /v1/account response turns
// into. Field names map 1:1 to the 1password.org_policy.v1 schema.
type PolicyState struct {
	OrgID                     string
	TwoFactorRequired         bool
	MinimumPasswordLength     int
	DomainRestrictionsEnabled bool
	ActiveMembers             int
	Result                    Result
	Reason                    string
	ObservedAt                time.Time
}

// RawAccount is the subset of GET /v1/account this connector decodes.
// Exposed so tests can roundtrip the wire format without invoking the
// HTTP client.
type RawAccount struct {
	ID                        string `json:"id"`
	Name                      string `json:"name,omitempty"`
	TwoFactorRequired         bool   `json:"two_factor_required"`
	MinimumPasswordLength     int    `json:"minimum_password_length,omitempty"`
	DomainRestrictionsEnabled bool   `json:"domain_restrictions_enabled,omitempty"`
	ActiveMembers             int    `json:"active_member_count,omitempty"`
}

// API is the narrow surface the inspector needs. The concrete REST
// transport (Client) satisfies it; tests inject a fake.
type API interface {
	GetAccount(ctx context.Context) (*RawAccount, error)
}

// Inspect fetches the account and returns a single PolicyState. Errors
// fetching the account propagate (one-record-per-run; nothing to recover
// to if the only call fails). Defensive validation: empty org id and
// negative member counts are hard errors so the platform never sees a
// schema-violating record.
func Inspect(ctx context.Context, api API, now func() time.Time) (*PolicyState, error) {
	if api == nil {
		return nil, errors.New("opaccount: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	acct, err := api.GetAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if acct == nil || acct.ID == "" {
		return nil, errors.New("opaccount: empty org id in account response")
	}
	if acct.ActiveMembers < 0 {
		return nil, fmt.Errorf("opaccount: negative active_member_count=%d", acct.ActiveMembers)
	}
	state := &PolicyState{
		OrgID:                     acct.ID,
		TwoFactorRequired:         acct.TwoFactorRequired,
		MinimumPasswordLength:     acct.MinimumPasswordLength,
		DomainRestrictionsEnabled: acct.DomainRestrictionsEnabled,
		ActiveMembers:             acct.ActiveMembers,
		ObservedAt:                now(),
	}
	state.Result, state.Reason = evaluate(state)
	return state, nil
}

// evaluate runs the shallow connector-level pass/fail. v1 rule:
// two_factor_required AND minimum_password_length >= 12 (NIST 800-63B
// baseline) passes; anything else fails. The evaluator owns the real
// policy ladder.
func evaluate(s *PolicyState) (Result, string) {
	if !s.TwoFactorRequired {
		return ResultFail, "two_factor_required=false"
	}
	if s.MinimumPasswordLength < 12 {
		return ResultFail, fmt.Sprintf("minimum_password_length=%d (< NIST 800-63B baseline 12)", s.MinimumPasswordLength)
	}
	return ResultPass, fmt.Sprintf("2fa_required=true minimum_password_length=%d", s.MinimumPasswordLength)
}

// ---- REST client ----

// Client is a thin wrapper around http.Client carrying 1Password auth +
// JSON decoding. Tests construct one against an httptest server.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   opauth.Credential
}

// NewClient builds a Client. baseURL defaults to https://api.1password.com.
// Tests override.
func NewClient(httpClient *http.Client, baseURL string, creds opauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://api.1password.com"
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// GetAccount calls GET /v1/account and decodes the canonical subset.
func (c *Client) GetAccount(ctx context.Context) (*RawAccount, error) {
	u := c.BaseURL + "/v1/account"
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
	var out RawAccount
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode account: %w", err)
	}
	return &out, nil
}

// APIError carries 1Password REST error context. The Body is bounded.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("1password: HTTP %d", e.Status)
	}
	return fmt.Sprintf("1password: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
