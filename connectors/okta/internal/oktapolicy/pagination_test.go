package oktapolicy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
)

// TestPull_FollowsLinkPagination puts the Everyone policy and the Everyone
// group on page two of their listings. Okta sends rel="self" and
// rel="next" as separate Link headers, the next URL carrying the `after`
// cursor (developer.okta.com/docs/api/#pagination). A first-page-only read
// would miss both and report cc6_1 false (#2915).
func TestPull_FollowsLinkPagination(t *testing.T) {
	page1Policies := `[{
		"type": "MFA_ENROLL", "id": "00p1page0one0contractor", "status": "ACTIVE",
		"name": "Contractors", "priority": 1,
		"conditions": {"people": {"groups": {"include": ["00g1contractors0000001"]}}},
		"settings": {"type": "AUTHENTICATORS", "authenticators": [{"key": "okta_password", "enroll": {"self": "REQUIRED"}}]}
	}]`
	page2Policies := `[{
		"type": "MFA_ENROLL", "id": "00p1page0two0everyone00", "status": "ACTIVE",
		"name": "Default Policy", "priority": 2, "system": true,
		"conditions": {"people": {"groups": {"include": ["` + everyoneID + `"]}}},
		"settings": {"type": "AUTHENTICATORS", "authenticators": [{"key": "webauthn", "enroll": {"self": "REQUIRED"}}]}
	}]`
	page1Groups := `[{"id": "00g1builtin0other00001", "type": "BUILT_IN", "profile": {"name": "Okta Administrators"}}]`

	var srv *httptest.Server
	paged := func(first, second string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "SSWS test-token" {
				t.Errorf("%s: Authorization = %q", r.URL, got)
			}
			self := srv.URL + r.URL.RequestURI()
			w.Header().Add("Link", "<"+self+`>; rel="self"`)
			if r.URL.Query().Get("after") == "" {
				q := r.URL.Query()
				q.Set("after", "00p1cursor")
				w.Header().Add("Link", "<"+srv.URL+r.URL.Path+"?"+q.Encode()+`>; rel="next"`)
				_, _ = w.Write([]byte(first))
				return
			}
			_, _ = w.Write([]byte(second))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policies", paged(page1Policies, page2Policies))
	mux.HandleFunc("/api/v1/groups", paged(page1Groups, builtInGroups))
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	states, err := oktapolicy.Pull(context.Background(), oktapolicy.NewClient(srv.Client(), srv.URL, creds), fixed)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	byID := map[string]oktapolicy.PolicyState{}
	for _, s := range states {
		byID[s.PolicyID] = s
	}
	if len(byID) != 2 {
		t.Fatalf("states = %v; want both pages' policies", byID)
	}
	every, ok := byID["00p1page0two0everyone00"]
	if !ok || !every.AppliesToEveryone || every.Result != oktapolicy.ResultPass {
		t.Errorf("page-two Everyone policy = %+v (found=%v); want AppliesToEveryone, pass", every, ok)
	}
}
