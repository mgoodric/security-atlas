package lineartickets_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/lineartickets"
)

func newLinearFakeServer(t *testing.T, respBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Errorf("Linear server hit non-graphql path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("Linear server got method %s; want POST", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("Linear server got empty Authorization header")
		}
		if strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("Linear server got Bearer prefix; Linear API rejects it: %q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "issues(") {
			t.Errorf("Linear query missing issues(): %s", body)
		}
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestList_DecodesCanonicalFields(t *testing.T) {
	const body = `{
		"data": {
			"issues": {
				"nodes": [{
					"identifier": "ENG-42",
					"title": "Incident: payment 5xx spike",
					"url": "https://linear.app/example/issue/ENG-42",
					"state": {"name": "Done"},
					"assignee": {"name": "Bob Jones"},
					"team": {"key": "ENG"}
				}],
				"pageInfo": {"hasNextPage": false, "endCursor": null}
			}
		}
	}`
	srv := newLinearFakeServer(t, body)
	creds, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "lin_api_test"})
	if err != nil {
		t.Fatalf("ResolveLinear: %v", err)
	}
	c := lineartickets.NewClient(srv.Client(), srv.URL, creds)
	tickets, err := lineartickets.List(context.Background(), c, lineartickets.ListOpts{
		Filter: lineartickets.Filter{TeamKey: "ENG"},
		Now:    func() time.Time { return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("len(tickets) = %d; want 1", len(tickets))
	}
	tk := tickets[0]
	if tk.TicketKey != "ENG-42" {
		t.Errorf("TicketKey = %q; want ENG-42", tk.TicketKey)
	}
	if tk.ProjectKey != "ENG" {
		t.Errorf("ProjectKey = %q; want ENG", tk.ProjectKey)
	}
	if tk.Summary != "Incident: payment 5xx spike" {
		t.Errorf("Summary = %q", tk.Summary)
	}
	if tk.Status != "Done" {
		t.Errorf("Status = %q; want Done", tk.Status)
	}
	if tk.Assignee != "Bob Jones" {
		t.Errorf("Assignee = %q; want Bob Jones", tk.Assignee)
	}
	if tk.URL != "https://linear.app/example/issue/ENG-42" {
		t.Errorf("URL = %q", tk.URL)
	}
}

func TestList_HandlesUnassigned(t *testing.T) {
	const body = `{
		"data": {
			"issues": {
				"nodes": [{
					"identifier": "ENG-43",
					"title": "Open work item",
					"url": "https://linear.app/example/issue/ENG-43",
					"state": {"name": "Todo"},
					"assignee": null,
					"team": {"key": "ENG"}
				}],
				"pageInfo": {"hasNextPage": false, "endCursor": null}
			}
		}
	}`
	srv := newLinearFakeServer(t, body)
	creds, _ := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "lin_api_test"})
	c := lineartickets.NewClient(srv.Client(), srv.URL, creds)
	tickets, err := lineartickets.List(context.Background(), c, lineartickets.ListOpts{Filter: lineartickets.Filter{TeamKey: "ENG"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("len(tickets) = %d", len(tickets))
	}
	if tickets[0].Assignee != "" {
		t.Errorf("Assignee = %q; want empty (unassigned)", tickets[0].Assignee)
	}
}

func TestList_PropagatesGraphQLErrors(t *testing.T) {
	const body = `{"errors":[{"message":"Authentication required"}]}`
	srv := newLinearFakeServer(t, body)
	creds, _ := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "lin_api_test"})
	c := lineartickets.NewClient(srv.Client(), srv.URL, creds)
	_, err := lineartickets.List(context.Background(), c, lineartickets.ListOpts{Filter: lineartickets.Filter{TeamKey: "ENG"}})
	if err == nil {
		t.Fatal("expected error on GraphQL errors block; got nil")
	}
	if !strings.Contains(err.Error(), "Authentication required") {
		t.Errorf("error missing GraphQL message: %v", err)
	}
}

func TestList_PropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	creds, _ := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "lin_api_test"})
	c := lineartickets.NewClient(srv.Client(), srv.URL, creds)
	_, err := lineartickets.List(context.Background(), c, lineartickets.ListOpts{Filter: lineartickets.Filter{TeamKey: "ENG"}})
	if err == nil {
		t.Fatal("expected error on 401; got nil")
	}
}

func TestList_RejectsNilAPI(t *testing.T) {
	if _, err := lineartickets.List(context.Background(), nil, lineartickets.ListOpts{Filter: lineartickets.Filter{TeamKey: "ENG"}}); err == nil {
		t.Fatal("expected error on nil API; got nil")
	}
}

func TestList_PaginatesViaCursor(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{
				"data": {"issues": {
					"nodes": [{"identifier":"ENG-1","title":"a","url":"u","state":{"name":"Done"},"team":{"key":"ENG"}}],
					"pageInfo": {"hasNextPage": true, "endCursor": "c1"}
				}}}`))
			return
		}
		// Page 2 — confirm endCursor was threaded through.
		vars, _ := payload["variables"].(map[string]any)
		if vars["after"] != "c1" {
			t.Errorf("page 2 missing after=c1; got %v", vars["after"])
		}
		_, _ = w.Write([]byte(`{
			"data": {"issues": {
				"nodes": [{"identifier":"ENG-2","title":"b","url":"u","state":{"name":"Done"},"team":{"key":"ENG"}}],
				"pageInfo": {"hasNextPage": false, "endCursor": null}
			}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "lin_api_test"})
	c := lineartickets.NewClient(srv.Client(), srv.URL, creds)
	tickets, err := lineartickets.List(context.Background(), c, lineartickets.ListOpts{Filter: lineartickets.Filter{TeamKey: "ENG"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 2 {
		t.Fatalf("len(tickets) = %d; want 2 (across pages)", len(tickets))
	}
	if calls != 2 {
		t.Errorf("server saw %d calls; want 2", calls)
	}
}
