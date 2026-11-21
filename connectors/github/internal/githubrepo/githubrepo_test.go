package githubrepo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
)

// fakeAPI is the in-process fake used by behavior-level tests so the
// merge and verdict logic and the HTTP layer can be tested independently.
type fakeAPI struct {
	repos      []githubrepo.Repo
	prot       map[string]*githubrepo.BranchProtection
	notFound   map[string]bool
	errOn      map[string]error
	rules      map[string][]githubrepo.BranchRule
	rulesErrOn map[string]error
	rulesets   map[int64]*githubrepo.Ruleset
}

func (f *fakeAPI) ListOrgRepos(_ context.Context, _ string) ([]githubrepo.Repo, error) {
	return f.repos, nil
}

func (f *fakeAPI) GetBranchProtection(_ context.Context, repo, _ string) (*githubrepo.BranchProtection, error) {
	if e := f.errOn[repo]; e != nil {
		return nil, e
	}
	if f.notFound[repo] {
		return nil, &githubrepo.APIError{Status: http.StatusNotFound}
	}
	return f.prot[repo], nil
}

func (f *fakeAPI) GetBranchRules(_ context.Context, repo, _ string) ([]githubrepo.BranchRule, error) {
	if e := f.rulesErrOn[repo]; e != nil {
		return nil, e
	}
	return f.rules[repo], nil
}

func (f *fakeAPI) GetRuleset(_ context.Context, _ string, id int64) (*githubrepo.Ruleset, error) {
	rs, ok := f.rulesets[id]
	if !ok {
		return nil, &githubrepo.APIError{Status: http.StatusNotFound}
	}
	return rs, nil
}

func reviews(n int, codeOwner bool) *githubrepo.PullRequestReviews {
	return &githubrepo.PullRequestReviews{RequiredApprovingReviewCount: n, RequireCodeOwnerReviews: codeOwner}
}

func prRule(rulesetID int64, n int) githubrepo.BranchRule {
	return githubrepo.BranchRule{
		Type:       "pull_request",
		RulesetID:  rulesetID,
		Parameters: json.RawMessage(fmt.Sprintf(`{"required_approving_review_count":%d,"require_code_owner_review":false}`, n)),
	}
}

func bypass(actors ...githubrepo.BypassActor) *githubrepo.Ruleset {
	list := append([]githubrepo.BypassActor{}, actors...)
	return &githubrepo.Ruleset{BypassActors: &list}
}

func inspectOne(t *testing.T, api *fakeAPI) githubrepo.ProtectionState {
	t.Helper()
	got, err := githubrepo.Inspect(context.Background(), api, "org", nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(states) = %d; want 1", len(got))
	}
	return got[0]
}

func boolVal(p *bool) string {
	if p == nil {
		return "unread"
	}
	return fmt.Sprint(*p)
}

func intVal(p *int) string {
	if p == nil {
		return "unread"
	}
	return fmt.Sprint(*p)
}

var repoA = githubrepo.Repo{ID: 101, FullName: "org/a", DefaultBranch: "main"}

func TestInspect_PassWhenReviewsRequired(t *testing.T) {
	s := inspectOne(t, &fakeAPI{
		repos: []githubrepo.Repo{repoA},
		prot:  map[string]*githubrepo.BranchProtection{"org/a": {RequiredPullRequestReviews: reviews(1, true)}},
	})
	if s.Result != githubrepo.ResultPass {
		t.Errorf("result = %q; want pass", s.Result)
	}
	if boolVal(s.RequireCodeOwnerReviews) != "true" {
		t.Errorf("RequireCodeOwnerReviews = %s; want true", boolVal(s.RequireCodeOwnerReviews))
	}
	if s.RepoID != 101 {
		t.Errorf("RepoID = %d; want 101", s.RepoID)
	}
	if s.ProtectionSource != githubrepo.SourceClassic {
		t.Errorf("ProtectionSource = %q; want classic", s.ProtectionSource)
	}
}

func TestInspect_FailWhenReviewsZero(t *testing.T) {
	s := inspectOne(t, &fakeAPI{
		repos: []githubrepo.Repo{repoA},
		prot:  map[string]*githubrepo.BranchProtection{"org/a": {RequiredPullRequestReviews: reviews(0, false)}},
	})
	if s.Result != githubrepo.ResultFail || intVal(s.RequiredReviews) != "0" {
		t.Fatalf("result = %q reviews = %s; want fail, 0", s.Result, intVal(s.RequiredReviews))
	}
}

