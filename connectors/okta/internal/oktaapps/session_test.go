package oktaapps_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaapps"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

// #2951: the documented global session shapes (testdata/README.md). The
// spec's list-all-sign-on-policy-rule-response rule is a catch-all that
// allows a session at one factor; with requireFactor true it requires MFA.
func TestPull_SessionMFAFromDocumentedShapes(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		rules string
		want  *bool
	}{
		{"session_rules_default.json", &no},
		{"session_rules_mfa.json", &yes},
	} {
		t.Run(tc.rules, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[{"id":"a1","label":"A","status":"ACTIVE"}]`))
			})
			mux.HandleFunc("/api/v1/apps/a1/groups", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
			mux.HandleFunc("/api/v1/policies", serveSessionPolicies(t, "session_policies.json"))
			mux.HandleFunc("/api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("search"); got != `type eq "BUILT_IN"` {
					t.Errorf("groups search = %q", got)
				}
				serveFixture(t, "groups_builtin.json")(w, r)
			})
			mux.HandleFunc("/api/v1/policies/00p1default/rules", serveFixture(t, tc.rules))
			got := pullOne(t, mux)
			if !reflect.DeepEqual(got.SessionMFARequired, tc.want) || got.SessionReason != "" {
				t.Errorf("SessionMFARequired = %v (%q), want %v", deref(got.SessionMFARequired), got.SessionReason, deref(tc.want))
			}
			// The app has no sign-in policy; the session verdict does not
			// stand in for the app's own.
			if got.SignOnMFARequired != nil || !strings.Contains(got.Reason, "_links.accessPolicy absent") {
				t.Errorf("SignOnMFARequired = %v (%q)", deref(got.SignOnMFARequired), got.Reason)
			}
		})
	}
}

func pullOne(t *testing.T, mux *http.ServeMux) oktaapps.Assignment {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "t"})
	got, err := oktaapps.Pull(context.Background(), oktaapps.NewClient(srv.Client(), srv.URL, creds), nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("Pull = %v, %v", got, err)
	}
	return got[0]
}

const everyone = "00g1everyone"

// Policies and rules for the decision cases. A rule's conditions decide
// whether it is a catch-all.
const (
	anywhere = `{"people":{"users":{"exclude":[]}},"network":{"connection":"ANYWHERE"},"risk":{"behaviors":[]},"riskScore":{"level":"ANY"},"identityProvider":{"provider":"ANY"}}`
	zone     = `{"network":{"connection":"ZONE","include":["nzo1"]}}`
)

func policy(id string, prio int, group string) string {
	return policyWith(id, prio, `{"people":{"groups":{"include":["`+group+`"]}}}`)
}

func policyWith(id string, prio int, conds string) string {
	return `{"type":"OKTA_SIGN_ON","id":"` + id + `","status":"ACTIVE","priority":` + strconv.Itoa(prio) + `,"conditions":` + conds + `}`
}

func rule(id string, prio int, conds, signon string) string {
	return `{"id":"` + id + `","status":"ACTIVE","priority":` + strconv.Itoa(prio) + `,"conditions":` + conds + `,"actions":{"signon":` + signon + `},"type":"SIGN_ON"}`
}

const (
	mfa   = `{"access":"ALLOW","requireFactor":true,"factorPromptMode":"ALWAYS"}`
	oneFA = `{"access":"ALLOW","requireFactor":false}`
	deny  = `{"access":"DENY"}`
)

func TestPull_SessionMFACases(t *testing.T) {
	// The default policy, last, with Okta's 1FA catch-all default rule.
	def := policy("pdef", 99, everyone)
	defRules := `[` + rule("rdef", 99, anywhere, oneFA) + `]`
	cases := []struct {
		name     string
		policies string
		rules    map[string]string
		groups   string // "" serves the Everyone group
		mfa      any
		reason   string
	}{
		{"everyone policy with mfa catch-all shadows the default", `[` + policy("p1", 1, everyone) + `,` + def + `]`,
			map[string]string{"p1": `[` + rule("r1", 1, anywhere, mfa) + `]`, "pdef": defRules}, "", true, ""},
		{"everyone policy with a zoned mfa rule falls through to the default", `[` + policy("p1", 1, everyone) + `,` + def + `]`,
			map[string]string{"p1": `[` + rule("r1", 1, zone, mfa) + `]`, "pdef": defRules}, "", false, ""},
		{"everyone policy excluding a group does not shadow the default", `[` + policyWith("p1", 1, `{"people":{"groups":{"include":["`+everyone+`"],"exclude":["00g1contractors"]}}}`) + `,` + def + `]`,
			map[string]string{"p1": `[` + rule("r1", 1, anywhere, mfa) + `]`, "pdef": defRules}, "", false, ""},
		{"everyone policy excluding a user does not shadow the default", `[` + policyWith("p1", 1, `{"people":{"groups":{"include":["`+everyone+`"]},"users":{"exclude":["00u1svc"]}}}`) + `,` + def + `]`,
			map[string]string{"p1": `[` + rule("r1", 1, anywhere, mfa) + `]`, "pdef": defRules}, "", false, ""},
		{"everyone policy with another condition does not shadow the default", `[` + policyWith("p1", 1, `{"people":{"groups":{"include":["`+everyone+`"]}},"network":{"connection":"ZONE","include":["nzo1"]}}`) + `,` + def + `]`,
			map[string]string{"p1": `[` + rule("r1", 1, anywhere, mfa) + `]`, "pdef": defRules}, "", false, ""},
		{"everyone policy with empty excludes shadows the default", `[` + policyWith("p1", 1, `{"people":{"groups":{"include":["`+everyone+`"],"exclude":[]},"users":{"exclude":[]}}}`) + `,` + def + `]`,
			map[string]string{"p1": `[` + rule("r1", 1, anywhere, mfa) + `]`, "pdef": defRules}, "", true, ""},
		{"group policy does not shadow the default", `[` + policy("p1", 1, "00g1admins") + `,` + def + `]`,
			map[string]string{"p1": `[` + rule("r1", 1, anywhere, mfa) + `]`, "pdef": defRules}, "", false, ""},
		{"mfa catch-all rule shadows the default rule", `[` + def + `]`,
			map[string]string{"pdef": `[` + rule("rdef", 99, anywhere, oneFA) + `,` + rule("r1", 1, `null`, mfa) + `]`}, "", true, ""},
		{"zoned mfa rule does not shadow the default rule", `[` + def + `]`,
			map[string]string{"pdef": `[` + rule("r1", 1, zone, mfa) + `,` + rule("rdef", 99, anywhere, oneFA) + `]`}, "", false, ""},
		{"unknown condition is not catch-all", `[` + def + `]`,
			map[string]string{"pdef": `[` + rule("r1", 1, `{"device":{"registered":true}}`, mfa) + `,` + rule("rdef", 99, anywhere, oneFA) + `]`}, "", false, ""},
		{"shared rule priority shadows nothing", `[` + def + `]`,
			map[string]string{"pdef": `[` + rule("r1", 1, anywhere, mfa) + `,` + rule("rdef", 1, anywhere, oneFA) + `]`}, "", false, ""},
		{"deny and mfa", `[` + def + `]`,
			map[string]string{"pdef": `[` + rule("r1", 1, zone, deny) + `,` + rule("rdef", 99, anywhere, mfa) + `]`}, "", true, ""},
		{"absent requireFactor is one factor", `[` + def + `]`,
			map[string]string{"pdef": `[` + rule("rdef", 99, anywhere, `{"access":"ALLOW"}`) + `]`}, "", false, ""},
		{"policy without active rules falls through", `[` + policy("p1", 1, everyone) + `,` + def + `]`,
			map[string]string{"p1": `[]`, "pdef": `[` + rule("rdef", 99, anywhere, mfa) + `]`}, "", true, ""},
		{"inactive rule is skipped", `[` + def + `]`,
			map[string]string{"pdef": `[{"id":"ri","status":"INACTIVE","priority":1,"actions":{"signon":` + oneFA + `}},` + rule("rdef", 99, anywhere, mfa) + `]`}, "", true, ""},
		{"rule without signon", `[` + def + `]`,
			map[string]string{"pdef": `[{"id":"rx","status":"ACTIVE","priority":99,"actions":{}}]`}, "", nil, "rule rx has no signon action"},
		{"unknown access", `[` + def + `]`,
			map[string]string{"pdef": `[` + rule("rdef", 99, anywhere, `{"access":"CHALLENGE"}`) + `]`}, "", nil, `access "CHALLENGE"`},
		{"one factor outranks undecided", `[` + def + `]`,
			map[string]string{"pdef": `[{"id":"rx","status":"ACTIVE","priority":1,"conditions":` + zone + `,"actions":{}},` + rule("rdef", 99, anywhere, oneFA) + `]`}, "", false, ""},
		{"undecided catch-all shadows the rule after it", `[` + def + `]`,
			map[string]string{"pdef": `[{"id":"rx","status":"ACTIVE","priority":1,"actions":{}},` + rule("rdef", 99, anywhere, oneFA) + `]`}, "", nil, "rule rx has no signon action"},
		{"no active policy", `[{"type":"OKTA_SIGN_ON","id":"pdef","status":"INACTIVE","priority":1}]`,
			nil, "", nil, "no active global session policy"},
		{"rules unreadable", `[` + def + `]`, map[string]string{}, "", nil, "pdef rules"},
		{"everyone group hidden", `[` + def + `]`, map[string]string{"pdef": defRules}, `[]`, nil, "everyone group not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[{"id":"a1","label":"A","status":"ACTIVE"}]`))
			})
			mux.HandleFunc("/api/v1/apps/a1/groups", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
			mux.HandleFunc("/api/v1/policies", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.policies)) })
			mux.HandleFunc("/api/v1/policies/", func(w http.ResponseWriter, r *http.Request) {
				id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/policies/"), "/rules")
				body, ok := tc.rules[id]
				if !ok {
					http.Error(w, `{"errorCode":"E0000007"}`, http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(body))
			})
			mux.HandleFunc("/api/v1/groups", func(w http.ResponseWriter, _ *http.Request) {
				g := tc.groups
				if g == "" {
					g = `[{"id":"` + everyone + `","type":"BUILT_IN","profile":{"name":"Everyone"}}]`
				}
				_, _ = w.Write([]byte(g))
			})
			got := pullOne(t, mux)
			if d := deref(got.SessionMFARequired); d != tc.mfa {
				t.Errorf("SessionMFARequired = %v (%q), want %v", d, got.SessionReason, tc.mfa)
			}
			if tc.reason == "" && got.SessionReason != "" || !strings.Contains(got.SessionReason, tc.reason) {
				t.Errorf("SessionReason = %q, want %q", got.SessionReason, tc.reason)
			}
		})
	}
}
