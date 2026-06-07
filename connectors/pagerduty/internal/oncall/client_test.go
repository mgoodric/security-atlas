package oncall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// escalationPoliciesJSON includes responder PII fields (email, phone) that the
// client MUST NOT decode or surface. The fixture proves the decode boundary
// drops them (P0-489-3).
const escalationPoliciesJSON = `{
  "escalation_policies": [
    {
      "id": "PABC",
      "name": "Primary On-Call",
      "escalation_rules": [
        {
          "targets": [
            {"id": "U1", "type": "user_reference", "summary": "Alice Eng",
             "email": "alice@fixture.invalid", "phone": "+15555550100"}
          ]
        },
        {
          "targets": [
            {"id": "S1", "type": "schedule_reference", "summary": "Backup Rotation"}
          ]
        }
      ]
    }
  ]
}`

func TestClient_ListEscalationPolicies(t *testing.T) {
	t.Parallel()
	var gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if !strings.HasPrefix(r.URL.Path, "/escalation_policies") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(escalationPoliciesJSON))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-pagerduty-token")
	got, err := c.ListEscalationPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListEscalationPolicies: %v", err)
	}
	if gotAuth != "Token token=test-pagerduty-token" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	if !strings.Contains(gotAccept, "pagerduty+json") {
		t.Errorf("Accept header = %q", gotAccept)
	}
	if len(got) != 1 || got[0].ID != "PABC" {
		t.Fatalf("got %+v", got)
	}
	if len(got[0].Tiers) != 2 {
		t.Fatalf("tiers = %d; want 2", len(got[0].Tiers))
	}
	// The RawTarget type has no email/phone field — assert the identity-only
	// fields decoded and the PII never reached the struct (it cannot, by type).
	tgt := got[0].Tiers[0].Targets[0]
	if tgt.ID != "U1" || tgt.Name != "Alice Eng" || tgt.Kind != "user_reference" {
		t.Errorf("target = %+v", tgt)
	}
}

func TestClient_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "test-pagerduty-token")
	if _, err := c.ListEscalationPolicies(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error; got %v", err)
	}
}
