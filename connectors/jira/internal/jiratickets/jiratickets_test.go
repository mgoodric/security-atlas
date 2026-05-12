package jiratickets_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiratickets"
)

func newJiraFakeServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("Jira API received non-Basic Authorization: %q", auth)
		}
		decoded, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
		if !strings.Contains(string(decoded), ":") {
			t.Errorf("Jira API received malformed Basic header (no colon): %q", auth)
		}
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestList_DecodesCanonicalFields(t *testing.T) {
	const body = `{
		"issues": [{
			"key": "PROJ-123",
			"fields": {
				"summary": "Change ticket: deploy api 4.2.0",
				"status": {"name": "Done"},
				"resolution": {"name": "Fixed"},
				"assignee": {"displayName": "Alice Smith"},
				"project": {"key": "PROJ"}
			}
		}]
	}`
	srv := newJiraFakeServer(t, body)
	creds, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "abc"})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	c := jiratickets.NewClient(srv.Client(), srv.URL, creds)
	tickets, err := jiratickets.List(context.Background(), c, jiratickets.ListOpts{
		JQL: `project = PROJ AND status changed AFTER -90d`,
		Now: func() time.Time { return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("len(tickets) = %d; want 1", len(tickets))
	}
	tk := tickets[0]
	if tk.TicketKey != "PROJ-123" {
		t.Errorf("TicketKey = %q; want PROJ-123", tk.TicketKey)
	}
	if tk.ProjectKey != "PROJ" {
		t.Errorf("ProjectKey = %q; want PROJ", tk.ProjectKey)
	}
	if tk.Summary != "Change ticket: deploy api 4.2.0" {
		t.Errorf("Summary = %q", tk.Summary)
	}
	if tk.Status != "Done" {
		t.Errorf("Status = %q; want Done", tk.Status)
	}
	if tk.Resolution != "Fixed" {
		t.Errorf("Resolution = %q; want Fixed", tk.Resolution)
	}
	if tk.Assignee != "Alice Smith" {
		t.Errorf("Assignee = %q; want Alice Smith", tk.Assignee)
	}
	if tk.URL == "" {
		t.Errorf("URL empty — should be %s/browse/PROJ-123", srv.URL)
	}
	if tk.ObservedAt.IsZero() {
		t.Error("ObservedAt is zero")
	}
}

func TestList_HandlesUnassignedAndUnresolved(t *testing.T) {
	const body = `{
		"issues": [{
			"key": "PROJ-9",
			"fields": {
				"summary": "Open bug",
				"status": {"name": "In Progress"},
				"resolution": null,
				"assignee": null,
				"project": {"key": "PROJ"}
			}
		}]
	}`
	srv := newJiraFakeServer(t, body)
	creds, _ := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "abc"})
	c := jiratickets.NewClient(srv.Client(), srv.URL, creds)
	tickets, err := jiratickets.List(context.Background(), c, jiratickets.ListOpts{JQL: "project = PROJ"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("len(tickets) = %d; want 1", len(tickets))
	}
	if tickets[0].Resolution != "" {
		t.Errorf("Resolution = %q; want empty (unresolved)", tickets[0].Resolution)
	}
	if tickets[0].Assignee != "" {
		t.Errorf("Assignee = %q; want empty (unassigned)", tickets[0].Assignee)
	}
}

func TestList_PropagatesAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errorMessages":["Unauthorized"]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "abc"})
	c := jiratickets.NewClient(srv.Client(), srv.URL, creds)
	_, err := jiratickets.List(context.Background(), c, jiratickets.ListOpts{JQL: "project = PROJ"})
	if err == nil {
		t.Fatal("expected error on 401; got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error doesn't mention status: %v", err)
	}
}

func TestList_RequiresJQL(t *testing.T) {
	creds, _ := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "abc"})
	c := jiratickets.NewClient(nil, "http://unused", creds)
	if _, err := jiratickets.List(context.Background(), c, jiratickets.ListOpts{}); err == nil {
		t.Fatal("expected error on missing JQL; got nil")
	}
}

func TestList_RejectsNilAPI(t *testing.T) {
	if _, err := jiratickets.List(context.Background(), nil, jiratickets.ListOpts{JQL: "project = X"}); err == nil {
		t.Fatal("expected error on nil API; got nil")
	}
}
