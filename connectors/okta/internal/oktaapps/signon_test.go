package oktaapps_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaapps"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) }
}

// #2828: Pull replays Okta's documented app and ACCESS_POLICY rule shapes
// (testdata/README.md) and decides, per app, whether its app sign-in
// policy requires MFA.
func TestPull_SignOnMFAFromDocumentedShapes(t *testing.T) {
	var mu sync.Mutex
	ruleCalls := map[string]int{}
	counted := func(id string, h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			ruleCalls[id]++
			mu.Unlock()
			h(w, r)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/apps", serveFixture(t, "apps.json"))
	mux.HandleFunc("/api/v1/apps/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "00g1"}]`))
	})
	mux.HandleFunc("/api/v1/policies/rst1mfa/rules", counted("rst1mfa", serveFixture(t, "access_policy_rules_mfa.json")))
	mux.HandleFunc("/api/v1/policies/rst2default/rules", counted("rst2default", serveFixture(t, "access_policy_rules_default.json")))
	mux.HandleFunc("/api/v1/policies/rst3chain/rules", counted("rst3chain", serveFixture(t, "access_policy_rules_chain.json")))
	mux.HandleFunc("/api/v1/policies/rst4gone/rules", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errorCode":"E0000007"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	got, err := oktaapps.Pull(context.Background(), oktaapps.NewClient(srv.Client(), srv.URL, creds), nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	yes, no := true, false
	want := []struct {
		id, policy string
		mfa        *bool
		reason     string // substring; "" means no reason
	}{
		{"0oa1admin", "rst1mfa", &yes, ""},
		{"0oa2aws", "rst2default", &no, ""},
		{"0oa3slack", "rst2default", &no, ""},
		{"0oa4chain", "rst3chain", nil, "rul1chain verification method is not ASSURANCE"},
		{"0oa5classic", "", nil, "_links.accessPolicy absent"},
		{"0oa6broken", "rst4gone", nil, "HTTP 404"},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		a := got[i]
		if a.AppID != w.id || a.SignOnPolicyID != w.policy {
			t.Errorf("[%d] app/policy = %s/%s, want %s/%s", i, a.AppID, a.SignOnPolicyID, w.id, w.policy)
		}
		if !reflect.DeepEqual(a.SignOnMFARequired, w.mfa) {
			t.Errorf("%s: SignOnMFARequired = %v, want %v", w.id, deref(a.SignOnMFARequired), deref(w.mfa))
		}
		if w.reason == "" && a.Reason != "" || !strings.Contains(a.Reason, w.reason) {
			t.Errorf("%s: Reason = %q, want %q", w.id, a.Reason, w.reason)
		}
		if a.Classification != "" {
			t.Errorf("%s: Pull classified the app %q; only Classify does", w.id, a.Classification)
		}
	}
	if ruleCalls["rst2default"] != 1 {
		t.Errorf("rst2default rules fetched %d times, want 1 (shared policy cached)", ruleCalls["rst2default"])
	}
}

func deref(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// Edge cases of the rule decision, each served as one policy's rules.
func TestPull_SignOnMFARuleCases(t *testing.T) {
	const allow2FA = `{"id":"r2","status":"ACTIVE","actions":{"appSignOn":{"access":"ALLOW","verificationMethod":{"type":"ASSURANCE","factorMode":"2FA"}}}}`
	const allow1FA = `{"id":"r1","status":"ACTIVE","actions":{"appSignOn":{"access":"ALLOW","verificationMethod":{"type":"ASSURANCE","factorMode":"1FA"}}}}`
	const deny = `{"id":"rd","status":"ACTIVE","actions":{"appSignOn":{"access":"DENY"}}}`
	cases := []struct {
		name   string
		rules  string
		mfa    any // true, false, or nil
		reason string
	}{
		{"2fa then deny", `[` + allow2FA + `,` + deny + `]`, true, ""},
		{"only deny", `[` + deny + `]`, true, ""},
		{"1fa outranks undecided", `[{"id":"rc","status":"ACTIVE","actions":{"appSignOn":{"access":"ALLOW","verificationMethod":{"type":"ID_PROOFING"}}}},` + allow1FA + `]`, false, ""},
		{"no factorMode", `[{"id":"rn","status":"ACTIVE","actions":{"appSignOn":{"access":"ALLOW","verificationMethod":{"type":"ASSURANCE"}}}}]`, nil, `factorMode ""`},
		{"no appSignOn", `[{"id":"rx","status":"ACTIVE","actions":{}}]`, nil, "no appSignOn"},
		{"unknown access", `[{"id":"ru","status":"ACTIVE","actions":{"appSignOn":{"access":"CHALLENGE"}}}]`, nil, `access "CHALLENGE"`},
		{"no active rule", `[{"id":"ri","status":"INACTIVE","actions":{"appSignOn":{"access":"DENY"}}}]`, nil, "no active rule"},
		{"empty", `[]`, nil, "no active rule"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`[{"id":"a1","label":"A","status":"ACTIVE","_links":{"accessPolicy":{"href":"https://x.okta.com/api/v1/policies/p1"}}}]`))
			})
			mux.HandleFunc("/api/v1/apps/a1/groups", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
			mux.HandleFunc("/api/v1/policies/p1/rules", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.rules)) })
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "t"})
			got, err := oktaapps.Pull(context.Background(), oktaapps.NewClient(srv.Client(), srv.URL, creds), nil)
			if err != nil || len(got) != 1 {
				t.Fatalf("Pull = %v, %v", got, err)
			}
			if d := deref(got[0].SignOnMFARequired); d != tc.mfa {
				t.Errorf("SignOnMFARequired = %v, want %v", d, tc.mfa)
			}
			if tc.reason == "" && got[0].Reason != "" || !strings.Contains(got[0].Reason, tc.reason) {
				t.Errorf("Reason = %q, want %q", got[0].Reason, tc.reason)
			}
		})
	}
}

