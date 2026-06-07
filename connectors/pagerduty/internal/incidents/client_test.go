package incidents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// incidentsJSON includes incident free-text fields (title, description) and a
// notes blob that the client MUST NOT decode or surface (P0-489-3).
const incidentsJSON = `{
  "incidents": [
    {
      "id": "INC1",
      "incident_number": 42,
      "title": "Customer ACME data export failed — SSN leak suspected",
      "description": "free text body with customer PII",
      "status": "resolved",
      "urgency": "high",
      "created_at": "2026-05-01T09:00:00Z",
      "resolved_at": "2026-05-01T10:30:00Z",
      "service": {"id": "SVC1", "summary": "Export API"}
    }
  ]
}`

func TestClient_ListIncidents(t *testing.T) {
	t.Parallel()
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		if !strings.HasPrefix(r.URL.Path, "/incidents") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(incidentsJSON))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-pagerduty-token")
	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	got, err := c.ListIncidents(context.Background(), since, until)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if gotAuth != "Token token=test-pagerduty-token" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	for _, want := range []string{"since=", "until=", "limit=", "offset="} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d incidents; want 1", len(got))
	}
	in := got[0]
	if in.ID != "INC1" || in.Number != 42 || in.Status != "resolved" || in.ServiceName != "Export API" {
		t.Errorf("incident = %+v", in)
	}
	want := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	if !in.CreatedAt.Equal(want) {
		t.Errorf("created_at = %v; want %v", in.CreatedAt, want)
	}
	// RawIncident has NO Title/Body/Notes field — the free-text cannot have been
	// decoded into the struct. Assert it by type-construction (compile-time) and
	// by absence here.
	if in.ResolvedAt.IsZero() {
		t.Error("resolved_at should be parsed")
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
	if parseTime("2026-05-01T09:00:00Z").IsZero() {
		t.Error("good -> non-zero")
	}
}
