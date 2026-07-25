package alertrules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ListAlertRules_DecodesSecretFreeFields(t *testing.T) {
	t.Parallel()
	var gotAuth string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
		  {
		    "uid": "r1",
		    "title": "High latency",
		    "isPaused": false,
		    "folderUID": "f1",
		    "type": "grafana",
		    "notification_settings": {"receiver": "sec-oncall", "group_by": ["alertname"]},
		    "data": [{"refId":"A","datasourceUid":"prom","model":{"expr":"secret_metric{tenant=42}"}}]
		  }
		]`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-grafana-token")
	got, err := c.ListAlertRules(context.Background())
	if err != nil {
		t.Fatalf("ListAlertRules: %v", err)
	}
	if gotAuth != "Bearer test-grafana-token" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s; want GET (read-only)", gotMethod)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	r := got[0]
	if r.UID != "r1" || r.Title != "High latency" || r.Paused {
		t.Errorf("fields = %+v", r)
	}
	if r.ReceiverName != "sec-oncall" {
		t.Errorf("receiver = %q", r.ReceiverName)
	}
	// RawRule has no field for `data` (the query model), so the secret metric
	// expression cannot have leaked.
}

func TestClient_ListContactPoints_DropsSettings(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The settings blob holds the secret webhook URL — the client MUST NOT
		// decode it into ContactPoint. The fixture uses an obviously-fake
		// placeholder host (not a real webhook shape) so secret-scanners don't
		// false-positive on a test fixture.
		_, _ = w.Write([]byte(`[
		  {"name": "sec-oncall", "type": "slack", "settings": {"url": "https://webhook.invalid/REDACTED-FIXTURE"}},
		  {"name": "ops-pd", "type": "pagerduty", "settings": {"integrationKey": "fixture-placeholder-not-a-token"}}
		]`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-grafana-token")
	got, err := c.ListContactPoints(context.Background())
	if err != nil {
		t.Fatalf("ListContactPoints: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	// ContactPoint has no settings field — the secret cannot have leaked. Assert
	// only name + kind decoded.
	if got[0].Name != "sec-oncall" || got[0].Kind != "slack" {
		t.Errorf("contact[0] = %+v", got[0])
	}
	if got[1].Kind != "pagerduty" {
		t.Errorf("contact[1] kind = %q", got[1].Kind)
	}
}

func TestClient_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "t")
	if _, err := c.ListAlertRules(context.Background()); err == nil {
		t.Fatal("want HTTP error")
	}
	if _, err := c.ListContactPoints(context.Background()); err == nil {
		t.Fatal("want HTTP error")
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	if NewClient(nil, "https://g", "t").HTTP == nil {
		t.Error("default HTTP client not set")
	}
}
