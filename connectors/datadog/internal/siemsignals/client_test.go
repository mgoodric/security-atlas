package siemsignals

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClient_ListSignals_DecodesBodyFreeFields asserts the client decodes only
// the triage-metadata fields and never the signal message, matched samples, the
// detection query, or body tags — even when the payload embeds them. Also
// asserts the request is a read-only GET with the auth headers + look-back
// filter set.
func TestClient_ListSignals_DecodesBodyFreeFields(t *testing.T) {
	t.Parallel()
	var gotAPIKey, gotAppKey, gotMethod, gotFrom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("DD-API-KEY")
		gotAppKey = r.Header.Get("DD-APPLICATION-KEY")
		gotMethod = r.Method
		gotFrom = r.URL.Query().Get("filter[from]")
		w.Header().Set("Content-Type", "application/json")
		// The payload deliberately includes body-bearing / over-collection
		// fields (message, samples, the rule query, a user.email tag) that the
		// client MUST NOT decode into RawSignal.
		_, _ = w.Write([]byte(`{
		  "data": [
		    {
		      "id": "sig-aaa",
		      "type": "signal",
		      "attributes": {
		        "timestamp": "2026-06-07T10:00:00Z",
		        "status": "high",
		        "message": "User bob@corp.test from 10.1.2.3 triggered brute force; raw log: password=hunter2",
		        "samples": [{"raw": "Jun 7 authentication failure for bob@corp.test"}],
		        "tags": ["user.email:bob@corp.test", "host:db-01", "source_ip:10.1.2.3"],
		        "workflow": {
		          "triage_state": "archived",
		          "archived_by": "alice-sec",
		          "triage_state_updated_at": "2026-06-07T11:05:00Z"
		        },
		        "custom": {
		          "rule_id": "rule-aaa",
		          "rule_name": "Brute force on login",
		          "query": "@evt.name:authentication source:secret_value",
		          "matched_event": {"payload": "SECRET-FIXTURE"}
		        }
		      }
		    }
		  ],
		  "meta": {"page": {"after": ""}}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-datadog-api-key", "test-datadog-app-key")
	since := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got, err := c.ListSignals(context.Background(), since)
	if err != nil {
		t.Fatalf("ListSignals: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s; want GET (read-only)", gotMethod)
	}
	if gotAPIKey != "test-datadog-api-key" || gotAppKey != "test-datadog-app-key" {
		t.Errorf("auth headers not set: api=%q app=%q", gotAPIKey, gotAppKey)
	}
	if gotFrom != since.Format(time.RFC3339) {
		t.Errorf("filter[from] = %q; want %q", gotFrom, since.Format(time.RFC3339))
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	s := got[0]
	if s.ID != "sig-aaa" || s.RuleID != "rule-aaa" || s.RuleName != "Brute force on login" {
		t.Errorf("signal wrong: %+v", s)
	}
	if s.Severity != "high" || s.Status != "archived" || s.TriagerHandle != "alice-sec" {
		t.Errorf("triage metadata wrong: %+v", s)
	}
	if s.FirstSeen.IsZero() || s.Triaged.IsZero() {
		t.Errorf("timestamps not parsed: %+v", s)
	}
	// The over-collection proof: RawSignal has no field for the message /
	// samples / query / tags, so they could not have leaked. Belt-and-suspenders
	// — assert no decoded string field carries the secret/PII markers.
	for _, v := range []string{s.ID, s.RuleID, s.RuleName, s.Severity, s.Status, s.TriagerHandle} {
		for _, bad := range []string{"hunter2", "SECRET-FIXTURE", "bob@corp.test", "10.1.2.3", "secret_value"} {
			if strings.Contains(v, bad) {
				t.Errorf("decoded field %q leaked over-collected content %q", v, bad)
			}
		}
	}
}

// TestClient_ListSignals_EpochMillisTimestamp covers the epoch-millis timestamp
// branch of parseTime.
func TestClient_ListSignals_EpochMillisTimestamp(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"s","attributes":{"timestamp":"1717752000000","status":"low","custom":{"rule_id":"r"}}}],"meta":{"page":{"after":""}}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	got, err := c.ListSignals(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("ListSignals: %v", err)
	}
	if got[0].FirstSeen.IsZero() {
		t.Error("epoch-millis timestamp not parsed")
	}
}

// TestClient_ListSignals_AssigneeFallback covers the assignee triager fallback
// when archived_by is empty.
func TestClient_ListSignals_AssigneeFallback(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"s","attributes":{"status":"medium","workflow":{"triage_state":"under_review","assignee_id":"assignee-99"},"custom":{"rule_id":"r"}}}],"meta":{"page":{"after":""}}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	got, _ := c.ListSignals(context.Background(), time.Time{})
	if got[0].TriagerHandle != "assignee-99" {
		t.Errorf("assignee fallback not used: %q", got[0].TriagerHandle)
	}
}

// TestClient_ListSignals_Paginates walks a two-page cursor response.
func TestClient_ListSignals_Paginates(t *testing.T) {
	t.Parallel()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		cursor := r.URL.Query().Get("page[cursor]")
		w.Header().Set("Content-Type", "application/json")
		switch cursor {
		case "":
			_, _ = w.Write([]byte(`{"data":[{"id":"s1","attributes":{"status":"low","custom":{"rule_id":"r1"}}}],"meta":{"page":{"after":"CUR2"}}}`))
		case "CUR2":
			_, _ = w.Write([]byte(`{"data":[{"id":"s2","attributes":{"status":"high","custom":{"rule_id":"r2"}}}],"meta":{"page":{"after":""}}}`))
		default:
			t.Errorf("unexpected cursor %q", cursor)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	got, err := c.ListSignals(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("ListSignals: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d; want 2 pages", calls)
	}
	if len(got) != 2 || got[0].ID != "s1" || got[1].ID != "s2" {
		t.Fatalf("pagination concat wrong: %+v", got)
	}
}

// TestClient_ListSignals_CapTerminates asserts the per-run page cap stops an
// unbounded source with ErrSignalCapExceeded (DoS / over-collection guard).
func TestClient_ListSignals_CapTerminates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("page[cursor]")
		if n == "" {
			n = "0"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"id":"s-%s","attributes":{"status":"low","custom":{"rule_id":"r"}}}],"meta":{"page":{"after":"%s-next"}}}`, n, n)
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	_, err := c.ListSignals(context.Background(), time.Time{})
	if err == nil {
		t.Fatal("want cap-exceeded error on a non-terminating source")
	}
	if err.Error() != ErrSignalCapExceeded.Error() {
		t.Errorf("err = %v; want ErrSignalCapExceeded", err)
	}
}

func TestClient_ListSignals_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["Forbidden"]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	_, err := c.ListSignals(context.Background(), time.Time{})
	if err == nil {
		t.Fatal("want HTTP error")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusForbidden {
		t.Errorf("want APIError 403; got %v", err)
	}
}

func TestAPIError_Message(t *testing.T) {
	t.Parallel()
	if got := (&APIError{Status: 500}).Error(); !strings.Contains(got, "500") {
		t.Errorf("error without body = %q", got)
	}
	if got := (&APIError{Status: 403, Body: "nope"}).Error(); !strings.Contains(got, "nope") {
		t.Errorf("error with body = %q", got)
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://api.datadoghq.com", "k", "a")
	if c.HTTP == nil {
		t.Error("default HTTP client not set")
	}
}
