// Package opaccount reads account state from 1Password's documented APIs
// and turns it into a canonical PolicyState that the cmd layer transforms
// into a 1password.org_policy.v2 evidence record.
//
// #2968: v1 read GET https://api.1password.com/v1/account, which no
// 1Password documentation describes, and emitted 2FA-required, minimum
// password length and domain restrictions from it. None of those fields
// has a documented source, so v2 drops them. The two sources now are:
//
//   - Events API, GET /api/v2/auth/introspect
//     (www.1password.dev/events-api/reference/introspect.md): the account
//     UUID, and the features the Events Reporting token may read. The
//     feature `auditevents` is what audit_events_enabled reports.
//   - Users API, POST /v1/oauth/token then GET /v1/accounts/{account}/users
//     (www.1password.dev/users-api/request-access-token.md, list-users.md):
//     each user's state, ACTIVE or SUSPENDED, counted into the two member
//     totals.
//
// Nothing here was checked against a live 1Password account; the tests
// replay the documented response shapes.
//
// The package is read-only. The only requests it makes are the two GETs
// and the token POST; the Users API's suspend/reactivate calls are never
// wired.
package opaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
)

// Result mirrors the record's verdict.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// FeatureAuditEvents is the introspect feature naming the audit-event
// stream.
const FeatureAuditEvents = "auditevents"

// PolicyState is the canonical record one run turns into. Field names map
// 1:1 to the 1password.org_policy.v2 schema.
type PolicyState struct {
	OrgID              string
	AuditEventsEnabled bool
	ActiveMembers      int
	SuspendedMembers   int
	Result             Result
	Reason             string
	ObservedAt         time.Time
}

// Introspection is the documented introspect response.
type Introspection struct {
	UUID        string   `json:"uuid"`
	IssuedAt    string   `json:"issued_at"`
	Features    []string `json:"features"`
	AccountUUID string   `json:"account_uuid"`
}

// User is the documented user.v1.User object, the fields this connector
// decodes.
type User struct {
	ID    string `json:"id"`
	State string `json:"state,omitempty"`
}

// MemberCounts is the per-state tally of one account's users.
type MemberCounts struct {
	Active    int
	Suspended int
}

// API is the narrow surface the inspector needs. The concrete REST
// transport (Client) satisfies it; tests inject a fake.
type API interface {
	Introspect(ctx context.Context) (*Introspection, error)
	CountMembers(ctx context.Context, account string) (MemberCounts, error)
}

// Inspect reads the introspection, then the account's users, and returns
// one PolicyState. Any error stops the run: a record built from half the
// sources would report a count or a stream state nobody read.
func Inspect(ctx context.Context, api API, now func() time.Time) (*PolicyState, error) {
	if api == nil {
		return nil, errors.New("opaccount: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	in, err := api.Introspect(ctx)
	if err != nil {
		return nil, fmt.Errorf("events introspect: %w", err)
	}
	if in == nil || in.AccountUUID == "" {
		return nil, errors.New("opaccount: introspect response has no account_uuid")
	}
	counts, err := api.CountMembers(ctx, in.AccountUUID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	state := &PolicyState{
		OrgID:              in.AccountUUID,
		AuditEventsEnabled: slices.Contains(in.Features, FeatureAuditEvents),
		ActiveMembers:      counts.Active,
		SuspendedMembers:   counts.Suspended,
		ObservedAt:         now(),
	}
	state.Result, state.Reason = evaluate(state)
	return state, nil
}

// evaluate is the connector's verdict. It can fail a record and never pass
// one: the audit stream being off is a failure, but the stream being on
// does not show the account's sign-in rules, which no documented API
// reports (#2968).
func evaluate(s *PolicyState) (Result, string) {
	if !s.AuditEventsEnabled {
		return ResultFail, "audit_events_enabled=false (the Events Reporting integration is not authorized for auditevents)"
	}
	return ResultInconclusive, "audit_events_enabled=true; 2FA and password rules have no documented 1Password API"
}

// ---- REST client ----

// Default base URLs, for accounts on 1password.com. Other regions
// (1password.ca, 1password.eu, ent.1password.com) set both.
const (
	DefaultEventsBaseURL = "https://events.1password.com"
	DefaultUsersBaseURL  = "https://api.1password.com"
)

// pageSize is the documented maximum max_page_size for list users.
const pageSize = 1000

// maxPages bounds the user listing: 1000 pages of 1000 users.
const maxPages = 1000

// Client is a thin wrapper around http.Client carrying 1Password auth +
// JSON decoding. Tests construct one against an httptest server.
type Client struct {
	HTTP          *http.Client
	EventsBaseURL string
	UsersBaseURL  string
	Creds         opauth.Credentials
}

// NewClient builds a Client. Empty base URLs take the 1password.com
// defaults. A nil httpClient gets a 20-second timeout and the redirect
// guard.
func NewClient(httpClient *http.Client, eventsBaseURL, usersBaseURL string, creds opauth.Credentials) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: opauth.CheckRedirect}
	}
	if eventsBaseURL == "" {
		eventsBaseURL = DefaultEventsBaseURL
	}
	if usersBaseURL == "" {
		usersBaseURL = DefaultUsersBaseURL
	}
	return &Client{
		HTTP:          httpClient,
		EventsBaseURL: strings.TrimRight(eventsBaseURL, "/"),
		UsersBaseURL:  strings.TrimRight(usersBaseURL, "/"),
		Creds:         creds,
	}
}