// 404 from classic protection and no ruleset rule: nothing protects the
// branch. Every field is read, and is off.
func TestInspect_FailWhenNothingProtects(t *testing.T) {
	s := inspectOne(t, &fakeAPI{
		repos:    []githubrepo.Repo{repoA},
		notFound: map[string]bool{"org/a": true},
	})
	if s.Result != githubrepo.ResultFail {
		t.Fatalf("result = %q; want fail", s.Result)
	}
	if s.ProtectionSource != githubrepo.SourceNone {
		t.Errorf("ProtectionSource = %q; want none", s.ProtectionSource)
	}
	if intVal(s.RequiredReviews) != "0" || boolVal(s.EnforceAdmins) != "false" {
		t.Errorf("reviews = %s enforce_admins = %s; want 0, false", intVal(s.RequiredReviews), boolVal(s.EnforceAdmins))
	}
}

// #2925 item 1: a failed fetch is inconclusive with every protection
// field unread, never zeros.
func TestInspect_FetchErrorLeavesFieldsUnread(t *testing.T) {
	cases := map[string]*fakeAPI{
		"classic protection 500": {
			repos: []githubrepo.Repo{repoA},
			errOn: map[string]error{"org/a": &githubrepo.APIError{Status: 500, Body: "boom"}},
		},
		"classic protection 403": {
			repos: []githubrepo.Repo{repoA},
			errOn: map[string]error{"org/a": &githubrepo.APIError{Status: 403}},
		},
		"branch rules 500": {
			repos:      []githubrepo.Repo{repoA},
			prot:       map[string]*githubrepo.BranchProtection{"org/a": {RequiredPullRequestReviews: reviews(1, false)}},
			rulesErrOn: map[string]error{"org/a": &githubrepo.APIError{Status: 500}},
		},
		"malformed pull_request rule": {
			repos:    []githubrepo.Repo{repoA},
			notFound: map[string]bool{"org/a": true},
			rules: map[string][]githubrepo.BranchRule{"org/a": {{
				Type: "pull_request", RulesetID: 7, Parameters: json.RawMessage(`{"required_approving_review_count":"two"}`),
			}}},
		},
	}
	for name, api := range cases {
		t.Run(name, func(t *testing.T) {
			s := inspectOne(t, api)
			if s.Result != githubrepo.ResultInconclusive {
				t.Errorf("result = %q; want inconclusive", s.Result)
			}
			if s.Reason == "" {
				t.Error("reason is empty")
			}
			if s.RequiredReviews != nil || s.RequireCodeOwnerReviews != nil || s.RequireSignedCommits != nil ||
				s.RequireLinearHistory != nil || s.EnforceAdmins != nil || s.ProtectionSource != "" {
				t.Errorf("unread repo carries protection fields: %+v", s)
			}
			if s.RepoID != 101 || s.RepoFullName != "org/a" {
				t.Errorf("identity = %d %q; want 101 org/a", s.RepoID, s.RepoFullName)
			}
		})
	}
}

// #2925 item 2: a repo protected only by a ruleset is read as protected,
// and admin enforcement follows the ruleset's bypass actors.
func TestInspect_RulesetOnly(t *testing.T) {
	cases := []struct {
		name    string
		ruleset *githubrepo.Ruleset
		want    string
	}{
		{"nobody bypasses", bypass(), "true"},
		{"only an app and a deploy key bypass", bypass(
			githubrepo.BypassActor{ActorType: "Integration"},
			githubrepo.BypassActor{ActorType: "DeployKey"},
		), "true"},
		{"org admins bypass", bypass(githubrepo.BypassActor{ActorType: "OrganizationAdmin"}), "false"},
		{"a repository role bypasses", bypass(githubrepo.BypassActor{ActorType: "RepositoryRole"}), "false"},
		{"a team bypasses on pull requests", bypass(githubrepo.BypassActor{ActorType: "Team", BypassMode: "pull_request"}), "false"},
		{"a user bypasses", bypass(githubrepo.BypassActor{ActorType: "User"}), "false"},
		{"bypass actors withheld", &githubrepo.Ruleset{}, "unread"},
		{"ruleset not readable", nil, "unread"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{
				repos:    []githubrepo.Repo{repoA},
				notFound: map[string]bool{"org/a": true},
				rules:    map[string][]githubrepo.BranchRule{"org/a": {prRule(9, 2)}},
				rulesets: map[int64]*githubrepo.Ruleset{},
			}
			if tc.ruleset != nil {
				api.rulesets[9] = tc.ruleset
			}
			s := inspectOne(t, api)
			if s.Result != githubrepo.ResultPass || intVal(s.RequiredReviews) != "2" {
				t.Errorf("result = %q reviews = %s; want pass, 2", s.Result, intVal(s.RequiredReviews))
			}
			if s.ProtectionSource != githubrepo.SourceRulesets {
				t.Errorf("ProtectionSource = %q; want rulesets", s.ProtectionSource)
			}
			if got := boolVal(s.EnforceAdmins); got != tc.want {
				t.Errorf("enforce_admins = %s; want %s", got, tc.want)
			}
			if tc.want == "unread" && !strings.Contains(s.Reason, "admin enforcement unknown") {
				t.Errorf("reason = %q; want it to say admin enforcement is unknown", s.Reason)
			}
		})
	}
}

