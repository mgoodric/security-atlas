package firingevents

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClient_ListMonitorEvents_DecodesBodyFreeFields is the load-bearing
// over-collection DROP test (P0-535, Information Disclosure DOMINANT): the
// source event carries an alert MESSAGE body, triggering METRIC VALUES, a secret
// WEBHOOK URL, and recipient PII — none of which may reach a record. It also
// asserts the request is a read-only GET, scoped to monitor_alert events, with
// the auth headers + look-back window set.
func TestClient_ListMonitorEvents_DecodesBodyFreeFields(t *testing.T) {
	t.Parallel()
	var gotAPIKey, gotAppKey, gotMethod, gotSources, gotStart string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("DD-API-KEY")
		gotAppKey = r.Header.Get("DD-APPLICATION-KEY")
		gotMethod = r.Method
		gotSources = r.URL.Query().Get("sources")
		gotStart = r.URL.Query().Get("start")
		w.Header().Set("Content-Type", "application/json")
		// The payload deliberately embeds over-collection surfaces the client
		// MUST NOT decode: an alert message body, the triggering metric values,
		// the secret webhook URL, and a recipient email.
		_, _ = w.Write([]byte(`{
		  "events": [
		    {
		      "id": 99,
		      "alert_type": "error",
		      "monitor_id": 12345,
		      "date_happened": 1717752000,
		      "title": "[Triggered] CPU high @slack-sec-oncall",
		      "text": "test-message-body-should-be-dropped: host db-01 cpu=97.5 over threshold; page bob@corp.test",
		      "monitor_groups": ["host:db-01"],
		      "tags": ["metric_value:97.5", "user.email:bob@corp.test"],
		      "webhook_url": "https://example.test/secret-webhook-should-be-dropped",
		      "values": [97.5, 98.1]
		    }
		  ]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-datadog-api-key", "test-datadog-app-key")
	since := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got, err := c.ListMonitorEvents(context.Background(), since)
	if err != nil {
		t.Fatalf("ListMonitorEvents: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s; want GET (read-only)", gotMethod)
	}
	if gotSources != "monitor_alert" {
		t.Errorf("sources = %q; want monitor_alert", gotSources)
	}
	if gotStart != fmt.Sprintf("%d", since.Unix()) {
		t.Errorf("start = %q; want %d", gotStart, since.Unix())
	}
	if gotAPIKey != "test-datadog-api-key" || gotAppKey != "test-datadog-app-key" {
		t.Errorf("auth headers not set: api=%q app=%q", gotAPIKey, gotAppKey)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	e := got[0]
	if e.RuleID != "12345" || e.State != "error" || e.TargetHandle != "slack-sec-oncall" {
		t.Errorf("event wrong: %+v", e)
	}
	if e.FiredAt.IsZero() {
		t.Error("fired_at not parsed")
	}
	// The over-collection proof: RawFiring has no field for the message / metric
	// values / webhook URL / email, so they could not have leaked. Belt-and-
	// suspenders — assert no decoded string field carries the forbidden markers.
	for _, v := range []string{e.RuleID, e.State, e.TargetHandle, e.TargetKind} {
		for _, bad := range []string{
			"test-message-body-should-be-dropped",
			"secret-webhook-should-be-dropped",
			"bob@corp.test", "97.5", "98.1", "db-01",
		} {
			if strings.Contains(v, bad) {
				t.Errorf("decoded field %q leaked over-collected content %q", v, bad)
			}
		}
	}
}

func TestClient_ListMonitorEvents_DropsEmailHandle(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"alert_type":"error","monitor_id":1,"date_happened":1717752000,"title":"alert @victim@corp.test"}]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	got, err := c.ListMonitorEvents(context.Background(), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("ListMonitorEvents: %v", err)
	}
	if len(got) != 1 || got[0].TargetHandle != "" {
		t.Errorf("email handle not skipped by firstHandle: %+v", got)
	}
}

func TestClient_ListMonitorEvents_SkipsInvalid(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[
		  {"alert_type":"error","monitor_id":0,"date_happened":1717752000},
		  {"alert_type":"success","monitor_id":7,"date_happened":0},
		  {"alert_type":"success","monitor_id":7,"date_happened":1717752000}
		]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	got, _ := c.ListMonitorEvents(context.Background(), time.Unix(1, 0))
	if len(got) != 1 || got[0].RuleID != "7" {
		t.Errorf("invalid events not skipped: %+v", got)
	}
}

// TestClient_ListMonitorEvents_Paginates walks two time-windowed pages.
func TestClient_ListMonitorEvents_Paginates(t *testing.T) {
	t.Parallel()
	var calls int
	// First page returns a full page (pageLimit events all at the same old
	// instant), forcing a second page; the second returns a short page to stop.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			var b strings.Builder
			b.WriteString(`{"events":[`)
			for i := 0; i < pageLimit; i++ {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"alert_type":"error","monitor_id":%d,"date_happened":1717760000}`, i+1)
			}
			b.WriteString(`]}`)
			_, _ = w.Write([]byte(b.String()))
			return
		}
		_, _ = w.Write([]byte(`{"events":[{"alert_type":"success","monitor_id":999,"date_happened":1717752000}]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	got, err := c.ListMonitorEvents(context.Background(), time.Unix(1717740000, 0))
	if err != nil {
		t.Fatalf("ListMonitorEvents: %v", err)
	}
	if calls < 2 {
		t.Errorf("calls = %d; want >= 2 pages", calls)
	}
	if len(got) != pageLimit+1 {
		t.Errorf("len = %d; want %d", len(got), pageLimit+1)
	}
}

// TestClient_ListMonitorEvents_CapTerminates asserts the per-run page cap stops
// an unbounded source with ErrEventCapExceeded (DoS guard).
func TestClient_ListMonitorEvents_CapTerminates(t *testing.T) {
	t.Parallel()
	var seq int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Always return a full page whose oldest event is strictly newer than
		// `since`, so the loop never terminates on its own and must hit the cap.
		var b strings.Builder
		b.WriteString(`{"events":[`)
		for i := 0; i < pageLimit; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			seq++
			// date_happened far in the future so it always stays > since.
			fmt.Fprintf(&b, `{"alert_type":"error","monitor_id":%d,"date_happened":4000000000}`, seq)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	_, err := c.ListMonitorEvents(context.Background(), time.Unix(1, 0))
	if err == nil {
		t.Fatal("want cap-exceeded error on a non-terminating source")
	}
	if err.Error() != ErrEventCapExceeded.Error() {
		t.Errorf("err = %v; want ErrEventCapExceeded", err)
	}
}

func TestClient_ListMonitorEvents_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["Forbidden"]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "k", "a")
	_, err := c.ListMonitorEvents(context.Background(), time.Unix(1, 0))
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

func TestFirstHandle(t *testing.T) {
	t.Parallel()
	if got := firstHandle(""); got != "" {
		t.Errorf("empty title = %q", got)
	}
	if got := firstHandle("no mentions here"); got != "" {
		t.Errorf("no-handle = %q", got)
	}
	if got := firstHandle("alert @slack-x and @pd-y"); got != "slack-x" {
		t.Errorf("first handle = %q; want slack-x", got)
	}
	if got := firstHandle("alert @a@b.test then @real-one"); got != "real-one" {
		t.Errorf("email-skip = %q; want real-one", got)
	}
}