// Introspect calls GET /api/v2/auth/introspect on the Events API.
func (c *Client) Introspect(ctx context.Context) (*Introspection, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.EventsBaseURL+"/api/v2/auth/introspect", nil)
	if err != nil {
		return nil, err
	}
	c.Creds.ApplyEvents(req)
	var out Introspection
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// usersToken exchanges the OAuth client credentials for an access token
// (POST /v1/oauth/token, grant_type=client_credentials, Basic client
// auth). Tokens last 900 seconds and one run needs one.
func (c *Client) usersToken(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.UsersBaseURL+"/v1/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	c.Creds.ApplyUsersClient(req)
	var tok tokenResponse
	if err := c.do(req, &tok); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	if tok.AccessToken == "" || !strings.EqualFold(tok.TokenType, "Bearer") {
		return "", fmt.Errorf("token: response has no bearer access_token (token_type %q)", tok.TokenType)
	}
	return tok.AccessToken, nil
}

type listUsersResponse struct {
	Results       []User `json:"results"`
	NextPageToken string `json:"next_page_token,omitempty"`
}

// CountMembers lists every user of account and tallies them by state. It
// follows next_page_token until the response omits it, and returns all the
// counts or an error, never a partial tally.
func (c *Client) CountMembers(ctx context.Context, account string) (MemberCounts, error) {
	var counts MemberCounts
	token, err := c.usersToken(ctx)
	if err != nil {
		return counts, err
	}
	seen := map[string]bool{}
	pageToken := ""
	for range maxPages {
		q := url.Values{"max_page_size": {fmt.Sprint(pageSize)}}
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		u := c.UsersBaseURL + "/v1/accounts/" + url.PathEscape(account) + "/users?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return MemberCounts{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		var page listUsersResponse
		if err := c.do(req, &page); err != nil {
			return MemberCounts{}, err
		}
		for _, usr := range page.Results {
			switch usr.State {
			case "ACTIVE":
				counts.Active++
			case "SUSPENDED":
				counts.Suspended++
			}
		}
		if page.NextPageToken == "" {
			return counts, nil
		}
		if seen[page.NextPageToken] {
			return MemberCounts{}, fmt.Errorf("list users: next_page_token %q repeated", page.NextPageToken)
		}
		seen[page.NextPageToken] = true
		pageToken = page.NextPageToken
	}
	return MemberCounts{}, fmt.Errorf("list users: more than %d pages", maxPages)
}

func (c *Client) do(req *http.Request, out any) error {
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", req.URL.Path, err)
	}
	return nil
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
