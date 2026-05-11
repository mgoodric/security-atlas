// Package githubscim reconciles GitHub Enterprise SCIM-provisioned users.
// SCIM 2.0 over the /scim/v2/Users endpoint scoped to one organization.
//
// Slice 044 emits one github.scim_user.v1 record per discovered user per
// run. The package is intentionally read-only — SCIM PATCH/DELETE never
// originate here. Lifecycle decisions live in slice 035 (RBAC).
package githubscim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
)

// User is the canonical record one SCIM response row turns into. Fields
// map 1:1 to github.scim_user.v1 schema.
type User struct {
	SCIMUserID   string
	UserName     string
	Active       bool
	ExternalID   string
	PrimaryEmail string
	Org          string
	ObservedAt   time.Time
}

// API is the narrow surface needed by Reconcile; the concrete Client
// satisfies it.
type API interface {
	ListUsers(ctx context.Context, org string) ([]rawSCIMUser, error)
}

// Reconcile lists every SCIM user in org and returns canonical User
// records. Returns ErrSCIMUnavailable if the org has no SCIM provider
// (non-enterprise) — caller turns this into a clean skip so the rest of
// the run continues.
func Reconcile(ctx context.Context, api API, org string, now func() time.Time) ([]User, error) {
	if api == nil {
		return nil, errors.New("githubscim: API is nil")
	}
	if org == "" {
		return nil, errors.New("githubscim: org is required")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	rows, err := api.ListUsers(ctx, org)
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(rows))
	for _, r := range rows {
		u := User{
			SCIMUserID: r.ID,
			UserName:   r.UserName,
			Active:     r.Active,
			ExternalID: r.ExternalID,
			Org:        org,
			ObservedAt: now(),
		}
		for _, e := range r.Emails {
			if e.Primary {
				u.PrimaryEmail = e.Value
				break
			}
		}
		if u.SCIMUserID == "" || u.UserName == "" {
			// SCIM compliance requires both — skip rather than emit invalid records.
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// ---- REST client ----

type rawSCIMUser struct {
	ID         string          `json:"id"`
	UserName   string          `json:"userName"`
	ExternalID string          `json:"externalId"`
	Active     bool            `json:"active"`
	Emails     []rawSCIMEmail  `json:"emails"`
	Schemas    json.RawMessage `json:"schemas"`
}

type rawSCIMEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
}

type scimListResponse struct {
	Resources    []rawSCIMUser `json:"Resources"`
	TotalResults int           `json:"totalResults"`
}

// ErrSCIMUnavailable is the sentinel for "this org has no SCIM provider"
// — the cmd treats it as a skippable condition, not a hard failure.
var ErrSCIMUnavailable = errors.New("githubscim: SCIM not available for org")

// Client is a thin HTTP client for the SCIM v2 surface.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   githubauth.Credential
}

// NewClient builds a SCIM client. baseURL defaults to api.github.com
// (where the SCIM v2 endpoint lives for Enterprise organizations).
func NewClient(httpClient *http.Client, baseURL string, creds githubauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// ListUsers fetches every SCIM-provisioned user in the org. Returns
// ErrSCIMUnavailable when the endpoint responds 404 (non-enterprise or
// SCIM not enabled).
func (c *Client) ListUsers(ctx context.Context, org string) ([]rawSCIMUser, error) {
	u := fmt.Sprintf("%s/scim/v2/organizations/%s/Users?count=100", c.BaseURL, url.PathEscape(org))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	req.Header.Set("Accept", "application/scim+json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK:
		var body scimListResponse
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			return nil, fmt.Errorf("decode scim: %w", err)
		}
		return body.Resources, nil
	case http.StatusNotFound:
		return nil, ErrSCIMUnavailable
	default:
		return nil, fmt.Errorf("scim: HTTP %d: %s", res.StatusCode, drain(res.Body))
	}
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
