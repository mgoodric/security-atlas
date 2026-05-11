package githubrepo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
)

// fakeAPI is the in-process fake used by behavior-level tests so the
// pass/fail logic and the HTTP layer can be tested independently.
type fakeAPI struct {
	repos    []githubrepo.Repo
	prot     map[string]*githubrepo.BranchProtection
	notFound map[string]bool
	errOn    map[string]error
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

func TestInspect_PassWhenReviewsRequired(t *testing.T) {
	api := &fakeAPI{
		repos: []githubrepo.Repo{{FullName: "org/a", DefaultBranch: "main"}},
		prot: map[string]*githubrepo.BranchProtection{
			"org/a": {
				RequiredPullRequestReviews: &struct {
					RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
					RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
				}{RequiredApprovingReviewCount: 1, RequireCodeOwnerReviews: true},
			},
		},
	}
	got, err := githubrepo.Inspect(context.Background(), api, "org", nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(states) = %d", len(got))
	}
	if got[0].Result != githubrepo.ResultPass {
		t.Errorf("result = %q; want pass", got[0].Result)
	}
	if !got[0].RequireCodeOwnerReviews {
		t.Errorf("RequireCodeOwnerReviews = false; want true")
	}
}

func TestInspect_FailWhenReviewsZero(t *testing.T) {
	api := &fakeAPI{
		repos: []githubrepo.Repo{{FullName: "org/zero", DefaultBranch: "main"}},
		prot: map[string]*githubrepo.BranchProtection{
			"org/zero": {RequiredPullRequestReviews: &struct {
				RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
				RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
			}{RequiredApprovingReviewCount: 0}},
		},
	}
	got, err := githubrepo.Inspect(context.Background(), api, "org", nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != githubrepo.ResultFail {
		t.Fatalf("result = %q; want fail", got[0].Result)
	}
}

func TestInspect_FailWhenProtection404(t *testing.T) {
	api := &fakeAPI{
		repos:    []githubrepo.Repo{{FullName: "org/none", DefaultBranch: "main"}},
		notFound: map[string]bool{"org/none": true},
	}
	got, err := githubrepo.Inspect(context.Background(), api, "org", nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != githubrepo.ResultFail {
		t.Fatalf("result = %q; want fail", got[0].Result)
	}
}

func TestInspect_SkipsArchived(t *testing.T) {
	api := &fakeAPI{
		repos: []githubrepo.Repo{
			{FullName: "org/live", DefaultBranch: "main"},
			{FullName: "org/dead", DefaultBranch: "main", Archived: true},
		},
		prot: map[string]*githubrepo.BranchProtection{
			"org/live": {RequiredPullRequestReviews: &struct {
				RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
				RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
			}{RequiredApprovingReviewCount: 1}},
		},
	}
	got, err := githubrepo.Inspect(context.Background(), api, "org", nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 state (archived skipped); got %d", len(got))
	}
	if got[0].RepoFullName != "org/live" {
		t.Fatalf("RepoFullName = %q; want org/live", got[0].RepoFullName)
	}
}

// TestClient_HTTPListAndProtection exercises the real REST shapes against
// an httptest.NewServer with realistic GitHub JSON, satisfying the
// orchestrator's "do not mock the GitHub API in a way that hides
// contract bugs" directive.
func TestClient_HTTPListAndProtection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/example/repos", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("Authorization header missing; got %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		_ = json.NewEncoder(w).Encode([]githubrepo.Repo{
			{FullName: "example/web", DefaultBranch: "main", Private: false},
			{FullName: "example/api", DefaultBranch: "trunk", Private: true},
		})
	})
	mux.HandleFunc("/repos/example/web/branches/main/protection", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"required_pull_request_reviews": {
				"required_approving_review_count": 2,
				"require_code_owner_reviews": true
			},
			"required_signatures": {"enabled": true},
			"required_linear_history": {"enabled": true},
			"enforce_admins": {"enabled": true}
		}`))
	})
	mux.HandleFunc("/repos/example/api/branches/trunk/protection", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	creds, err := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	c := githubrepo.NewClient(srv.Client(), srv.URL, creds)

	got, err := githubrepo.Inspect(context.Background(), c, "example", func() time.Time {
		return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	byRepo := map[string]githubrepo.ProtectionState{}
	for _, s := range got {
		byRepo[s.RepoFullName] = s
	}
	web := byRepo["example/web"]
	if web.Result != githubrepo.ResultPass {
		t.Errorf("web result = %q; want pass", web.Result)
	}
	if web.RequiredReviews != 2 {
		t.Errorf("web required_reviews = %d; want 2", web.RequiredReviews)
	}
	if !web.RequireSignedCommits {
		t.Errorf("web require_signed_commits = false; want true")
	}
	api := byRepo["example/api"]
	if api.Result != githubrepo.ResultFail {
		t.Errorf("api result = %q; want fail (404 protection)", api.Result)
	}
}

func TestInspect_RejectsEmptyOrg(t *testing.T) {
	_, err := githubrepo.Inspect(context.Background(), &fakeAPI{}, "", nil)
	if err == nil {
		t.Fatal("expected error on empty org")
	}
}
