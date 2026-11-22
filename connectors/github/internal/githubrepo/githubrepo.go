// Package githubrepo inspects org repos and the rules that protect their
// default branch, producing one ProtectionState per repository.
//
// A default branch can be protected two ways, and GitHub enforces both:
// classic branch protection (GET .../branches/{b}/protection) and
// rulesets, at the repository or organization level
// (GET .../rules/branches/{b}). Inspect reads both and merges them into
// the effective protection (#2925).
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
	"sort"
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

// Where a branch's protection comes from. Reported as the
// protection_source field of github.repo_protection.v2.
const (
	SourceNone     = "none"
	SourceClassic  = "classic"
	SourceRulesets = "rulesets"
	SourceBoth     = "classic_and_rulesets"
)

// ProtectionState is one record the cmd layer turns into an evidence
// record. Field names map 1:1 to the github.repo_protection.v2 schema.
//
// A nil pointer is a field the connector did not read. It is omitted from
// the payload, never reported as 0 or false (#2925).
type ProtectionState struct {
	RepoID                  int64
	RepoFullName            string
	DefaultBranch           string
	RequiredReviews         *int
	RequireCodeOwnerReviews *bool
	RequireSignedCommits    *bool
	RequireLinearHistory    *bool
	EnforceAdmins           *bool
	RequiredStatusChecks    *[]string // contexts, sorted and unique
	AllowForcePushes        *bool
	Topics                  *[]string // from the repository listing
	ProtectionSource        string    // "" when not read
	Result                  Result
	Reason                  string
	ObservedAt              time.Time
}

// API is the narrow surface the inspector needs. The concrete REST
// transport (Client) satisfies it; tests inject a fake.
type API interface {
	ListOrgRepos(ctx context.Context, org string) ([]Repo, error)
	GetBranchProtection(ctx context.Context, repoFullName, branch string) (*BranchProtection, error)
	GetBranch(ctx context.Context, repoFullName, branch string) (*Branch, error)
	GetBranchRules(ctx context.Context, repoFullName, branch string) ([]BranchRule, error)
	GetRuleset(ctx context.Context, repoFullName string, rulesetID int64) (*Ruleset, error)
}

// Repo is the subset of GET /orgs/{org}/repos we consume. ID is GitHub's
// stable repository id; it survives a rename, full_name does not.
// Permissions are the caller's own on the repository. GitHub's schema
// marks the object optional, so nil means not reported.
type Repo struct {
	ID            int64            `json:"id"`
	FullName      string           `json:"full_name"`
	DefaultBranch string           `json:"default_branch"`
	Private       bool             `json:"private"`
	Archived      bool             `json:"archived"`
	Permissions   *RepoPermissions `json:"permissions,omitempty"`
	// Topics is optional in GitHub's minimal-repository schema, so nil
	// means not reported, and [] means the repository has none.
	Topics *[]string `json:"topics,omitempty"`
}

// RepoPermissions is the subset of a minimal-repository's permissions
// object we consume.
type RepoPermissions struct {
	Admin bool `json:"admin"`
}

// Branch is the subset of GET /repos/{owner}/{repo}/branches/{branch} we
// consume.
type Branch struct {
	Protected *bool `json:"protected"`
}

// ErrBranchNotProtected is GetBranchProtection's answer when GitHub's 404
// says the branch has no classic protection, as opposed to a 404 for a
// repository or branch the token can't see.
var ErrBranchNotProtected = errors.New("github: branch not protected")

// branchNotProtected is the message of that 404. GitHub's REST description
// documents the 404 only as not_found and doesn't give this text, so a 404
// without it is cross-checked against GET .../branches/{b} (#2933).
const branchNotProtected = "Branch not protected"

// PullRequestReviews is the required_pull_request_reviews object of
// GET /repos/{owner}/{repo}/branches/{branch}/protection.
type PullRequestReviews struct {
	RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
	RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
}

// Enabled is the {url, enabled} object GitHub uses for several classic
// protection settings.
type Enabled struct {
	Enabled bool `json:"enabled"`
}

