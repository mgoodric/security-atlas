package main

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiratickets"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/lineartickets"
)

const tenantA = "11111111-1111-1111-1111-111111111111"

func newBufconnPlatform(t *testing.T) (*api.Server, *grpc.ClientConn, string) {
	t.Helper()
	srv := api.New(api.Config{RotationGrace: time.Hour})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(lis) }()
	t.Cleanup(func() {
		srv.GRPC.GracefulStop()
		_ = lis.Close()
	})
	_, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn, bearer
}

// TestRegister_ListsConnector verifies AC-1: register surfaces this
// connector via the ConnectorRegistry List RPC.
func TestRegister_ListsConnector(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	registry := connectorsv1.NewConnectorRegistryServiceClient(conn)

	ctxBearer := authedTestContext(bearer, 5*time.Second)
	ctx, cancel := ctxBearer()
	defer cancel()
	resp, err := registry.Register(ctx, &connectorsv1.RegisterRequest{
		Name:              ConnectorName,
		Version:           connectorVersion(),
		InstanceId:        "test-instance-1",
		SupportedKinds:    SupportedKinds,
		ProfilesSupported: []string{"pull"},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.GetHandle().GetName() != ConnectorName {
		t.Fatalf("name = %q; want %q", resp.GetHandle().GetName(), ConnectorName)
	}

	listCtx, cancel2 := ctxBearer()
	defer cancel2()
	list, err := registry.List(listCtx, &connectorsv1.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, h := range list.GetHandles() {
		if h.GetName() == ConnectorName {
			found = true
			if len(h.GetSupportedKinds()) != 1 {
				t.Errorf("supported_kinds count = %d; want 1 (jira.ticket_evidence.v1)", len(h.GetSupportedKinds()))
			}
			if h.GetSupportedKinds()[0] != "jira.ticket_evidence.v1" {
				t.Errorf("supported_kinds[0] = %q; want jira.ticket_evidence.v1", h.GetSupportedKinds()[0])
			}
			profiles := strings.Join(h.GetProfilesSupported(), ",")
			if !strings.Contains(profiles, "pull") {
				t.Errorf("profiles_supported = %q; want pull", profiles)
			}
		}
	}
	if !found {
		t.Fatal("jira-linear-connector not present in List response — AC-1 fail")
	}
}

// TestRunJira_PushesTicketEvidence verifies AC-2/AC-3/AC-4/AC-5: the Jira
// run path pulls from a fake Jira API, builds canonical records, and
// pushes them through the platform's Push RPC. Confirms Basic auth and
// scope cell.
func TestRunJira_PushesTicketEvidence(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	authsSeen := []string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search", func(w http.ResponseWriter, r *http.Request) {
		authsSeen = append(authsSeen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{
			"issues": [{
				"key": "CR-100",
				"fields": {
					"summary": "Deploy api v4.2.0",
					"status": {"name": "Done"},
					"resolution": {"name": "Fixed"},
					"assignee": {"displayName": "Carol Reviewer"},
					"project": {"key": "CR"}
				}
			}]
		}`))
	})
	jiraSrv := httptest.NewServer(mux)
	t.Cleanup(jiraSrv.Close)

	creds, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: "neutral.test.value"})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}

	api := jiratickets.NewClient(jiraSrv.Client(), jiraSrv.URL, creds)
	tickets, err := jiratickets.List(context.Background(), api, jiratickets.ListOpts{
		JQL: `project = CR`,
		Now: func() time.Time { return time.Date(2026, 5, 11, 12, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("len(tickets) = %d; want 1", len(tickets))
	}

	rec, err := buildJiraTicketRecord(tickets[0], "prod", "scf:CHG-02")
	if err != nil {
		t.Fatalf("buildJiraTicketRecord: %v", err)
	}
	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt hash empty")
	}

	// AC-5: scope cell (platform, project, environment).
	if got := scopeValue(rec.GetScope(), "platform"); got != "jira" {
		t.Errorf("scope.platform = %q; want jira", got)
	}
	if got := scopeValue(rec.GetScope(), "project"); got != "CR" {
		t.Errorf("scope.project = %q; want CR", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("scope.environment = %q; want prod", got)
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:jira:tickets@") {
		t.Errorf("actor_id = %q; want connector:jira:tickets@<version>", rec.GetSourceAttribution().GetActorId())
	}

	// AC-4: Basic auth header on every request, base64 of email:token.
	if len(authsSeen) == 0 {
		t.Fatal("Jira server saw 0 requests")
	}
	for _, a := range authsSeen {
		if !strings.HasPrefix(a, "Basic ") {
			t.Errorf("Authorization = %q; want Basic prefix", a)
		}
		// Confirm base64 decodes to "email:token".
		decoded, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "Basic "))
		if !strings.HasPrefix(string(decoded), "ops@example.com:") {
			t.Errorf("Basic header didn't carry expected email prefix; decoded prefix = %q", string(decoded)[:min(20, len(decoded))])
		}
	}

	// AC-2: record validates against the v1 payload shape — required
	// fields populated and no extras leaking in.
	payload := rec.GetPayload().AsMap()
	if payload["ticket_key"] != "CR-100" {
		t.Errorf("payload.ticket_key = %v", payload["ticket_key"])
	}
	if payload["status"] != "Done" {
		t.Errorf("payload.status = %v", payload["status"])
	}
	allowed := map[string]bool{
		"ticket_key": true, "project_key": true, "summary": true,
		"status": true, "resolution": true, "assignee": true, "url": true,
	}
	for k := range payload {
		if !allowed[k] {
			t.Errorf("payload key %q not allowed by jira.ticket_evidence.v1 schema", k)
		}
	}
}

// TestRunLinear_PushesTicketEvidence verifies the Linear path: GraphQL
// query, canonical record, scope cell, actor_id format.
func TestRunLinear_PushesTicketEvidence(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	authsSeen := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authsSeen = append(authsSeen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{
			"data": {
				"issues": {
					"nodes": [{
						"identifier": "IR-7",
						"title": "Postmortem: cache eviction storm",
						"url": "https://linear.app/example/issue/IR-7",
						"state": {"name": "Done"},
						"assignee": {"name": "Dave Operator"},
						"team": {"key": "IR"}
					}],
					"pageInfo": {"hasNextPage": false, "endCursor": null}
				}
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	creds, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: "neutral.test.value"})
	if err != nil {
		t.Fatalf("ResolveLinear: %v", err)
	}
	lapi := lineartickets.NewClient(srv.Client(), srv.URL, creds)
	tickets, err := lineartickets.List(context.Background(), lapi, lineartickets.ListOpts{
		Filter: lineartickets.Filter{TeamKey: "IR"},
		Now:    func() time.Time { return time.Date(2026, 5, 11, 12, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("len(tickets) = %d; want 1", len(tickets))
	}

	rec, err := buildLinearTicketRecord(tickets[0], "prod", "scf:CHG-02")
	if err != nil {
		t.Fatalf("buildLinearTicketRecord: %v", err)
	}
	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt hash empty")
	}

	// AC-5: scope cell (platform, project, environment).
	if got := scopeValue(rec.GetScope(), "platform"); got != "linear" {
		t.Errorf("scope.platform = %q; want linear", got)
	}
	if got := scopeValue(rec.GetScope(), "project"); got != "IR" {
		t.Errorf("scope.project = %q; want IR", got)
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:linear:tickets@") {
		t.Errorf("actor_id = %q; want connector:linear:tickets@<version>", rec.GetSourceAttribution().GetActorId())
	}

	// AC-4: Linear Authorization carries the raw key, no Bearer prefix.
	if len(authsSeen) == 0 {
		t.Fatal("Linear server saw 0 requests")
	}
	for _, a := range authsSeen {
		if strings.HasPrefix(a, "Bearer ") {
			t.Errorf("Linear got Bearer prefix; Linear API rejects it: %q", a)
		}
		if a != "neutral.test.value" {
			t.Errorf("Linear Authorization = %q; want raw API key", a)
		}
	}
}

// TestRun_TicketDedupesByIdempotencyKey verifies AC-3: two pushes of
// the same ticket in the same hour produce the same record id.
func TestRun_TicketDedupesByIdempotencyKey(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	tk := jiratickets.Ticket{
		TicketKey:  "CR-100",
		ProjectKey: "CR",
		Summary:    "Deploy",
		Status:     "Done",
		ObservedAt: time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
	}
	r1, err := buildJiraTicketRecord(tk, "prod", "scf:CHG-02")
	if err != nil {
		t.Fatalf("buildJiraTicketRecord: %v", err)
	}
	r2, err := buildJiraTicketRecord(tk, "prod", "scf:CHG-02")
	if err != nil {
		t.Fatalf("buildJiraTicketRecord: %v", err)
	}
	rec1, err := client.Push(context.Background(), r1)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	rec2, err := client.Push(context.Background(), r2)
	if err != nil {
		t.Fatalf("second push: %v", err)
	}
	if rec1.GetRecordId() != rec2.GetRecordId() {
		t.Fatalf("dedup failed: %q vs %q", rec1.GetRecordId(), rec2.GetRecordId())
	}
}

// TestRun_TicketIdempotencyKeyDistinctAcrossTickets confirms different
// tickets emit different keys.
func TestRun_TicketIdempotencyKeyDistinctAcrossTickets(t *testing.T) {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	r1, _ := buildJiraTicketRecord(jiratickets.Ticket{TicketKey: "A-1", Status: "Done", ObservedAt: now}, "prod", "scf:CHG-02")
	r2, _ := buildJiraTicketRecord(jiratickets.Ticket{TicketKey: "A-2", Status: "Done", ObservedAt: now}, "prod", "scf:CHG-02")
	if r1.GetIdempotencyKey() == r2.GetIdempotencyKey() {
		t.Fatal("distinct tickets produced same idempotency_key")
	}
}

// TestNoSecretInString confirms the credential types never leak secrets
// through default formatting. Belt-and-braces against accidental log
// inclusion of a Credential in the cmd layer.
func TestNoSecretInString(t *testing.T) {
	const tok = "very_sensitive_token_AAA"
	jc, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: "ops@example.com", Token: tok})
	if err != nil {
		t.Fatalf("ResolveJira: %v", err)
	}
	if strings.Contains(jc.String(), tok) {
		t.Errorf("Jira Credential leaked token through String: %q", jc.String())
	}

	const lk = "very_sensitive_key_BBB"
	lc, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: lk})
	if err != nil {
		t.Fatalf("ResolveLinear: %v", err)
	}
	if strings.Contains(lc.String(), lk) {
		t.Errorf("Linear Credential leaked key through String: %q", lc.String())
	}
}

// TestRecordValidatesAgainstSchema spot-checks a built record decodes
// to the shape jira.ticket_evidence/1.0.0.json declares. The full
// schema validator runs server-side; this test pins payload shape so a
// connector refactor can't drop a required field.
func TestRecordValidatesAgainstSchema(t *testing.T) {
	tk := jiratickets.Ticket{
		TicketKey:  "CR-1",
		ProjectKey: "CR",
		Summary:    "x",
		Status:     "Done",
		Resolution: "Fixed",
		Assignee:   "Eve",
		URL:        "https://acme.atlassian.net/browse/CR-1",
		ObservedAt: time.Now().UTC(),
	}
	rec, err := buildJiraTicketRecord(tk, "prod", "scf:CHG-02")
	if err != nil {
		t.Fatalf("buildJiraTicketRecord: %v", err)
	}
	payload := rec.GetPayload().AsMap()
	for _, req := range []string{"ticket_key", "status"} {
		if _, ok := payload[req]; !ok {
			t.Errorf("required field %q missing from payload", req)
		}
	}
}

// TestHelpText_NoSecretLeakage scans every visible help string for the
// literal placeholder tokens; if a future flag accidentally hardcodes a
// secret in its help text the test catches it.
func TestHelpText_NoSecretLeakage(t *testing.T) {
	root := newRootCmd()
	var buf strings.Builder
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, sub := range []string{"register", "run", "scopes"} {
		sb := &strings.Builder{}
		root := newRootCmd()
		root.SetOut(sb)
		root.SetErr(sb)
		root.SetArgs([]string{sub, "--help"})
		_ = root.Execute()
		buf.WriteString(sb.String())
	}
	out := buf.String()
	// These literal values are placeholders; if any flag description
	// hardcoded a credential it would show up here.
	banned := []string{"github_pat_", "ghp_", "atlassian_token_=", "linear_key_=", "secret="}
	for _, b := range banned {
		if strings.Contains(out, b) {
			t.Errorf("help text leaked literal %q", b)
		}
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

func scopeValue(dims []*evidencev1.ScopeDimension, key string) string {
	for _, d := range dims {
		if d.GetKey() == key && len(d.GetValues()) > 0 {
			return d.GetValues()[0]
		}
	}
	return ""
}
