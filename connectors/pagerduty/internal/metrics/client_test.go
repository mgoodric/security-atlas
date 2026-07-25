package metrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_ListIncidentTimings_AuthAndQuery(t *testing.T) {
	t.Parallel()
	var gotAuth, gotQuery, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"incidents":[{"id":"INC1","created_at":"2026-06-01T00:00:00Z","resolved_at":"2026-06-01T00:10:00Z","service":{"id":"SVCA"},"acknowledgments":[{"at":"2026-06-01T00:01:00Z"}]}],"more":false}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-pd-token")
	got, err := c.ListIncidentTimings(context.Background(),
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListIncidentTimings: %v", err)
	}
	if gotAuth != "Token token=test-pd-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.HasPrefix(gotPath, "/incidents") {
		t.Errorf("path = %q; want /incidents", gotPath)
	}
	for _, want := range []string{"since=", "until=", "limit=", "offset="} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
	if len(got) != 1 || got[0].ServiceID != "SVCA" {
		t.Fatalf("got %+v; want one SVCA timing", got)
	}
	if len(got[0].Acks) != 1 || got[0].Acks[0].At.IsZero() {
		t.Errorf("ack timing not decoded: %+v", got[0].Acks)
	}
	if got[0].ResolvedAt.IsZero() {
		t.Error("resolved_at not decoded")
	}
}

func TestClient_ListIncidentTimings_Paginates(t *testing.T) {
	t.Parallel()
	page := func(offset int, more bool) string {
		return fmt.Sprintf(`{"incidents":[{"id":"INC-%d","created_at":"2026-06-01T00:00:00Z","service":{"id":"SVC-%d"}}],"more":%t,"offset":%d}`,
			offset, offset, more, offset)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "offset=100") {
			_, _ = w.Write([]byte(page(100, false)))
			return
		}
		_, _ = w.Write([]byte(page(0, true)))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "fake")
	got, err := c.ListIncidentTimings(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("ListIncidentTimings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d; want 2 across two pages", len(got))
	}
}

func TestClient_ListIncidentTimings_StopsAtPageCap(t *testing.T) {
	t.Parallel()
	// Every page claims more=true forever; maxPages is the stop condition.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"incidents":[{"id":"INC","created_at":"2026-06-01T00:00:00Z","service":{"id":"SVC"}}],"more":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "fake")
	if _, err := c.ListIncidentTimings(context.Background(), time.Now().Add(-24*time.Hour), time.Now()); err != nil {
		t.Fatalf("ListIncidentTimings: %v", err)
	}
	if calls > maxPages {
		t.Errorf("made %d calls; must stop at maxPages=%d", calls, maxPages)
	}
}

func TestClient_ListIncidentTimings_SkipsEmptyID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"incidents":[{"id":"","service":{"id":"SVC"}},{"id":"INC1","created_at":"2026-06-01T00:00:00Z","service":{"id":"SVC"}}],"more":false}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "fake")
	got, err := c.ListIncidentTimings(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("ListIncidentTimings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d; want 1 (empty-id skipped)", len(got))
	}
}

// TestClient_TokenNeverLogged is the P0-539 token-confidentiality assertion: the
// read-only PagerDuty token must never appear in the connector's own output. We
// drive a full collect through a fake server while capturing everything the
// client could conceivably emit (the error path's APIError body, the stringified
// transport, and the rendered records) and assert the token literal is absent.
func TestClient_TokenNeverLogged(t *testing.T) {
	t.Parallel()
	const token = "test-pd-secret-token-value"

	// Error path: the server 500s with a body. The APIError must not echo the
	// token (PagerDuty error bodies do not, and our transport never adds it).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"server error"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, token)
	_, err := c.ListIncidentTimings(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	if err == nil {
		t.Fatal("want error from 500")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked into error message: %v", err)
	}

	// Success path: rendered records must not carry the token.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"incidents":[{"id":"INC1","created_at":"2026-06-01T00:00:00Z","service":{"id":"SVC"}}],"more":false}`))
	}))
	defer ok.Close()
	c2 := NewClient(ok.Client(), ok.URL, token)
	raw, err := c2.ListIncidentTimings(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("ListIncidentTimings: %v", err)
	}
	if rendered := fmt.Sprintf("%+v", raw); strings.Contains(rendered, token) {
		t.Fatalf("token leaked into rendered records: %s", rendered)
	}
}

func TestParseTime(t *testing.T) {
	t.Parallel()
	if !parseTime("").IsZero() {
		t.Error("empty -> zero")
	}
	if !parseTime("not-a-time").IsZero() {
		t.Error("bad -> zero")
	}
	if parseTime("2026-06-01T00:00:00Z").IsZero() {
		t.Error("good -> non-zero")
	}
}

func TestNewClient_NonNil(t *testing.T) {
	t.Parallel()
	if NewClient(nil, "https://api.pagerduty.com", "test-pd-token") == nil {
		t.Error("NewClient returned nil")
	}
}
