// Package oktausers pulls per-user lifecycle + MFA-enrolment state.
//
// Endpoints:
//   - GET /api/v1/users                        (list users)
//   - GET /api/v1/users/{id}/factors           (per-user MFA factors)
//
// Output is one UserLifecycle record per user per run. Feeds the SCIM
// joiner/mover/leaver evidence path plus MFA-enrolment coverage. The
// Result is PASS for ACTIVE + MFA-enrolled, FAIL for DEPROVISIONED /
// SUSPENDED / MFA-unenrolled, otherwise INCONCLUSIVE.
package oktausers

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

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

// Result mirrors the connector's pass/fail intent.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// Lifecycle is one record the cmd layer turns into an evidence record.
// Field names map to okta.user_lifecycle.v1 schema; nullable timestamps
// stay zero when Okta returns nothing.
type Lifecycle struct {
	UserID        string
	Login         string
	Status        string
	MFAEnrolled   bool
	PrimaryEmail  string
	CreatedAt     time.Time
	ActivatedAt   time.Time
	LastLoginAt   time.Time
	DeactivatedAt time.Time
	Result        Result
	Reason        string
	ObservedAt    time.Time
}

// API is the narrow surface Pull depends on.
type API interface {
	ListUsers(ctx context.Context) ([]rawUser, error)
	ListUserFactors(ctx context.Context, userID string) ([]rawFactor, error)
}

// Pull lists every user and reconciles MFA enrolment. Errors fetching one
// user's factors are recorded as MFAEnrolled=false with no fatal abort.
func Pull(ctx context.Context, api API, now func() time.Time) ([]Lifecycle, error) {
	if api == nil {
		return nil, errors.New("oktausers: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	users, err := api.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	out := make([]Lifecycle, 0, len(users))
	for _, u := range users {
		if u.ID == "" {
			continue
		}
		login := u.Profile.Login
		if login == "" {
			continue
		}
		l := Lifecycle{
			UserID:       u.ID,
			Login:        login,
			Status:       strings.ToUpper(strings.TrimSpace(u.Status)),
			PrimaryEmail: u.Profile.Email,
			ObservedAt:   now(),
		}
		l.CreatedAt = parseTime(u.Created)
		l.ActivatedAt = parseTime(u.Activated)
		l.LastLoginAt = parseTime(u.LastLogin)
		l.DeactivatedAt = parseTime(u.StatusChanged) // only meaningful when Status=DEPROVISIONED

		// Only ACTIVE users get a factor-list lookup. Inactive users
		// cannot meaningfully be "MFA enrolled" — Okta returns 404 on
		// /factors for some statuses. Avoid the extra round-trip.
		if l.Status == "ACTIVE" {
			factors, err := api.ListUserFactors(ctx, u.ID)
			if err == nil {
				l.MFAEnrolled = hasActiveMFAFactor(factors)
			}
		}
		l.Result, l.Reason = evaluate(l)
		out = append(out, l)
	}
	return out, nil
}

func evaluate(l Lifecycle) (Result, string) {
	switch l.Status {
	case "ACTIVE":
		if l.MFAEnrolled {
			return ResultPass, "active+mfa_enrolled"
		}
		return ResultFail, "active+mfa_unenrolled"
	case "DEPROVISIONED", "SUSPENDED", "LOCKED_OUT", "PASSWORD_EXPIRED":
		// Deactivated / suspended users SHOULD have entitlements stripped
		// downstream; the evaluator decides. The connector marks the
		// record FAIL to make stale entitlements visible.
		return ResultFail, "status=" + l.Status
	case "":
		return ResultInconclusive, "no status"
	default:
		// STAGED, PROVISIONED, RECOVERY — transitional.
		return ResultInconclusive, "status=" + l.Status
	}
}

func hasActiveMFAFactor(factors []rawFactor) bool {
	for _, f := range factors {
		// Recovery factors (email, password) don't count toward MFA.
		factorType := strings.ToLower(f.FactorType)
		if factorType == "" || strings.HasPrefix(factorType, "email") || factorType == "password" {
			continue
		}
		if strings.EqualFold(f.Status, "ACTIVE") {
			return true
		}
	}
	return false
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---- REST client ----

type rawUser struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	Created       string  `json:"created"`
	Activated     string  `json:"activated"`
	LastLogin     string  `json:"lastLogin"`
	StatusChanged string  `json:"statusChanged"`
	Profile       profile `json:"profile"`
}

type profile struct {
	Login string `json:"login"`
	Email string `json:"email"`
}

type rawFactor struct {
	ID         string `json:"id"`
	FactorType string `json:"factorType"`
	Status     string `json:"status"`
}

// Client is a thin HTTP client for the users endpoint family.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   oktaauth.Credential
}

// NewClient builds a Client.
func NewClient(httpClient *http.Client, baseURL string, creds oktaauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// ListUsers fetches the first page of users.
func (c *Client) ListUsers(ctx context.Context) ([]rawUser, error) {
	u := c.BaseURL + "/api/v1/users?limit=200"
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
	var out []rawUser
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode users: %w", err)
	}
	return out, nil
}

// ListUserFactors fetches the MFA factor list for one user.
func (c *Client) ListUserFactors(ctx context.Context, userID string) ([]rawFactor, error) {
	if userID == "" {
		return nil, errors.New("oktausers: userID required")
	}
	u := fmt.Sprintf("%s/api/v1/users/%s/factors", c.BaseURL, url.PathEscape(userID))
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
	if res.StatusCode == http.StatusNotFound {
		// No factors yet.
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var out []rawFactor
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode factors: %w", err)
	}
	return out, nil
}

// APIError carries Okta REST error context.
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
