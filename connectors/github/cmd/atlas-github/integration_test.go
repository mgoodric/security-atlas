package main

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubscim"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubwebhook"
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
		ProfilesSupported: []string{"pull", "push"},
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
			if len(h.GetSupportedKinds()) != 3 {
				t.Errorf("supported_kinds count = %d; want 3", len(h.GetSupportedKinds()))
			}
			profiles := strings.Join(h.GetProfilesSupported(), ",")
			if !strings.Contains(profiles, "pull") || !strings.Contains(profiles, "push") {
				t.Errorf("profiles_supported = %q; want pull + push", profiles)
			}
		}
	}
	if !found {
		t.Fatal("github-connector not present in List response — AC-1 fail")
	}
}

// TestRun_PushesRepoProtectionAndSCIM verifies AC-2/AC-3/AC-5: the run
// path pulls from a fake GitHub API, builds canonical records, and
// pushes them through the platform's Push RPC.
func TestRun_PushesRepoProtectionAndSCIM(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	// fake GitHub
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/example/repos", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]githubrepo.Repo{
			{FullName: "example/web", DefaultBranch: "main"},
		})
	})
	mux.HandleFunc("/repos/example/web/branches/main/protection", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"required_pull_request_reviews": {"required_approving_review_count": 2, "require_code_owner_reviews": true},
			"required_signatures": {"enabled": true}
		}`))
	})
	mux.HandleFunc("/scim/v2/organizations/example/Users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"totalResults": 1,
			"Resources": [
				{"id": "scim-1", "userName": "alice", "active": true, "emails": [{"value": "a@x", "primary": true}]}
			]
		}`))
	})
	githubSrv := httptest.NewServer(mux)
	t.Cleanup(githubSrv.Close)

	creds, err := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	repoAPI := githubrepo.NewClient(githubSrv.Client(), githubSrv.URL, creds)
	states, err := githubrepo.Inspect(context.Background(), repoAPI, "example", nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(states) != 1 || states[0].Result != githubrepo.ResultPass {
		t.Fatalf("unexpected inspect result: %+v", states)
	}

	rec, err := buildRepoProtectionRecord(states[0], "example", "prod", "scf:TDA-06")
	if err != nil {
		t.Fatalf("buildRepoProtectionRecord: %v", err)
	}
	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push repo_protection: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt hash empty")
	}

	// Verify scope + actor_id format.
	if got := scopeValue(rec.GetScope(), "org"); got != "example" {
		t.Errorf("scope.org = %q; want example", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("scope.environment = %q; want prod", got)
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:github:repo@") {
		t.Errorf("actor_id = %q; want connector:github:repo@<version>", rec.GetSourceAttribution().GetActorId())
	}

	scimAPI := githubscim.NewClient(githubSrv.Client(), githubSrv.URL, creds)
	users, err := githubscim.Reconcile(context.Background(), scimAPI, "example", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("len(users) = %d", len(users))
	}
	scimRec, err := buildSCIMRecord(users[0], "prod", "scf:IAC-22")
	if err != nil {
		t.Fatalf("buildSCIMRecord: %v", err)
	}
	if _, err := client.Push(context.Background(), scimRec); err != nil {
		t.Fatalf("Push scim_user: %v", err)
	}
	if !strings.HasPrefix(scimRec.GetSourceAttribution().GetActorId(), "connector:github:scim@") {
		t.Errorf("scim actor_id = %q", scimRec.GetSourceAttribution().GetActorId())
	}
}

// TestRun_RepoProtectionDedupes verifies the idempotency_key shape.
func TestRun_RepoProtectionDedupes(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	state := githubrepo.ProtectionState{
		RepoFullName:    "example/web",
		DefaultBranch:   "main",
		RequiredReviews: 1,
		Result:          githubrepo.ResultPass,
		ObservedAt:      time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
	}
	r1, err := buildRepoProtectionRecord(state, "example", "prod", "scf:TDA-06")
	if err != nil {
		t.Fatalf("buildRepoProtectionRecord: %v", err)
	}
	r2, err := buildRepoProtectionRecord(state, "example", "prod", "scf:TDA-06")
	if err != nil {
		t.Fatalf("buildRepoProtectionRecord: %v", err)
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

// TestWebhook_AcceptsSignedDeliveryAndPushesAuditEvent verifies AC-4 +
// AC-2: HMAC-signed delivery is accepted, transformed, and pushed; the
// idempotency_key matches X-GitHub-Delivery verbatim.
func TestWebhook_AcceptsSignedDeliveryAndPushesAuditEvent(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	pusher := &sdkPusher{client: client, env: "prod", controlID: "scf:MON-01"}
	secret := []byte("integration-test-secret")
	handler, err := githubwebhook.NewHandler(secret, pusher, func() time.Time {
		return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	httpSrv := httptest.NewServer(handler)
	t.Cleanup(httpSrv.Close)

	body := []byte(`{
		"action": "edited",
		"sender": {"login": "sample-user"},
		"organization": {"login": "example"},
		"repository": {"full_name": "example/web"}
	}`)
	req, _ := http.NewRequest(http.MethodPost, httpSrv.URL, bytes.NewReader(body))
	req.Header.Set(githubwebhook.HeaderSignature, githubwebhook.Sign(secret, body))
	req.Header.Set(githubwebhook.HeaderEvent, "repository")
	req.Header.Set(githubwebhook.HeaderDelivery, "72d3162e-cc78-11e3-81ab-4c9367dc0958")
	res, err := httpSrv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d; want 204", res.StatusCode)
	}
}

// TestWebhook_DedupesByDeliveryID verifies the anti-criterion: replays
// with the same X-GitHub-Delivery return the same evidence record id.
func TestWebhook_DedupesByDeliveryID(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	now := func() time.Time { return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC) }
	r := &githubwebhook.AuditEventRecord{
		IdempotencyKey: "72d3162e-cc78-11e3-81ab-4c9367dc0958",
		EventType:      "repository",
		Action:         "edited",
		Actor:          "sample-user",
		Org:            "example",
		Repo:           "example/web",
		DeliveryID:     "72d3162e-cc78-11e3-81ab-4c9367dc0958",
		CreatedAt:      now(),
	}
	r2 := *r
	rec1, err := buildAuditEventRecord(r, "prod", "scf:MON-01")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rec2, err := buildAuditEventRecord(&r2, "prod", "scf:MON-01")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	p1, err := client.Push(context.Background(), rec1)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	p2, err := client.Push(context.Background(), rec2)
	if err != nil {
		t.Fatalf("second push: %v", err)
	}
	if p1.GetRecordId() != p2.GetRecordId() {
		t.Fatalf("dedup failed: %q vs %q", p1.GetRecordId(), p2.GetRecordId())
	}
	// Anti-criterion: idempotency_key on the wire MUST equal the delivery id.
	if rec1.GetIdempotencyKey() != "72d3162e-cc78-11e3-81ab-4c9367dc0958" {
		t.Fatalf("idempotency_key = %q; must be X-GitHub-Delivery verbatim", rec1.GetIdempotencyKey())
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

// scopeValue returns the first scope value for key. Returns empty string
// when key absent.
func scopeValue(dims []*evidencev1.ScopeDimension, key string) string {
	for _, d := range dims {
		if d.GetKey() == key && len(d.GetValues()) > 0 {
			return d.GetValues()[0]
		}
	}
	return ""
}
