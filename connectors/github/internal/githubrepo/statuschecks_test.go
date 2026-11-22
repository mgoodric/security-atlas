package githubrepo_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
)

// #2827: status checks and force pushes merge across classic protection
// and rulesets; topics come from the listing and survive an unread
// protection.
func TestInspect_StatusChecksForcePushesTopics(t *testing.T) {
	topics := []string{"iac", "production"}
	withTopics := repoA
	withTopics.Topics = &topics
	nonFF := githubrepo.BranchRule{Type: "non_fast_forward", RulesetID: 9}
	checksRule := func(params string) githubrepo.BranchRule {
		return githubrepo.BranchRule{Type: "required_status_checks", RulesetID: 9, Parameters: json.RawMessage(params)}
	}
	classic := func(force *githubrepo.Enabled, sc *githubrepo.StatusChecks) *githubrepo.BranchProtection {
		return &githubrepo.BranchProtection{RequiredPullRequestReviews: reviews(1, false), AllowForcePushes: force, RequiredStatusChecks: sc}
	}
	sc := &githubrepo.StatusChecks{
		Contexts: []string{"lint", "build"},
		Checks:   []githubrepo.StatusCheck{{Context: "test"}, {Context: "build"}},
	}
	cases := []struct {
		name        string
		prot        *githubrepo.BranchProtection
		rules       []githubrepo.BranchRule
		checks      string
		forcePushes string
		result      githubrepo.Result
	}{
		{"classic blocks force pushes", classic(&githubrepo.Enabled{Enabled: false}, nil), nil, "[]", "false", githubrepo.ResultPass},
		{"classic allows force pushes", classic(&githubrepo.Enabled{Enabled: true}, nil), nil, "[]", "true", githubrepo.ResultPass},
		{"classic omits allow_force_pushes", classic(nil, nil), nil, "[]", "unread", githubrepo.ResultPass},
		{"non_fast_forward decides an omitted classic setting", classic(nil, nil), []githubrepo.BranchRule{nonFF}, "[]", "false", githubrepo.ResultPass},
		{"non_fast_forward blocks what classic allows", classic(&githubrepo.Enabled{Enabled: true}, nil), []githubrepo.BranchRule{nonFF}, "[]", "false", githubrepo.ResultPass},
		{"classic contexts and checks union", classic(&githubrepo.Enabled{}, sc), nil, "[build lint test]", "false", githubrepo.ResultPass},
		{"rule checks merge with classic", classic(&githubrepo.Enabled{}, sc), []githubrepo.BranchRule{checksRule(`{"required_status_checks":[{"context":"deploy"},{"context":"lint"}],"strict_required_status_checks_policy":false}`)}, "[build deploy lint test]", "false", githubrepo.ResultPass},
		{"undecodable checks rule leaves the repo unread", classic(nil, nil), []githubrepo.BranchRule{checksRule(`{"required_status_checks":"ci"}`)}, "unread", "unread", githubrepo.ResultInconclusive},
		{"checks rule without parameters leaves the repo unread", classic(nil, nil), []githubrepo.BranchRule{{Type: "required_status_checks", RulesetID: 9}}, "unread", "unread", githubrepo.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := inspectOne(t, &fakeAPI{
				repos: []githubrepo.Repo{withTopics},
				prot:  map[string]*githubrepo.BranchProtection{"org/a": tc.prot},
				rules: map[string][]githubrepo.BranchRule{"org/a": tc.rules},
			})
			if got := stringsVal(s.RequiredStatusChecks); got != tc.checks {
				t.Errorf("required_status_checks = %s; want %s", got, tc.checks)
			}
			if got := boolVal(s.AllowForcePushes); got != tc.forcePushes {
				t.Errorf("allow_force_pushes = %s; want %s", got, tc.forcePushes)
			}
			if got := stringsVal(s.Topics); got != "[iac production]" {
				t.Errorf("topics = %s; want [iac production]", got)
			}
			if s.Result != tc.result {
				t.Errorf("result = %s; want %s (%s)", s.Result, tc.result, s.Reason)
			}
		})
	}

	t.Run("unread protection keeps topics", func(t *testing.T) {
		s := inspectOne(t, &fakeAPI{
			repos: []githubrepo.Repo{withTopics},
			errOn: map[string]error{"org/a": &githubrepo.APIError{Status: http.StatusBadGateway}},
		})
		if s.Result != githubrepo.ResultInconclusive || s.RequiredStatusChecks != nil || s.AllowForcePushes != nil {
			t.Errorf("unread repo = %s checks=%s force=%s", s.Result, stringsVal(s.RequiredStatusChecks), boolVal(s.AllowForcePushes))
		}
		if got := stringsVal(s.Topics); got != "[iac production]" {
			t.Errorf("topics = %s; want [iac production]", got)
		}
	})

	t.Run("no protection allows force pushes and requires no check", func(t *testing.T) {
		s := inspectOne(t, &fakeAPI{repos: []githubrepo.Repo{repoA}, notFound: map[string]bool{"org/a": true}})
		if stringsVal(s.RequiredStatusChecks) != "[]" || boolVal(s.AllowForcePushes) != "true" {
			t.Errorf("checks=%s force=%s; want [] true", stringsVal(s.RequiredStatusChecks), boolVal(s.AllowForcePushes))
		}
		if s.Topics != nil {
			t.Errorf("topics = %s; want unread when the listing omits them", stringsVal(s.Topics))
		}
	})
}