// Classic protection and rulesets are both enforced, so the strictest
// setting applies.
func TestInspect_MergesClassicAndRulesets(t *testing.T) {
	t.Run("ruleset raises the review count and binds admins", func(t *testing.T) {
		s := inspectOne(t, &fakeAPI{
			repos: []githubrepo.Repo{repoA},
			prot: map[string]*githubrepo.BranchProtection{"org/a": {
				RequiredPullRequestReviews: reviews(1, false),
				EnforceAdmins:              &githubrepo.Enabled{Enabled: false},
			}},
			rules: map[string][]githubrepo.BranchRule{"org/a": {
				prRule(9, 3),
				{Type: "required_linear_history", RulesetID: 9},
			}},
			rulesets: map[int64]*githubrepo.Ruleset{9: bypass()},
		})
		if intVal(s.RequiredReviews) != "3" || boolVal(s.EnforceAdmins) != "true" || boolVal(s.RequireLinearHistory) != "true" {
			t.Errorf("reviews=%s enforce_admins=%s linear=%s; want 3 true true",
				intVal(s.RequiredReviews), boolVal(s.EnforceAdmins), boolVal(s.RequireLinearHistory))
		}
		if s.ProtectionSource != githubrepo.SourceBoth {
			t.Errorf("ProtectionSource = %q; want classic_and_rulesets", s.ProtectionSource)
		}
	})
	t.Run("classic binds admins, ruleset bypass unreadable", func(t *testing.T) {
		s := inspectOne(t, &fakeAPI{
			repos: []githubrepo.Repo{repoA},
			prot: map[string]*githubrepo.BranchProtection{"org/a": {
				RequiredPullRequestReviews: reviews(1, false),
				EnforceAdmins:              &githubrepo.Enabled{Enabled: true},
			}},
			rules: map[string][]githubrepo.BranchRule{"org/a": {prRule(9, 1)}},
		})
		if boolVal(s.EnforceAdmins) != "true" {
			t.Errorf("enforce_admins = %s; want true", boolVal(s.EnforceAdmins))
		}
	})
	t.Run("neither binds admins", func(t *testing.T) {
		s := inspectOne(t, &fakeAPI{
			repos: []githubrepo.Repo{repoA},
			prot: map[string]*githubrepo.BranchProtection{"org/a": {
				RequiredPullRequestReviews: reviews(1, false),
				EnforceAdmins:              &githubrepo.Enabled{Enabled: false},
			}},
			rules:    map[string][]githubrepo.BranchRule{"org/a": {prRule(9, 1)}},
			rulesets: map[int64]*githubrepo.Ruleset{9: bypass(githubrepo.BypassActor{ActorType: "OrganizationAdmin"})},
		})
		if boolVal(s.EnforceAdmins) != "false" {
			t.Errorf("enforce_admins = %s; want false", boolVal(s.EnforceAdmins))
		}
	})
	t.Run("ruleset with no review rule", func(t *testing.T) {
		s := inspectOne(t, &fakeAPI{
			repos:    []githubrepo.Repo{repoA},
			notFound: map[string]bool{"org/a": true},
			rules:    map[string][]githubrepo.BranchRule{"org/a": {{Type: "non_fast_forward", RulesetID: 9}}},
		})
		if s.Result != githubrepo.ResultFail || intVal(s.RequiredReviews) != "0" || boolVal(s.EnforceAdmins) != "false" {
			t.Errorf("result=%q reviews=%s enforce_admins=%s; want fail 0 false",
				s.Result, intVal(s.RequiredReviews), boolVal(s.EnforceAdmins))
		}
		if s.ProtectionSource != githubrepo.SourceRulesets {
			t.Errorf("ProtectionSource = %q; want rulesets", s.ProtectionSource)
		}
	})
}

