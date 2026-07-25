package monitors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ListMonitors_DecodesSecretFreeFields(t *testing.T) {
	t.Parallel()
	var gotAPIKey, gotAppKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("DD-API-KEY")
		gotAppKey = r.Header.Get("DD-APPLICATION-KEY")
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only)", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		// The payload deliberately includes secret-bearing fields (query,
		// options with a webhook url, restricted_roles) that the client MUST NOT
		// decode into RawMonitor.
		_, _ = w.Write([]byte(`[
		  {
		    "id": 12345,
		    "name": "API 5xx",
		    "type": "metric alert",
		    "message": "@slack-ops",
		    "query": "avg(last_5m):sum:http.5xx{env:prod,secret_tag:supersecret} > 100",
		    "options": {"silenced": {}, "notify_audit": false},
		    "restricted_roles": ["role-abc"]
		  },
		  {
		    "id": 67890,
		    "name": "Muted one",
		    "type": "log alert",
		    "options": {"silenced": {"*": null}}
		  }
		]`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-datadog-api-key", "test-datadog-app-key")
	got, err := c.ListMonitors(context.Background())
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if gotAPIKey != "test-datadog-api-key" || gotAppKey != "test-datadog-app-key" {
		t.Errorf("auth headers not set: api=%q app=%q", gotAPIKey, gotAppKey)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].ID != "12345" || !got[0].Enabled {
		t.Errorf("first monitor wrong: %+v", got[0])
	}
	if got[1].Enabled {
		t.Error("muted monitor should be disabled")
	}
	// RawMonitor has no field for query / options / restricted_roles, so they
	// cannot have leaked. Assert the message did decode (used for handles only).
	if got[0].Message != "@slack-ops" {
		t.Errorf("message = %q", got[0].Message)
	}
}

func TestClient_ListMonitors_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["Forbidden"]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	_, err := c.ListMonitors(context.Background())
	if err == nil {
		t.Fatal("want HTTP error")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.Status != http.StatusForbidden {
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

func asAPIError(err error, target **APIError) bool {
	if e, ok := err.(*APIError); ok {
		*target = e
		return true
	}
	return false
}
