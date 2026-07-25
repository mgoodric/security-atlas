package alerthistory

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClient_ListStateHistory_DecodesBodyFreeFields is the load-bearing
// over-collection DROP test (P0-535, Information Disclosure DOMINANT): the
// state-history line carries an annotation/message body, the triggering metric
// VALUES, a secret contact-point setting, and recipient PII — none of which may
// reach a record. It also asserts a read-only GET with the auth header + window.
func TestClient_ListStateHistory_DecodesBodyFreeFields(t *testing.T) {
	t.Parallel()
	var gotAuth, gotMethod, gotFrom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotFrom = r.URL.Query().Get("from")
		w.Header().Set("Content-Type", "application/json")
		// The line deliberately embeds over-collection surfaces the client MUST
		// NOT decode: a values map (metric numbers), annotations (alert message
		// body), and a recipient-email label + a webhook secret.
		_, _ = w.Write([]byte(`{
		  "data": {
		    "values": [
		      [1717752000000, 1717755600000],
		      [
		        {
		          "current": "Alerting",
		          "previous": "Normal",
		          "ruleUID": "rule-uid-1",
		          "labels": {"__contact_point__": "pd-primary", "alertname": "CPU", "user_email": "bob@corp.test"},
		          "values": {"A": 97.5, "B": 98.1},
		          "error": "",
		          "annotations": {"summary": "test-message-body-should-be-dropped", "webhook": "https://example.test/secret-webhook-should-be-dropped"}
		        },
		        {
		          "current": "Normal",
		          "ruleUID": "rule-uid-1",
		          "labels": {"receiver": "email-team"},
		          "values": {"A": 10.0}
		        }
		      ]
		    ]
		  }
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-grafana-token")
	since := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got, err := c.ListStateHistory(context.Background(), since)
	if err != nil {
		t.Fatalf("ListStateHistory: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s; want GET (read-only)", gotMethod)
	}
	if gotAuth != "Bearer test-grafana-token" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotFrom != fmt.Sprintf("%d", since.Unix()) {
		t.Errorf("from = %q; want %d", gotFrom, since.Unix())
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].RuleID != "rule-uid-1" || got[0].State != "Alerting" || got[0].TargetHandle != "pd-primary" {
		t.Errorf("transition wrong: %+v", got[0])
	}
	if got[0].FiredAt.IsZero() {
		t.Error("fired_at not parsed from epoch-millis")
	}
	// The over-collection proof: no decoded string field carries the forbidden
	// markers. (The values/annotations have no struct field, so cannot leak.)
	for _, tr := range got {
		for _, v := range []string{tr.RuleID, tr.State, tr.TargetHandle, tr.TargetKind} {
			for _, bad := range []string{
				"test-message-body-should-be-dropped",
				"secret-webhook-should-be-dropped",
				"bob@corp.test", "97.5", "98.1",
			} {
				if strings.Contains(v, bad) {
					t.Errorf("decoded field %q leaked over-collected content %q", v, bad)
				}
			}
		}
	}
}

func TestClient_ListStateHistory_EmptyFrame(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"values":[]}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k")
	got, err := c.ListStateHistory(context.Background(), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("ListStateHistory: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty frame should yield 0 rows: %+v", got)
	}
}

func TestClient_ListStateHistory_SkipsUidlessAndUntimed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"values":[
		  [1717752000000, 0, 1717752000000],
		  [
		    {"current":"Alerting","ruleUID":""},
		    {"current":"Alerting","ruleUID":"r-untimed"},
		    {"current":"Alerting","ruleUID":"r-ok"}
		  ]
		]}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k")
	got, _ := c.ListStateHistory(context.Background(), time.Unix(1, 0))
	if len(got) != 1 || got[0].RuleID != "r-ok" {
		t.Errorf("uidless/untimed rows not skipped: %+v", got)
	}
}

func TestClient_ListStateHistory_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k")
	_, err := c.ListStateHistory(context.Background(), time.Unix(1, 0))
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("want APIError 401; got %v", err)
	}
}

func TestClient_ListStateHistory_MalformedLineSkipped(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"values":[[1717752000000,1717752000000],["not-an-object",{"current":"Alerting","ruleUID":"r"}]]}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k")
	got, err := c.ListStateHistory(context.Background(), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("ListStateHistory: %v", err)
	}
	if len(got) != 1 || got[0].RuleID != "r" {
		t.Errorf("malformed line not skipped: %+v", got)
	}
}

func TestReceiverFromLabels(t *testing.T) {
	t.Parallel()
	if h, k := receiverFromLabels(nil); h != "" || k != "" {
		t.Errorf("nil labels = %q/%q", h, k)
	}
	if h, k := receiverFromLabels(map[string]string{"receiver": "team-x"}); h != "team-x" || k != "contact_point" {
		t.Errorf("receiver label = %q/%q", h, k)
	}
	if h, _ := receiverFromLabels(map[string]string{"alertname": "x"}); h != "" {
		t.Errorf("no receiver should be empty: %q", h)
	}
}

func TestAPIError_Message(t *testing.T) {
	t.Parallel()
	if got := (&APIError{Status: 500}).Error(); !strings.Contains(got, "500") {
		t.Errorf("error without body = %q", got)
	}
	if got := (&APIError{Status: 401, Body: "nope"}).Error(); !strings.Contains(got, "nope") {
		t.Errorf("error with body = %q", got)
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://grafana.example.com", "k")
	if c.HTTP == nil {
		t.Error("default HTTP client not set")
	}
}