// BranchProtection is the subset of
// GET /repos/{owner}/{repo}/branches/{branch}/protection we consume.
type BranchProtection struct {
	RequiredPullRequestReviews *PullRequestReviews `json:"required_pull_request_reviews,omitempty"`
	RequiredSignatures         *Enabled            `json:"required_signatures,omitempty"`
	RequiredLinearHistory      *Enabled            `json:"required_linear_history,omitempty"`
	EnforceAdmins              *Enabled            `json:"enforce_admins,omitempty"`
	RequiredStatusChecks       *StatusChecks       `json:"required_status_checks,omitempty"`
	AllowForcePushes           *Enabled            `json:"allow_force_pushes,omitempty"`
}

// StatusChecks is the required_status_checks object of classic protection.
// GitHub's schema names both contexts and checks; its example sends only
// contexts, so the connector reads the union.
type StatusChecks struct {
	Contexts []string      `json:"contexts"`
	Checks   []StatusCheck `json:"checks"`
}

// StatusCheck is one required check, in classic protection's checks or a
// required_status_checks rule's parameters.
type StatusCheck struct {
	Context string `json:"context"`
}

// BranchRule is one element of GET /repos/{owner}/{repo}/rules/branches/{branch}:
// an active rule from a ruleset at any level, with the ruleset it came from.
type BranchRule struct {
	Type              string          `json:"type"`
	Parameters        json.RawMessage `json:"parameters,omitempty"`
	RulesetSourceType string          `json:"ruleset_source_type,omitempty"`
	RulesetSource     string          `json:"ruleset_source,omitempty"`
	RulesetID         int64           `json:"ruleset_id,omitempty"`
}

// Rule types we read. GitHub documents more; the rest don't bear on the
// fields this kind reports.
const (
	ruleTypePullRequest           = "pull_request"
	ruleTypeRequiredSignatures    = "required_signatures"
	ruleTypeRequiredLinearHistory = "required_linear_history"
	ruleTypeRequiredStatusChecks  = "required_status_checks"
	ruleTypeNonFastForward        = "non_fast_forward"
)

// statusChecksRuleParams is the subset of a required_status_checks rule's
// parameters we consume.
type statusChecksRuleParams struct {
	RequiredStatusChecks []StatusCheck `json:"required_status_checks"`
}

// pullRequestRuleParams is the subset of a pull_request rule's parameters
// we consume. Note the singular require_code_owner_review, unlike the
// classic API's require_code_owner_reviews.
type pullRequestRuleParams struct {
	RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
	RequireCodeOwnerReview       bool `json:"require_code_owner_review"`
}

// Ruleset is the subset of GET /repos/{owner}/{repo}/rulesets/{id} we
// consume. GitHub returns bypass_actors only to a caller with write access
// to the ruleset, and doesn't say whether it omits the field or sends []
// to anyone else. bypassActorsRead decides which lists are GitHub's
// answer. current_user_can_bypass is no evidence of write access: GitHub
// returns it to any caller of the repository-level endpoint.
type Ruleset struct {
	ID           int64          `json:"id"`
	SourceType   string         `json:"source_type,omitempty"`
	BypassActors *[]BypassActor `json:"bypass_actors,omitempty"`
}

// sourceTypeRepository is the repository-ruleset source_type of a ruleset
// configured on the repository itself.
const sourceTypeRepository = "Repository"

// BypassActor is one element of a ruleset's bypass_actors.
type BypassActor struct {
	ActorID    *int64 `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode,omitempty"`
}

// mayBeAdmin reports whether a bypass actor can be, or include, a
// repository or organization admin. GitHub's actor types are Integration,
// OrganizationAdmin, RepositoryRole, Team, DeployKey and User. Only an app
// (Integration) and a deploy key are never a person; a role, team or user
// may be an admin, and we don't resolve which.
func (a BypassActor) mayBeAdmin() bool {
	switch a.ActorType {
	case "Integration", "DeployKey":
		return false
	default:
		return true
	}
}

