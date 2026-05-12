package oktapolicy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
)

// fakeAPI lets tests bypass the HTTP path by stubbing the API surface.
// Used for the pure-logic Pull tests.

// Pull hits the HTTP path through the Client. Replays a realistic
// /api/v1/policies?type=MFA_ENROLL response.
func TestPull_ParsesMFAEnrollPolicies(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "MFA_ENROLL" {
			t.Errorf("missing type=MFA_ENROLL param; got %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "SSWS test-token" {
			t.Errorf("Authorization header = %q; want SSWS test-token", got)
		}
		_, _ = w.Write([]byte(`[
			{
				"id": "p1",
				"name": "Default MFA Enrollment",
				"status": "ACTIVE",
				"type": "MFA_ENROLL",
				"settings": {
					"factors": {
						"okta_verify": {"enroll": {"self": "REQUIRED"}},
						"webauthn":    {"enroll": {"self": "OPTIONAL"}}
					}
				},
				"conditions": {"people": {"groups": {"include": ["g1", "g2"]}}}
			},
			{
				"id": "p2",
				"name": "Contractors MFA",
				"status": "ACTIVE",
				"type": "MFA_ENROLL",
				"settings": {
					"factors": {
						"okta_verify": {"enroll": {"self": "NOT_ALLOWED"}}
					}
				}
			}
		]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	creds, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	client := oktapolicy.NewClient(srv.Client(), srv.URL, creds)

	fixed := func() time.Time { return time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC) }
	states, err := oktapolicy.Pull(context.Background(), client, fixed)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("len(states) = %d; want 2", len(states))
	}

	// p1: requires okta_verify; passes.
	p1 := states[0]
	if p1.PolicyID != "p1" {
		t.Errorf("p1.PolicyID = %q", p1.PolicyID)
	}
	if !p1.MFARequired {
		t.Errorf("p1.MFARequired = false; want true (okta_verify enroll=REQUIRED)")
	}
	if p1.Result != oktapolicy.ResultPass {
		t.Errorf("p1.Result = %q; want pass", p1.Result)
	}
	if len(p1.FactorsAllowed) != 2 {
		t.Errorf("p1.FactorsAllowed = %v; want 2 (REQUIRED + OPTIONAL)", p1.FactorsAllowed)
	}
	if len(p1.AppliesToGroups) != 2 {
		t.Errorf("p1.AppliesToGroups = %v; want [g1 g2]", p1.AppliesToGroups)
	}

	// p2: no factor REQUIRED → fails.
	p2 := states[1]
	if p2.MFARequired {
		t.Errorf("p2.MFARequired = true; want false (only NOT_ALLOWED factor)")
	}
	if p2.Result != oktapolicy.ResultFail {
		t.Errorf("p2.Result = %q; want fail", p2.Result)
	}
}

func TestPull_NilAPI(t *testing.T) {
	if _, err := oktapolicy.Pull(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on nil API")
	}
}
