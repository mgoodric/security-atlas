package jiratickets_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiratickets"
)

// replay serves testdata/search_issues.json and records the fields
// parameter each search sent.
func replay(t *testing.T) (*jiratickets.Client, *[]string) {
	t.Helper()
	body, err := os.ReadFile("testdata/search_issues.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fields []string
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search", func(w http.ResponseWriter, r *http.Request) {
		fields = append(fields, r.URL.Query().Get("fields"))
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "abc"})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	return jiratickets.NewClient(srv.Client(), srv.URL, creds), &fields
}

func at(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return &t
}

type want struct {
	accountID  string
	labels     []string
	priority   string
	severity   string
	created    *time.Time
	resolved   *time.Time
	resolution string
}

func check(t *testing.T, got []jiratickets.Ticket, wants map[string]want) {
	t.Helper()
	if len(got) != len(wants) {
		t.Fatalf("len(tickets) = %d, want %d", len(got), len(wants))
	}
	for _, tk := range got {
		w, ok := wants[tk.TicketKey]
		if !ok {
			t.Fatalf("unexpected ticket %s", tk.TicketKey)
		}
		if tk.AssigneeAccountID != w.accountID {
			t.Errorf("%s: AssigneeAccountID = %q, want %q", tk.TicketKey, tk.AssigneeAccountID, w.accountID)
		}
		if !reflect.DeepEqual(tk.Labels, w.labels) {
			t.Errorf("%s: Labels = %v, want %v", tk.TicketKey, tk.Labels, w.labels)
		}
		if tk.Priority != w.priority {
			t.Errorf("%s: Priority = %q, want %q", tk.TicketKey, tk.Priority, w.priority)
		}
		if tk.Severity != w.severity {
			t.Errorf("%s: Severity = %q, want %q", tk.TicketKey, tk.Severity, w.severity)
		}
		if !reflect.DeepEqual(tk.CreatedAt, w.created) {
			t.Errorf("%s: CreatedAt = %v, want %v", tk.TicketKey, tk.CreatedAt, w.created)
		}
		if !reflect.DeepEqual(tk.ResolvedAt, w.resolved) {
			t.Errorf("%s: ResolvedAt = %v, want %v", tk.TicketKey, tk.ResolvedAt, w.resolved)
		}
		if tk.Resolution != w.resolution {
			t.Errorf("%s: Resolution = %q, want %q", tk.TicketKey, tk.Resolution, w.resolution)
		}
	}
}

// #2829: the replayed search yields labels, priority, severity read from
// the system priority, and created / resolution dates in UTC.
func TestList_DocumentedShapes_SeverityFromPriority(t *testing.T) {
	c, fields := replay(t)
	got, err := jiratickets.List(context.Background(), c, jiratickets.ListOpts{JQL: "project in (SEC, VULN)"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	check(t, got, map[string]want{
		"SEC-101": {
			accountID: "5b10a2844c20165700ede21g", labels: []string{"security-training", "fy26"},
			priority: "Medium", severity: "medium",
			created: at("2026-02-01T09:00:00Z"), resolved: at("2026-02-03T15:45:12.307Z"), resolution: "Done",
		},
		"VULN-7": {
			labels: []string{"vulnerability"}, priority: "Highest", severity: "critical",
			created: at("2026-11-18T07:32:47Z"),
		},
		"VULN-8": {
			accountID: "5b10ac8d82e05b22cc7d4ef5", priority: "Trivial",
			created: at("2026-10-01T07:00:00Z"), resolved: at("2026-10-20T19:00:00Z"), resolution: "Done",
		},
		"VULN-9": {},
	})
	wantFields := "summary,status,resolution,assignee,project,labels,priority,created,resolutiondate"
	if len(*fields) != 1 || (*fields)[0] != wantFields {
		t.Errorf("fields param = %v, want [%s]", *fields, wantFields)
	}
}

// #2829: with --severity-field naming a custom field, severity comes from
// that field's option value (or plain string) and the field is requested.
func TestList_DocumentedShapes_SeverityFromCustomField(t *testing.T) {
	c, fields := replay(t)
	got, err := jiratickets.List(context.Background(), c, jiratickets.ListOpts{
		JQL: "project = VULN", SeverityField: "customfield_10042",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sev := map[string]string{}
	for _, tk := range got {
		sev[tk.TicketKey] = tk.Severity
	}
	wantSev := map[string]string{"SEC-101": "", "VULN-7": "high", "VULN-8": "", "VULN-9": "critical"}
	if !reflect.DeepEqual(sev, wantSev) {
		t.Errorf("severity = %v, want %v", sev, wantSev)
	}
	if len(*fields) != 1 || !strings.HasSuffix((*fields)[0], ",resolutiondate,customfield_10042") {
		t.Errorf("fields param = %v, want the base fields plus customfield_10042", *fields)
	}
}

func TestNormalizeSeverity(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Critical": "critical", "HIGHEST": "critical", " high ": "high", "Medium": "medium",
		"low": "low", "Lowest": "low", "Major": "", "P1": "", "": "",
	}
	for in, w := range cases {
		if got := jiratickets.NormalizeSeverity(in); got != w {
			t.Errorf("NormalizeSeverity(%q) = %q, want %q", in, got, w)
		}
	}
}

func TestParseJiraTime(t *testing.T) {
	t.Parallel()
	cases := map[string]*time.Time{
		"2022-10-06T07:32:47.000+0000": at("2022-10-06T07:32:47Z"),
		"2022-10-06T07:32:47.123-0130": at("2022-10-06T09:02:47.123Z"),
		"2022-10-06T07:32:47Z":         at("2022-10-06T07:32:47Z"),
		"2022-10-06":                   nil,
		"":                             nil,
	}
	for in, w := range cases {
		if got := jiratickets.ParseJiraTime(in); !reflect.DeepEqual(got, w) {
			t.Errorf("ParseJiraTime(%q) = %v, want %v", in, got, w)
		}
	}
}