// Inspect lists every active repo in org and returns one ProtectionState
// per repo. A repo whose protection could not be read is recorded with
// Result=Inconclusive, a Reason and no protection fields, rather than
// aborting the run or reporting the unread fields as off.
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
		if r.Archived || r.ID == 0 || r.FullName == "" || r.DefaultBranch == "" {
			continue
		}
		out = append(out, inspectRepo(ctx, api, r, now()))
	}
	return out, nil
}

func inspectRepo(ctx context.Context, api API, r Repo, observedAt time.Time) ProtectionState {
	state := ProtectionState{
		RepoID:        r.ID,
		RepoFullName:  r.FullName,
		DefaultBranch: r.DefaultBranch,
		Topics:        r.Topics,
		ObservedAt:    observedAt,
	}
	unread := func(what string, err error) ProtectionState {
		state.Result = ResultInconclusive
		state.Reason = what + ": " + err.Error()
		return state
	}

	// GitHub documents only 200 and 404 for this endpoint, and returns 404
	// both for a branch without classic protection and for a token that
	// can't read it. Only the first means no classic protection; a ruleset
	// may still protect the branch.
	classic, err := api.GetBranchProtection(ctx, r.FullName, r.DefaultBranch)
	switch {
	case errors.Is(err, ErrBranchNotProtected):
		classic = nil
	case isNotFound(err):
		if !branchUnprotected(ctx, api, r) {
			return unread("branch protection", err)
		}
		classic = nil
	case err != nil:
		return unread("branch protection", err)
	}
	rules, err := api.GetBranchRules(ctx, r.FullName, r.DefaultBranch)
	if err != nil {
		return unread("branch rules", err)
	}

	m, err := merge(classic, rules)
	if err != nil {
		return unread("branch rules", err)
	}
	reviews := m.reviews
	state.RequiredReviews = &reviews
	state.RequireCodeOwnerReviews = &m.codeOwner
	state.RequireSignedCommits = &m.signed
	state.RequireLinearHistory = &m.linear
	checks := m.statusChecks()
	state.RequiredStatusChecks = &checks
	state.AllowForcePushes = m.allowForcePushes()
	state.EnforceAdmins = m.enforceAdmins(func(id int64) (bool, bool) {
		return rulesetBindsAdmins(ctx, api, r, id)
	})
	state.ProtectionSource = m.source()

	switch {
	case m.source() == SourceNone:
		state.Result = ResultFail
		state.Reason = "no branch protection or ruleset on default branch"
	default:
		state.Result, state.Reason = evaluate(reviews)
	}
	if state.EnforceAdmins == nil {
		state.Reason += "; admin enforcement unknown: a reviewing ruleset's bypass actors are not readable"
	}
	return state
}

// merged is the effective protection of one branch, across classic
// protection and every active ruleset. GitHub enforces each source on its
// own, so the strictest applies: the highest review count, and any source
// requiring a setting turns it on.
type merged struct {
	classic        *BranchProtection
	rulesetRules   bool
	reviews        int
	codeOwner      bool
	signed         bool
	linear         bool
	checks         map[string]bool // required status check contexts
	nonFastForward bool            // a non_fast_forward rule blocks force pushes
	reviewRulesets []int64         // rulesets with a pull_request rule requiring >= 1 review, sorted
}