func TestInspect_SkipsArchivedAndIDless(t *testing.T) {
	s := inspectOne(t, &fakeAPI{
		repos: []githubrepo.Repo{
			repoA,
			{ID: 102, FullName: "org/dead", DefaultBranch: "main", Archived: true},
			{FullName: "org/noid", DefaultBranch: "main"},
		},
		prot: map[string]*githubrepo.BranchProtection{"org/a": {RequiredPullRequestReviews: reviews(1, false)}},
	})
	if s.RepoFullName != "org/a" {
		t.Fatalf("RepoFullName = %q; want org/a", s.RepoFullName)
	}
}

func TestInspect_RejectsEmptyOrg(t *testing.T) {
	_, err := githubrepo.Inspect(context.Background(), &fakeAPI{}, "", nil)
	if err == nil {
		t.Fatal("expected error on empty org")
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func newClient(t *testing.T, srv *httptest.Server) *githubrepo.Client {
	t.Helper()
	creds, err := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return githubrepo.NewClient(srv.Client(), srv.URL, creds)
}

// TestClient_DocumentedShapes runs the REST client against GitHub's
// documented example responses (testdata/README.md), satisfying the
// orchestrator's "do not mock the GitHub API in a way that hides
// contract bugs" directive.
func TestClient_DocumentedShapes(t *testing.T) {
	cases := []struct {
		name           string
		classic        string // fixture, or "" for 404
		rules          string // fixture, or "" for []
		reviews        string
		enforceAdmins  string
		codeOwner      string
		signed         string
		linear         string
		source         string
		wantResult     githubrepo.Result
		wantRulesetGET bool
	}{
		{"classic only", "branch_protection.json", "", "2", "true", "true", "true", "true", githubrepo.SourceClassic, githubrepo.ResultPass, false},
		// The documented ruleset lets a Team bypass, which may hold an admin.
		{"org ruleset only", "", "branch_rules.json", "2", "false", "true", "true", "false", githubrepo.SourceRulesets, githubrepo.ResultPass, true},
		{"both", "branch_protection.json", "branch_rules.json", "2", "true", "true", "true", "true", githubrepo.SourceBoth, githubrepo.ResultPass, false},
		{"neither", "", "", "0", "false", "false", "false", "false", githubrepo.SourceNone, githubrepo.ResultFail, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rulesetGETs := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/orgs/octocat/repos", func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
					t.Errorf("Authorization header missing; got %q", got)
				}
				if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
					t.Errorf("Accept = %q", got)
				}
				_, _ = w.Write(fixture(t, "org_repos.json"))
			})
			mux.HandleFunc("/repos/octocat/Hello-World/branches/master/protection", func(w http.ResponseWriter, _ *http.Request) {
				if tc.classic == "" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"Branch not protected"}`))
					return
				}
				_, _ = w.Write(fixture(t, tc.classic))
			})
			mux.HandleFunc("/repos/octocat/Hello-World/rules/branches/master", func(w http.ResponseWriter, _ *http.Request) {
				if tc.rules == "" {
					_, _ = w.Write([]byte(`[]`))
					return
				}
				_, _ = w.Write(fixture(t, tc.rules))
			})
			mux.HandleFunc("/repos/octocat/Hello-World/rulesets/73", func(w http.ResponseWriter, _ *http.Request) {
				rulesetGETs++
				_, _ = w.Write(fixture(t, "ruleset.json"))
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			got, err := githubrepo.Inspect(context.Background(), newClient(t, srv), "octocat", func() time.Time {
				return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
			})
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len = %d; want 1", len(got))
			}
			s := got[0]
			if s.RepoID != 1296269 || s.RepoFullName != "octocat/Hello-World" || s.DefaultBranch != "master" {
				t.Errorf("identity = %d %q %q", s.RepoID, s.RepoFullName, s.DefaultBranch)
			}
			checks := map[string][2]string{
				"required_reviews":           {intVal(s.RequiredReviews), tc.reviews},
				"enforce_admins":             {boolVal(s.EnforceAdmins), tc.enforceAdmins},
				"require_code_owner_reviews": {boolVal(s.RequireCodeOwnerReviews), tc.codeOwner},
				"require_signed_commits":     {boolVal(s.RequireSignedCommits), tc.signed},
				"require_linear_history":     {boolVal(s.RequireLinearHistory), tc.linear},
				"protection_source":          {s.ProtectionSource, tc.source},
				"result":                     {string(s.Result), string(tc.wantResult)},
			}
			for k, v := range checks {
				if v[0] != v[1] {
					t.Errorf("%s = %s; want %s", k, v[0], v[1])
				}
			}
			if (rulesetGETs > 0) != tc.wantRulesetGET {
				t.Errorf("ruleset GETs = %d; want any=%v", rulesetGETs, tc.wantRulesetGET)
			}
		})
	}
}

// #2925 item 3: ListOrgRepos follows Link: rel="next" past the first page.
func TestClient_ListOrgReposFollowsNextLink(t *testing.T) {
	var srv *httptest.Server
	pages := map[string]string{
		"1": `[{"id":1,"full_name":"org/r1","default_branch":"main"}]`,
		"2": `[{"id":2,"full_name":"org/r2","default_branch":"main"}]`,
		"3": `[{"id":3,"full_name":"org/r3","default_branch":"main"}]`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/org/repos", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got == "" {
			t.Errorf("page %s sent without credentials", r.URL.Query().Get("page"))
		}
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		switch page {
		case "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/org/repos?per_page=100&type=all&page=2>; rel="next", <%s/orgs/org/repos?per_page=100&type=all&page=3>; rel="last"`, srv.URL, srv.URL))
		case "2":
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/org/repos?per_page=100&type=all&page=1>; rel="prev", <%s/orgs/org/repos?per_page=100&type=all&page=3>; rel="next", <%s/orgs/org/repos?per_page=100&type=all&page=3>; rel="last", <%s/orgs/org/repos?per_page=100&type=all&page=1>; rel="first"`, srv.URL, srv.URL, srv.URL, srv.URL))
		case "3":
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/org/repos?per_page=100&type=all&page=2>; rel="prev", <%s/orgs/org/repos?per_page=100&type=all&page=1>; rel="first"`, srv.URL, srv.URL))
		}
		_, _ = w.Write([]byte(pages[page]))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	repos, err := newClient(t, srv).ListOrgRepos(context.Background(), "org")
	if err != nil {
		t.Fatalf("ListOrgRepos: %v", err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, fmt.Sprintf("%d:%s", r.ID, r.FullName))
	}
	if got, want := strings.Join(names, ","), "1:org/r1,2:org/r2,3:org/r3"; got != want {
		t.Fatalf("repos = %s; want %s", got, want)
	}
}

