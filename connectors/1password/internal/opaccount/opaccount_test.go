package opaccount_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opaccount"
	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
)

const account = "M4E2SWNZAZFTRGQGDNS2E5A4MU"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func creds(t *testing.T) opauth.Credentials {
	t.Helper()
	t.Setenv(opauth.EnvEventsToken, "test-events-token")
	t.Setenv(opauth.EnvUsersClientID, "test-client-id")
	t.Setenv(opauth.EnvUsersClientSecret, "test-client-secret")
	c, err := opauth.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fake serves both APIs from the documented fixtures and checks every
// request carries the credential its API documents. Handlers can be
// overridden per test.
type fake struct {
	introspect string
	token      string
	pages      map[string]string // page_token -> body
	status     map[string]int    // path -> forced status
	methods    []string
	userCalls  atomic.Int32
}

func newFake(t *testing.T) *fake {
	return &fake{
		introspect: fixture(t, "introspect.json"),
		token:      fixture(t, "token.json"),
		pages:      map[string]string{"": fixture(t, "users_page1.json"), "CAIQAg": fixture(t, "users_page2.json")},
		status:     map[string]int{},
	}
}

func (f *fake) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.methods = append(f.methods, r.Method+" "+r.URL.Path)
		if code := f.status[r.URL.Path]; code != 0 {
			http.Error(w, `{"code":"internal","message":"boom"}`, code)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/auth/introspect":
			if r.Header.Get("Authorization") != "Bearer test-events-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(f.introspect))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/oauth/token":
			id, secret, ok := r.BasicAuth()
			if !ok || id != "test-client-id" || secret != "test-client-secret" {
				http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
				return
			}
			if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "client_credentials" {
				http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
				return
			}
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(f.token))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/accounts/"+account+"/users":
			f.userCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer op_o_c_test-access-token" {
				http.Error(w, `{"code":"unauthenticated"}`, http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("max_page_size") != "1000" {
				http.Error(w, "max_page_size", http.StatusBadRequest)
				return
			}
			body, ok := f.pages[r.URL.Query().Get("page_token")]
			if !ok {
				http.Error(w, "unknown page_token", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(t *testing.T, srv *httptest.Server) *opaccount.Client {
	return opaccount.NewClient(srv.Client(), srv.URL, srv.URL, creds(t))
}

var fixed = func() time.Time { return time.Date(2026, 11, 22, 14, 5, 0, 0, time.UTC) }

func TestInspect_DocumentedResponses(t *testing.T) {
	f := newFake(t)
	srv := f.serve(t)
	s, err := opaccount.Inspect(context.Background(), client(t, srv), fixed)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if s.OrgID != account {
		t.Errorf("OrgID = %q; want introspect account_uuid", s.OrgID)
	}
	if !s.AuditEventsEnabled {
		t.Error("AuditEventsEnabled = false; features include auditevents")
	}
	// Page 1: one ACTIVE, one SUSPENDED. Page 2: one ACTIVE, one with state
	// omitted, which is in neither count.
	if s.ActiveMembers != 2 || s.SuspendedMembers != 1 {
		t.Errorf("members = %d active, %d suspended; want 2, 1", s.ActiveMembers, s.SuspendedMembers)
	}
	if s.Result != opaccount.ResultInconclusive {
		t.Errorf("Result = %q; want inconclusive (the connector never passes)", s.Result)
	}
	if !s.ObservedAt.Equal(fixed()) {
		t.Errorf("ObservedAt = %v", s.ObservedAt)
	}
	for _, m := range f.methods {
		if !strings.HasPrefix(m, "GET ") && m != "POST /v1/oauth/token" {
			t.Errorf("unexpected request %q; the connector is read-only", m)
		}
	}
}

func TestInspect_FailsWithoutAuditEvents(t *testing.T) {
	f := newFake(t)
	f.introspect = fixture(t, "introspect_no_auditevents.json")
	s, err := opaccount.Inspect(context.Background(), client(t, f.serve(t)), fixed)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if s.AuditEventsEnabled || s.Result != opaccount.ResultFail {
		t.Errorf("state = %+v; want audit events off and fail", s)
	}
	if !strings.Contains(s.Reason, "auditevents") {
		t.Errorf("Reason = %q", s.Reason)
	}
}

func TestInspect_EmptyAccount(t *testing.T) {
	f := newFake(t)
	f.pages = map[string]string{"": `{}`} // documented: an empty page is {}
	s, err := opaccount.Inspect(context.Background(), client(t, f.serve(t)), fixed)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if s.ActiveMembers != 0 || s.SuspendedMembers != 0 {
		t.Errorf("members = %+v; want zero", s)
	}
}

func TestInspect_Errors(t *testing.T) {
	cases := map[string]func(f *fake){
		"introspect 401":      func(f *fake) { f.status["/api/v2/auth/introspect"] = http.StatusUnauthorized },
		"no account_uuid":     func(f *fake) { f.introspect = `{"uuid":"x","features":["auditevents"]}` },
		"token 401":           func(f *fake) { f.status["/v1/oauth/token"] = http.StatusUnauthorized },
		"token not bearer":    func(f *fake) { f.token = `{"access_token":"x","token_type":"mac"}` },
		"token empty":         func(f *fake) { f.token = `{"token_type":"Bearer"}` },
		"users page 2 bad":    func(f *fake) { f.pages["CAIQAg"] = "not json" },
		"users 403":           func(f *fake) { f.status["/v1/accounts/"+account+"/users"] = http.StatusForbidden },
		"repeated page token": func(f *fake) { f.pages["CAIQAg"] = `{"results":[],"next_page_token":"CAIQAg"}` },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			mut(f)
			s, err := opaccount.Inspect(context.Background(), client(t, f.serve(t)), fixed)
			if err == nil {
				t.Fatalf("expected error, got state %+v", s)
			}
		})
	}
}

func TestInspect_NilAPI(t *testing.T) {
	if _, err := opaccount.Inspect(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil API")
	}
}

func TestClient_RefusesRedirectToAnotherHost(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"account_uuid":"X","features":["auditevents"]}`))
	}))
	t.Cleanup(other.Close)
	events := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(events.Close)

	c := opaccount.NewClient(nil, events.URL, events.URL, creds(t))
	_, err := c.Introspect(context.Background())
	if !errors.Is(err, opauth.ErrUnsafeRedirect) {
		t.Fatalf("err = %v; want ErrUnsafeRedirect", err)
	}
	if hits.Load() != 0 {
		t.Errorf("the other host received %d requests", hits.Load())
	}
}

func TestNewClient_Defaults(t *testing.T) {
	c := opaccount.NewClient(nil, "", "", opauth.Credentials{})
	if c.EventsBaseURL != opaccount.DefaultEventsBaseURL || c.UsersBaseURL != opaccount.DefaultUsersBaseURL {
		t.Errorf("base URLs = %q, %q", c.EventsBaseURL, c.UsersBaseURL)
	}
	if c.HTTP.CheckRedirect == nil {
		t.Error("default client has no redirect guard")
	}
}

func TestAPIError(t *testing.T) {
	if got := (&opaccount.APIError{Status: 500}).Error(); got != "1password: HTTP 500" {
		t.Errorf("got %q", got)
	}
	if got := (&opaccount.APIError{Status: 403, Body: "nope"}).Error(); got != "1password: HTTP 403: nope" {
		t.Errorf("got %q", got)
	}
}