func merge(classic *BranchProtection, rules []BranchRule) (merged, error) {
	m := merged{classic: classic, rulesetRules: len(rules) > 0, checks: map[string]bool{}}
	if classic != nil {
		if sc := classic.RequiredStatusChecks; sc != nil {
			for _, c := range sc.Contexts {
				m.addCheck(c)
			}
			for _, c := range sc.Checks {
				m.addCheck(c.Context)
			}
		}
		if pr := classic.RequiredPullRequestReviews; pr != nil {
			m.reviews = pr.RequiredApprovingReviewCount
			m.codeOwner = pr.RequireCodeOwnerReviews
		}
		m.signed = classic.RequiredSignatures != nil && classic.RequiredSignatures.Enabled
		m.linear = classic.RequiredLinearHistory != nil && classic.RequiredLinearHistory.Enabled
	}
	seen := map[int64]bool{}
	for _, rule := range rules {
		switch rule.Type {
		case ruleTypePullRequest:
			var p pullRequestRuleParams
			if len(rule.Parameters) > 0 {
				if err := json.Unmarshal(rule.Parameters, &p); err != nil {
					return merged{}, fmt.Errorf("decode pull_request rule of ruleset %d: %w", rule.RulesetID, err)
				}
			}
			if p.RequiredApprovingReviewCount > m.reviews {
				m.reviews = p.RequiredApprovingReviewCount
			}
			m.codeOwner = m.codeOwner || p.RequireCodeOwnerReview
			if p.RequiredApprovingReviewCount >= 1 && !seen[rule.RulesetID] {
				seen[rule.RulesetID] = true
				m.reviewRulesets = append(m.reviewRulesets, rule.RulesetID)
			}
		case ruleTypeRequiredSignatures:
			m.signed = true
		case ruleTypeRequiredLinearHistory:
			m.linear = true
		case ruleTypeRequiredStatusChecks:
			var p statusChecksRuleParams
			if err := json.Unmarshal(rule.Parameters, &p); err != nil {
				return merged{}, fmt.Errorf("decode required_status_checks rule of ruleset %d: %w", rule.RulesetID, err)
			}
			for _, c := range p.RequiredStatusChecks {
				m.addCheck(c.Context)
			}
		case ruleTypeNonFastForward:
			m.nonFastForward = true
		}
	}
	sort.Slice(m.reviewRulesets, func(i, j int) bool { return m.reviewRulesets[i] < m.reviewRulesets[j] })
	return m, nil
}

func (m merged) addCheck(context string) {
	if context != "" {
		m.checks[context] = true
	}
}