func TestPull_UnrecognisedAccessPolicyHref(t *testing.T) {
	for _, href := range []string{
		"https://x.okta.com/api/v1/apps/p1",
		"https://x.okta.com/api/v1/policies/",
		"https://x.okta.com/api/v1/policies/p1/rules",
		"://bad",
	} {
		t.Run(href, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[{"id":"a1","label":"A","status":"ACTIVE","_links":{"accessPolicy":{"href":"` + href + `"}}}]`))
			})
			mux.HandleFunc("/api/v1/apps/a1/groups", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
			mux.HandleFunc("/api/v1/policies/", func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected rules request %s", r.URL.Path)
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "t"})
			got, err := oktaapps.Pull(context.Background(), oktaapps.NewClient(srv.Client(), srv.URL, creds), nil)
			if err != nil || len(got) != 1 {
				t.Fatalf("Pull = %v, %v", got, err)
			}
			if got[0].SignOnPolicyID != "" || got[0].SignOnMFARequired != nil || !strings.Contains(got[0].Reason, "unrecognised") {
				t.Errorf("got policy %q mfa %v reason %q", got[0].SignOnPolicyID, deref(got[0].SignOnMFARequired), got[0].Reason)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	apps := func() []oktaapps.Assignment {
		return []oktaapps.Assignment{{AppID: "a1"}, {AppID: "a2"}, {AppID: "a3"}}
	}
	classes := func(as []oktaapps.Assignment) []string {
		out := make([]string, len(as))
		for i, a := range as {
			out[i] = a.Classification
		}
		return out
	}

	as := apps()
	if missing := oktaapps.Classify(as, nil, nil); missing != nil {
		t.Errorf("unconfigured: missing = %v", missing)
	}
	if got := classes(as); !reflect.DeepEqual(got, []string{"", "", ""}) {
		t.Errorf("unconfigured: classes = %v, want all unclassified", got)
	}

	as = apps()
	missing := oktaapps.Classify(as, []string{"a1", "zz", "a3"}, []string{"a2", "a3", "yy"})
	if got := classes(as); !reflect.DeepEqual(got, []string{"admin", "infrastructure", "admin"}) {
		t.Errorf("classes = %v (an id on both lists is admin)", got)
	}
	if !reflect.DeepEqual(missing, []string{"yy", "zz"}) {
		t.Errorf("missing = %v, want [yy zz]", missing)
	}

	as = apps()
	oktaapps.Classify(as, nil, []string{"a2"})
	if got := classes(as); !reflect.DeepEqual(got, []string{"standard", "infrastructure", "standard"}) {
		t.Errorf("infrastructure only: classes = %v", got)
	}
}