// A next link off the API host would carry the token elsewhere; the
// client refuses to follow it.
func TestClient_RefusesNextLinkOffHost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/org/repos", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<https://evil.example/orgs/org/repos?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[{"id":1,"full_name":"org/r1","default_branch":"main"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := newClient(t, srv).ListOrgRepos(context.Background(), "org")
	if err == nil || !strings.Contains(err.Error(), "leaves") {
		t.Fatalf("err = %v; want refusal of off-host next link", err)
	}
}

func TestClient_GetBranchRulesFollowsNextLink(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/org/a/rules/branches/main", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[{"type":"required_linear_history","ruleset_id":5}]`))
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/org/a/rules/branches/main?per_page=100&page=2>; rel="next"`, srv.URL))
		_, _ = w.Write([]byte(`[{"type":"required_signatures","ruleset_id":5}]`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rules, err := newClient(t, srv).GetBranchRules(context.Background(), "org/a", "main")
	if err != nil {
		t.Fatalf("GetBranchRules: %v", err)
	}
	if len(rules) != 2 || rules[0].Type != "required_signatures" || rules[1].Type != "required_linear_history" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestClient_ListOrgReposErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/org/repos", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := newClient(t, srv).ListOrgRepos(context.Background(), "org")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("err = %v; want HTTP 403", err)
	}
}

func TestClient_GetRulesetWithheldBypass(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/org/a/rulesets/73", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":73,"name":"r","enforcement":"active"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rs, err := newClient(t, srv).GetRuleset(context.Background(), "org/a", 73)
	if err != nil {
		t.Fatalf("GetRuleset: %v", err)
	}
	if rs.BypassActors != nil {
		t.Fatalf("BypassActors = %v; want nil (withheld)", *rs.BypassActors)
	}
}
