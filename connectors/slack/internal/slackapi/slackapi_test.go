package slackapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackauth"
)

// newTestClient points a Client at a test server for both the base and audit
// roots.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	tok, err := slackauth.NewToken("xoxb-test-secret")
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	c := New(tok, false)
	c.httpClient = srv.Client()
	c.baseURL = srv.URL
	c.auditURL = srv.URL
	return c
}

func TestResolveTeam(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/team.info") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		// Token must arrive on the Authorization header.
		if got := r.Header.Get("Authorization"); got != "Bearer xoxb-test-secret" {
			t.Errorf("Authorization = %q; want Bearer token", got)
		}
		_, _ = w.Write([]byte(`{"ok":true,"team":{"id":"T0001","name":"Acme"}}`))
	}))
	defer srv.Close()

	id, err := newTestClient(t, srv).ResolveTeam(context.Background())
	if err != nil {
		t.Fatalf("ResolveTeam: %v", err)
	}
	if id.TeamID != "T0001" {
		t.Errorf("TeamID = %q; want T0001", id.TeamID)
	}
}

func TestListMembers_MetadataOnly(t *testing.T) {
	t.Parallel()
	// The server returns a member record plus a message-like field the
	// connector must IGNORE — the decoder has no field for it, proving the
	// adapter cannot over-collect even if Slack returns extra data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"members":[
			{"id":"U1","name":"alice","is_admin":true,"has_2fa":true,
			 "profile":{"display_name":"Alice","status_text":"secret note"},
			 "last_message":"this is a private message body"}
		],"response_metadata":{"next_cursor":""}}`))
	}))
	defer srv.Close()

	members, next, err := newTestClient(t, srv).ListMembers(context.Background(), "")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if next != "" {
		t.Errorf("next = %q; want empty", next)
	}
	if len(members) != 1 || members[0].UserID != "U1" || members[0].Handle != "Alice" {
		t.Fatalf("members = %+v; want one U1/Alice", members)
	}
	if !members[0].Has2FA || !members[0].IsAdmin {
		t.Error("admin/2fa flags not decoded")
	}
}

func TestListAuditEvents(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/logs") {
			t.Errorf("audit path = %q; want /logs", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"entries":[
			{"id":"e1","date_create":1700000000,"action":"user_login",
			 "actor":{"user":{"id":"U1","email":"alice@acme.test"}},
			 "entity":{"type":"workspace"}}
		],"response_metadata":{"next_cursor":""}}`))
	}))
	defer srv.Close()

	events, _, err := newTestClient(t, srv).ListAuditEvents(context.Background(), "")
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != "user_login" || events[0].ActorEmail != "alice@acme.test" {
		t.Fatalf("events = %+v", events)
	}
}

func TestGetRetention(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"prefs":{"retention_duration":90,"file_retention_duration":30,"retention_type":1}}`))
	}))
	defer srv.Close()

	s, err := newTestClient(t, srv).GetRetention(context.Background())
	if err != nil {
		t.Fatalf("GetRetention: %v", err)
	}
	if !s.RetentionEnabled || s.MessagesRetentionDays != 90 {
		t.Errorf("retention = %+v; want enabled/90", s)
	}
}

func TestGet_NonOKStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).ResolveTeam(context.Background())
	if err == nil {
		t.Fatal("403 should error")
	}
	// The error must not leak the token.
	if strings.Contains(err.Error(), "xoxb-test-secret") {
		t.Errorf("error leaked token: %v", err)
	}
}
