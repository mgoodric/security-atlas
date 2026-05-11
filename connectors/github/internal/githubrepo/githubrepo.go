// Package githubrepo inspects org repos and their default-branch
// protection rules, producing one ProtectionState per repository.
//
// The package is built around the small API interface so tests can swap
// in an httptest.NewServer-backed transport without depending on
// google/go-github (which we deliberately avoid pulling in for slice 044
// to keep the connector's go.sum lean).
package githubrepo

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

// Result mirrors the slice 014 schema's pass/fail intent.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// ProtectionState is one record the cmd layer turns into an evidence
// record. Field names map 1:1 to github.repo_protection.v1 schema.
type ProtectionState struct {
	RepoFullName            string
	DefaultBranch           string
	RequiredReviews         int
	RequireCodeOwnerReviews bool
	RequireSignedCommits    bool
	RequireLinearHistory    bool
	EnforceAdmins           bool
	Result                  Result
	Reason                  string
	ObservedAt              time.Time
}

// API is the narrow surface the inspector needs. The concrete REST
// transport (Client) satisfies it; tests inject a fake.
type API interface {
	ListOrgRepos(ctx context.Context, org string) ([]Repo, error)
	GetBranchProtection(ctx context.Context, repoFullName, branch string) (*BranchProtection, error)
}

// Repo is the subset of GET /orgs/{org}/repos we consume.
type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
}

// BranchProtection is the subset of
// GET /repos/{owner}/{repo}/branches/{branch}/protection we consume.
type BranchProtection struct {
	RequiredPullRequestReviews *struct {
		RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
		RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
	} `json:"required_pull_request_reviews,omitempty"`
	RequiredSignatures *struct {
		Enabled bool `json:"enabled"`
	} `json:"required_signatures,omitempty"`
	RequiredLinearHistory *struct {
		Enabled bool `json:"enabled"`
	} `json:"required_linear_history,omitempty"`
	EnforceAdmins *struct {
		Enabled bool `json:"enabled"`
	} `json:"enforce_admins,omitempty"`
}

// Inspect lists every active repo in org and returns one ProtectionState
// per repo. Errors fetching one repo's protection are recorded on the
// state (Result=Inconclusive, Reason=...) rather than aborting the run.
func Inspect(ctx context.Context, api API, org string, now func() time.Time) ([]ProtectionState, error) {
	if api == nil {
		return nil, errors.New("githubrepo: API is nil")
	}
	if org == "" {
		return nil, errors.New("githubrepo: org is required")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	repos, err := api.ListOrgRepos(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("list org repos: %w", err)
	}
	out := make([]ProtectionState, 0, len(repos))
	for _, r := range repos {
		if r.Archived || r.FullName == "" || r.DefaultBranch == "" {
			continue
		}
		state := ProtectionState{
			RepoFullName:  r.FullName,
			DefaultBranch: r.DefaultBranch,
			ObservedAt:    now(),
		}
		bp, err := api.GetBranchProtection(ctx, r.FullName, r.DefaultBranch)
		switch {
		case isNotFound(err):
			// 404 here means "no protection configured" — that is a
			// definitive FAIL, not inconclusive.
			state.Result = ResultFail
			state.Reason = "no branch protection configured on default branch"
		case err != nil:
			state.Result = ResultInconclusive
			state.Reason = err.Error()
		default:
			fill(&state, bp)
			state.Result, state.Reason = evaluate(state)
		}
		out = append(out, state)
	}
	return out, nil
}

func fill(s *ProtectionState, bp *BranchProtection) {
	if bp == nil {
		return
	}
	if bp.RequiredPullRequestReviews != nil {
		s.RequiredReviews = bp.RequiredPullRequestReviews.RequiredApprovingReviewCount
		s.RequireCodeOwnerReviews = bp.RequiredPullRequestReviews.RequireCodeOwnerReviews
	}
	if bp.RequiredSignatures != nil {
		s.RequireSignedCommits = bp.RequiredSignatures.Enabled
	}
	if bp.RequiredLinearHistory != nil {
		s.RequireLinearHistory = bp.RequiredLinearHistory.Enabled
	}
	if bp.EnforceAdmins != nil {
		s.EnforceAdmins = bp.EnforceAdmins.Enabled
	}
}

// evaluate computes pass/fail per AC-3. v1 rule: required_reviews >= 1
// passes; anything else fails. Slice 044 deliberately keeps this rule
// shallow so the evaluator (slice 015) owns the policy ladder, not the
// connector.
func evaluate(s ProtectionState) (Result, string) {
	if s.RequiredReviews >= 1 {
		return ResultPass, fmt.Sprintf("required_reviews=%d", s.RequiredReviews)
	}
	return ResultFail, "required_reviews=0"
}

// ---- REST client ----

// Client is a thin wrapper around http.Client carrying GitHub auth +
// JSON decoding. Tests construct one against an httptest server.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   githubauth.Credential
}

// NewClient builds a Client against the public GitHub REST API. baseURL
// allows tests to override.
func NewClient(httpClient *http.Client, baseURL string, creds githubauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// ListOrgRepos returns every non-archived repo under org. v1 pulls only
// the first 100 — pagination lands when org sizes demand it.
func (c *Client) ListOrgRepos(ctx context.Context, org string) ([]Repo, error) {
	u := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&type=all", c.BaseURL, url.PathEscape(org))
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
	var out []Repo
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode repos: %w", err)
	}
	return out, nil
}

// GetBranchProtection returns the protection state of one branch. A 404
// indicates no protection — surfaced through APIError so the caller can
// distinguish from other errors.
func (c *Client) GetBranchProtection(ctx context.Context, repoFullName, branch string) (*BranchProtection, error) {
	if repoFullName == "" || branch == "" {
		return nil, errors.New("githubrepo: repo + branch required")
	}
	u := fmt.Sprintf("%s/repos/%s/branches/%s/protection", c.BaseURL, repoFullName, url.PathEscape(branch))
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
		return nil, &APIError{Status: 404}
	}
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var bp BranchProtection
	if err := json.NewDecoder(res.Body).Decode(&bp); err != nil {
		return nil, fmt.Errorf("decode protection: %w", err)
	}
	return &bp, nil
}

// APIError carries GitHub REST error context. The Body is bounded.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("github: HTTP %d", e.Status)
	}
	return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Body)
}

func isNotFound(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusNotFound
	}
	return false
}

func drain(r io.Reader) string {
	const max = 1 << 13 // 8 KiB cap on captured error bodies
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
