package siemrules

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClient_ListRules_DecodesSecretFreeFields asserts the client decodes only
// the config + handle fields and never the detection query, conditions, or any
// matched signal/event — even when the payload embeds them. Also asserts the
// request is a read-only GET with the auth headers set.
func TestClient_ListRules_DecodesSecretFreeFields(t *testing.T) {
	t.Parallel()
	var gotAPIKey, gotAppKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("DD-API-KEY")
		gotAppKey = r.Header.Get("DD-APPLICATION-KEY")
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only)", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		// The payload deliberately includes secret-bearing / over-collection
		// fields (queries, the per-case condition, a webhook url) that the
		// client MUST NOT decode into RawRule.
		_, _ = w.Write([]byte(`{
		  "data": [
		    {
		      "id": "rule-aaa",
		      "type": "security_monitoring_rule",
		      "attributes": {
		        "name": "Brute force",
		        "type": "log_detection",
		        "isEnabled": true,
		        "queries": [{"query": "@evt.name:authentication source:secret_value"}],
		        "cases": [
		          {"status": "high", "condition": "a > 5", "notifications": ["@slack-ops"]},
		          {"status": "critical", "notifications": ["@pagerduty-primary"]}
		        ],
		        "options": {"notificationWebhook": "https://hooks.invalid/SECRET-FIXTURE"}
		      }
		    }
		  ],
		  "meta": {"page": {"after": ""}}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-datadog-api-key", "test-datadog-app-key")
	got, err := c.ListRules(context.Background())
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if gotAPIKey != "test-datadog-api-key" || gotAppKey != "test-datadog-app-key" {
		t.Errorf("auth headers not set: api=%q app=%q", gotAPIKey, gotAppKey)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	r := got[0]
	if r.ID != "rule-aaa" || r.DetectionClass != "log_detection" || !r.Enabled {
		t.Errorf("rule wrong: %+v", r)
	}
	// Highest case severity is reported.
	if r.Severity != "critical" {
		t.Errorf("severity = %q; want highest 'critical'", r.Severity)
	}
	// Handles flatten from both cases; RawRule has no field for the query / the
	// case condition / the webhook url, so they cannot have leaked.
	if len(r.Handles) != 2 {
		t.Errorf("handles = %v; want 2", r.Handles)
	}
}

// TestClient_ListRules_Paginates walks a two-page cursor response and asserts
// every page is concatenated and the cursor terminates correctly.
func TestClient_ListRules_Paginates(t *testing.T) {
	t.Parallel()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		cursor := r.URL.Query().Get("page[cursor]")
		w.Header().Set("Content-Type", "application/json")
		switch cursor {
		case "":
			_, _ = w.Write([]byte(`{"data":[{"id":"r1","attributes":{"name":"a","type":"log_detection","isEnabled":true,"cases":[{"status":"low"}]}}],"meta":{"page":{"after":"CUR2"}}}`))
		case "CUR2":
			_, _ = w.Write([]byte(`{"data":[{"id":"r2","attributes":{"name":"b","type":"signal_correlation","isEnabled":false,"cases":[{"status":"medium"}]}}],"meta":{"page":{"after":""}}}`))
		default:
			t.Errorf("unexpected cursor %q", cursor)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	got, err := c.ListRules(context.Background())
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d; want 2 pages", calls)
	}
	if len(got) != 2 || got[0].ID != "r1" || got[1].ID != "r2" {
		t.Fatalf("pagination concat wrong: %+v", got)
	}
}

// TestClient_ListRules_CapTerminates asserts the per-run page cap stops an
// unbounded source with ErrRuleCapExceeded (DoS / over-collection guard).
func TestClient_ListRules_CapTerminates(t *testing.T) {
	t.Parallel()
	// Server that NEVER stops advancing the cursor — simulates an adversarial
	// / runaway rule set.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("page[cursor]")
		if n == "" {
			n = "0"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"id":"r-%s","attributes":{"name":"n","type":"log","isEnabled":true}}],"meta":{"page":{"after":"%s-next"}}}`, n, n)
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	_, err := c.ListRules(context.Background())
	if err == nil {
		t.Fatal("want cap-exceeded error on a non-terminating source")
	}
	if err.Error() != ErrRuleCapExceeded.Error() {
		t.Errorf("err = %v; want ErrRuleCapExceeded", err)
	}
}

func TestClient_ListRules_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["Forbidden"]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	_, err := c.ListRules(context.Background())
	if err == nil {
		t.Fatal("want HTTP error")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusForbidden {
		t.Errorf("want APIError 403; got %v", err)
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://api.datadoghq.com", "k", "a")
	if c.HTTP == nil {
		t.Error("default HTTP client not set")
	}
}