// statusChecks is every required status check context, from classic
// protection and every required_status_checks rule, sorted. Empty when no
// source requires one.
func (m merged) statusChecks() []string {
	out := make([]string, 0, len(m.checks))
	for c := range m.checks {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// allowForcePushes reports whether force pushes to the branch are allowed:
// false when classic protection disables them or a non_fast_forward rule
// blocks them, true when no source blocks them, and nil when classic
// protection omits allow_force_pushes and no rule decides it. Who may
// bypass the block (classic admins, ruleset bypass actors) is not read.
func (m merged) allowForcePushes() *bool {
	t, f := true, false
	if m.nonFastForward {
		return &f
	}
	if m.classic == nil {
		return &t
	}
	if m.classic.AllowForcePushes == nil {
		return nil
	}
	allowed := m.classic.AllowForcePushes.Enabled
	return &allowed
}

func (m merged) source() string {
	switch {
	case m.classic != nil && m.rulesetRules:
		return SourceBoth
	case m.classic != nil:
		return SourceClassic
	case m.rulesetRules:
		return SourceRulesets
	default:
		return SourceNone
	}
}

// branchUnprotected cross-checks a classic-protection 404 that isn't
// GitHub's "Branch not protected": true only when GET .../branches/{b}
// reads protected=false. Any other answer leaves the 404 unexplained.
func branchUnprotected(ctx context.Context, api API, r Repo) bool {
	b, err := api.GetBranch(ctx, r.FullName, r.DefaultBranch)
	return err == nil && b != nil && b.Protected != nil && !*b.Protected
}

// rulesetBindsAdmins reads whether a ruleset's rules bind admins: no
// bypass actor may be an admin. ok is false when that can't be read (an
// error, a missing id, or bypass_actors withheld or not provably read).
func rulesetBindsAdmins(ctx context.Context, api API, repo Repo, id int64) (binds, ok bool) {
	if id == 0 {
		return false, false
	}
	rs, err := api.GetRuleset(ctx, repo.FullName, id)
	if err != nil || rs == nil || !bypassActorsRead(rs, repo) {
		return false, false
	}
	for _, a := range *rs.BypassActors {
		if a.mayBeAdmin() {
			return false, true
		}
	}
	return true, true
}

// bypassActorsRead reports whether rs.BypassActors is GitHub's answer
// rather than a withheld one. A non-empty list is, since GitHub sends one
// only to a caller with write access. An empty list may be either, so it
// counts only for a repository ruleset on a repository the caller
// administers: repository admins can edit its rulesets. An empty list on
// an organization or enterprise ruleset stays unread (#2933).
func bypassActorsRead(rs *Ruleset, repo Repo) bool {
	switch {
	case rs.BypassActors == nil:
		return false
	case len(*rs.BypassActors) > 0:
		return true
	default:
		return rs.SourceType == sourceTypeRepository && repo.Permissions != nil && repo.Permissions.Admin
	}
}

// enforceAdmins reports whether a required approving review binds admins:
// true when at least one source requiring a review does (classic
// enforce_admins, or a ruleset no admin can bypass), false when every such
// source is read and none does, and nil when that depends on a ruleset
// whose bypass actors we could not read. With no source requiring a
// review, it is classic enforce_admins, or false when there is no classic
// protection.
//
// Rulesets are read only until one binds, so a branch whose classic
// protection already binds admins costs no ruleset request.
func (m merged) enforceAdmins(bindsAdmins func(rulesetID int64) (binds, ok bool)) *bool {
	t, f := true, false
	classicBinds := m.classic != nil && m.classic.EnforceAdmins != nil && m.classic.EnforceAdmins.Enabled
	classicReviews := m.classic != nil && m.classic.RequiredPullRequestReviews != nil &&
		m.classic.RequiredPullRequestReviews.RequiredApprovingReviewCount >= 1
	if classicReviews && classicBinds {
		return &t
	}
	unknown := false
	for _, id := range m.reviewRulesets {
		binds, ok := bindsAdmins(id)
		switch {
		case !ok:
			unknown = true
		case binds:
			return &t
		}
	}
	if unknown {
		return nil
	}
	if !classicReviews && len(m.reviewRulesets) == 0 {
		return &classicBinds
	}
	return &f
}

// evaluate computes the connector's own verdict. v1 rule:
// required_reviews >= 1 passes; anything else fails. Slice 044
// deliberately keeps this rule shallow so the evaluator (slice 015) owns
// the policy ladder, not the connector.
func evaluate(reviews int) (Result, string) {
	if reviews >= 1 {
		return ResultPass, fmt.Sprintf("required_reviews=%d", reviews)
	}
	return ResultFail, "required_reviews=0"
}

// ---- REST client ----

// maxPages bounds how many Link: rel="next" pages one listing follows.
// At GitHub's per_page maximum of 100 that is 100,000 items.
const maxPages = 1000

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

// ListOrgRepos returns every repo under org, following GitHub's
// Link: rel="next" pagination. Archived repos are returned; Inspect
// skips them.
func (c *Client) ListOrgRepos(ctx context.Context, org string) ([]Repo, error) {
	u := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&type=all", c.BaseURL, url.PathEscape(org))
	var out []Repo
	err := c.getPages(ctx, u, func(body io.Reader) error {
		var page []Repo
		if err := json.NewDecoder(body).Decode(&page); err != nil {
			return fmt.Errorf("decode repos: %w", err)
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

// GetBranchProtection returns the classic protection of one branch. A 404
// whose message is "Branch not protected" returns ErrBranchNotProtected;
// any other 404 is an APIError carrying the body.
func (c *Client) GetBranchProtection(ctx context.Context, repoFullName, branch string) (*BranchProtection, error) {
	if repoFullName == "" || branch == "" {
		return nil, errors.New("githubrepo: repo + branch required")
	}
	u := fmt.Sprintf("%s/repos/%s/branches/%s/protection", c.BaseURL, repoFullName, url.PathEscape(branch))
	var bp BranchProtection
	if err := c.getJSON(ctx, u, &bp, "protection"); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound && errorMessage(ae.Body) == branchNotProtected {
			return nil, ErrBranchNotProtected
		}
		return nil, err
	}
	return &bp, nil
}

// GetBranch returns one branch, whose protected flag cross-checks a
// classic-protection 404.
func (c *Client) GetBranch(ctx context.Context, repoFullName, branch string) (*Branch, error) {
	if repoFullName == "" || branch == "" {
		return nil, errors.New("githubrepo: repo + branch required")
	}
	u := fmt.Sprintf("%s/repos/%s/branches/%s", c.BaseURL, repoFullName, url.PathEscape(branch))
	var b Branch
	if err := c.getJSON(ctx, u, &b, "branch"); err != nil {
		return nil, err
	}
	return &b, nil
}

// GetBranchRules returns every active ruleset rule that applies to the
// branch, at any level, following Link: rel="next" pagination.
func (c *Client) GetBranchRules(ctx context.Context, repoFullName, branch string) ([]BranchRule, error) {
	if repoFullName == "" || branch == "" {
		return nil, errors.New("githubrepo: repo + branch required")
	}
	u := fmt.Sprintf("%s/repos/%s/rules/branches/%s?per_page=100", c.BaseURL, repoFullName, url.PathEscape(branch))
	out := []BranchRule{}
	err := c.getPages(ctx, u, func(body io.Reader) error {
		var page []BranchRule
		if err := json.NewDecoder(body).Decode(&page); err != nil {
			return fmt.Errorf("decode rules: %w", err)
		}
		out = append(out, page...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetRuleset returns one ruleset that applies to the repository, including
// one configured at the organization level (includes_parents defaults to
// true).
func (c *Client) GetRuleset(ctx context.Context, repoFullName string, rulesetID int64) (*Ruleset, error) {
	if repoFullName == "" || rulesetID == 0 {
		return nil, errors.New("githubrepo: repo + ruleset id required")
	}
	u := fmt.Sprintf("%s/repos/%s/rulesets/%d", c.BaseURL, repoFullName, rulesetID)
	var rs Ruleset
	if err := c.getJSON(ctx, u, &rs, "ruleset"); err != nil {
		return nil, err
	}
	return &rs, nil
}

func (c *Client) getJSON(ctx context.Context, u string, into any, what string) error {
	res, err := c.do(ctx, u)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		return fmt.Errorf("decode %s: %w", what, err)
	}
	return nil
}

// getPages GETs u and every page its Link: rel="next" chain names,
// handing each 200 body to decode. A next link must stay on BaseURL's
// scheme and host, so the credential is never sent elsewhere.
func (c *Client) getPages(ctx context.Context, u string, decode func(io.Reader) error) error {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("githubrepo: base url: %w", err)
	}
	for page := 0; u != ""; page++ {
		if page >= maxPages {
			return fmt.Errorf("githubrepo: more than %d pages at %s", maxPages, u)
		}
		res, err := c.do(ctx, u)
		if err != nil {
			return err
		}
		if res.StatusCode != http.StatusOK {
			ae := &APIError{Status: res.StatusCode, Body: drain(res.Body)}
			_ = res.Body.Close()
			return ae
		}
		err = decode(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return err
		}
		next := nextLink(res.Header.Values("Link"))
		if next == "" {
			return nil
		}
		nu, err := url.Parse(next)
		if err != nil {
			return fmt.Errorf("githubrepo: next link %q: %w", next, err)
		}
		if nu.Scheme != base.Scheme || nu.Host != base.Host {
			return fmt.Errorf("githubrepo: next link %q leaves %s", next, c.BaseURL)
		}
		u = nu.String()
	}
	return nil
}

func (c *Client) do(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	return c.HTTP.Do(req)
}

// nextLink returns the rel="next" target of an RFC 8288 Link header as
// GitHub sends it (`<url>; rel="next", <url>; rel="last"`), or "".
func nextLink(headers []string) string {
	for _, h := range headers {
		for _, part := range strings.Split(h, ",") {
			segs := strings.Split(part, ";")
			target := strings.TrimSpace(segs[0])
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			for _, p := range segs[1:] {
				k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(k), "rel") {
					continue
				}
				for _, rel := range strings.Fields(strings.Trim(strings.TrimSpace(v), `"`)) {
					if rel == "next" {
						return target[1 : len(target)-1]
					}
				}
			}
		}
	}
	return ""
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

// errorMessage is the message field of a GitHub error body, or "".
func errorMessage(body string) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &e) != nil {
		return ""
	}
	return e.Message
}

func drain(r io.Reader) string {
	const max = 1 << 13 // 8 KiB cap on captured error bodies
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
