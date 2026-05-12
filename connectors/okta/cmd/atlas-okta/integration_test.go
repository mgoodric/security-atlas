package main

import (
	"context"
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

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaapps"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktausers"
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

// fakeOktaServer assembles a single httptest server replaying realistic
// payloads for /api/v1/policies, /api/v1/apps, /api/v1/apps/{id}/groups,
// /api/v1/users, and /api/v1/users/{id}/factors.
func fakeOktaServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policies", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{
				"id": "policy1",
				"name": "Default MFA Policy",
				"status": "ACTIVE",
				"type": "MFA_ENROLL",
				"settings": {"factors": {"okta_verify": {"enroll": {"self": "REQUIRED"}}}},
				"conditions": {"people": {"groups": {"include": ["everyone"]}}}
			}
		]`))
	})
	mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "app1", "label": "Slack", "status": "ACTIVE", "signOnMode": "SAML_2_0"}]`))
	})
	mux.HandleFunc("/api/v1/apps/app1/groups", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "g1"}, {"id": "g2"}]`))
	})
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "u1", "status": "ACTIVE", "profile": {"login": "alice@example.com"}}]`))
	})
	mux.HandleFunc("/api/v1/users/u1/factors", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "f1", "factorType": "push", "status": "ACTIVE"}]`))
	})
	return httptest.NewServer(mux)
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
		InstanceId:        "test-instance-okta",
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
			if len(h.GetSupportedKinds()) != 3 {
				t.Errorf("supported_kinds count = %d; want 3", len(h.GetSupportedKinds()))
			}
			if strings.Join(h.GetProfilesSupported(), ",") != "pull" {
				t.Errorf("profiles_supported = %v; want [pull]", h.GetProfilesSupported())
			}
		}
	}
	if !found {
		t.Fatal("okta-connector not present in List response — AC-1 fail")
	}
}

// TestRun_PushesAllThreeKinds verifies AC-2/AC-3/AC-4: the run path pulls
// each kind from a fake Okta API, builds canonical records, and pushes
// them through the platform's Push RPC.
func TestRun_PushesAllThreeKinds(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	oktaSrv := fakeOktaServer(t)
	t.Cleanup(oktaSrv.Close)

	creds, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// mfa_policy
	polClient := oktapolicy.NewClient(oktaSrv.Client(), oktaSrv.URL, creds)
	states, err := oktapolicy.Pull(context.Background(), polClient, nil)
	if err != nil {
		t.Fatalf("policy Pull: %v", err)
	}
	if len(states) != 1 || states[0].Result != oktapolicy.ResultPass {
		t.Fatalf("unexpected states: %+v", states)
	}
	mfaRec, err := buildMFAPolicyRecord(states[0], "example", "prod", "scf:IAC-06")
	if err != nil {
		t.Fatalf("buildMFAPolicyRecord: %v", err)
	}
	mfaReceipt, err := client.Push(context.Background(), mfaRec)
	if err != nil {
		t.Fatalf("Push mfa_policy: %v", err)
	}
	if mfaReceipt.GetHash() == "" {
		t.Fatal("mfa receipt hash empty")
	}
	if !strings.HasPrefix(mfaRec.GetSourceAttribution().GetActorId(), "connector:okta:policy@") {
		t.Errorf("mfa actor_id = %q", mfaRec.GetSourceAttribution().GetActorId())
	}
	if got := scopeValue(mfaRec.GetScope(), "org"); got != "example" {
		t.Errorf("mfa scope.org = %q", got)
	}
	if got := scopeValue(mfaRec.GetScope(), "environment"); got != "prod" {
		t.Errorf("mfa scope.environment = %q", got)
	}

	// app_assignment
	appsClient := oktaapps.NewClient(oktaSrv.Client(), oktaSrv.URL, creds)
	assignments, err := oktaapps.Pull(context.Background(), appsClient, nil)
	if err != nil {
		t.Fatalf("apps Pull: %v", err)
	}
	if len(assignments) != 1 || assignments[0].AssignedGroupCount != 2 {
		t.Fatalf("unexpected assignments: %+v", assignments)
	}
	appRec, err := buildAppAssignmentRecord(assignments[0], "example", "prod", "scf:IAC-21")
	if err != nil {
		t.Fatalf("buildAppAssignmentRecord: %v", err)
	}
	if _, err := client.Push(context.Background(), appRec); err != nil {
		t.Fatalf("Push app_assignment: %v", err)
	}
	if !strings.HasPrefix(appRec.GetSourceAttribution().GetActorId(), "connector:okta:apps@") {
		t.Errorf("apps actor_id = %q", appRec.GetSourceAttribution().GetActorId())
	}

	// user_lifecycle
	usersClient := oktausers.NewClient(oktaSrv.Client(), oktaSrv.URL, creds)
	users, err := oktausers.Pull(context.Background(), usersClient, nil)
	if err != nil {
		t.Fatalf("users Pull: %v", err)
	}
	if len(users) != 1 || users[0].Result != oktausers.ResultPass {
		t.Fatalf("unexpected users: %+v", users)
	}
	userRec, err := buildUserLifecycleRecord(users[0], "example", "prod", "scf:IAC-22")
	if err != nil {
		t.Fatalf("buildUserLifecycleRecord: %v", err)
	}
	if _, err := client.Push(context.Background(), userRec); err != nil {
		t.Fatalf("Push user_lifecycle: %v", err)
	}
	if !strings.HasPrefix(userRec.GetSourceAttribution().GetActorId(), "connector:okta:users@") {
		t.Errorf("users actor_id = %q", userRec.GetSourceAttribution().GetActorId())
	}
}

// TestRun_AllEmittersDedupe verifies the anti-criterion: replays with the
// same observed-hour key dedupe to the same record_id (per kind).
func TestRun_AllEmittersDedupe(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	// mfa_policy
	pol := oktapolicy.PolicyState{
		PolicyID: "p1", PolicyName: "P", MFARequired: true,
		FactorsAllowed: []string{"push"}, Result: oktapolicy.ResultPass,
		ObservedAt: fixed,
	}
	r1, _ := buildMFAPolicyRecord(pol, "example", "prod", "scf:IAC-06")
	r2, _ := buildMFAPolicyRecord(pol, "example", "prod", "scf:IAC-06")
	rec1, err := client.Push(context.Background(), r1)
	if err != nil {
		t.Fatalf("first mfa push: %v", err)
	}
	rec2, err := client.Push(context.Background(), r2)
	if err != nil {
		t.Fatalf("second mfa push: %v", err)
	}
	if rec1.GetRecordId() != rec2.GetRecordId() {
		t.Fatalf("mfa dedup failed: %q vs %q", rec1.GetRecordId(), rec2.GetRecordId())
	}

	// app_assignment
	a := oktaapps.Assignment{
		AppID: "app1", AppName: "Slack", Status: "ACTIVE",
		AssignedGroupIDs: []string{"g1"}, AssignedGroupCount: 1,
		ObservedAt: fixed,
	}
	r3, _ := buildAppAssignmentRecord(a, "example", "prod", "scf:IAC-21")
	r4, _ := buildAppAssignmentRecord(a, "example", "prod", "scf:IAC-21")
	rec3, err := client.Push(context.Background(), r3)
	if err != nil {
		t.Fatalf("first app push: %v", err)
	}
	rec4, err := client.Push(context.Background(), r4)
	if err != nil {
		t.Fatalf("second app push: %v", err)
	}
	if rec3.GetRecordId() != rec4.GetRecordId() {
		t.Fatalf("app dedup failed: %q vs %q", rec3.GetRecordId(), rec4.GetRecordId())
	}

	// user_lifecycle
	u := oktausers.Lifecycle{
		UserID: "u1", Login: "alice@example.com", Status: "ACTIVE",
		MFAEnrolled: true, Result: oktausers.ResultPass,
		ObservedAt: fixed,
	}
	r5, _ := buildUserLifecycleRecord(u, "example", "prod", "scf:IAC-22")
	r6, _ := buildUserLifecycleRecord(u, "example", "prod", "scf:IAC-22")
	rec5, err := client.Push(context.Background(), r5)
	if err != nil {
		t.Fatalf("first user push: %v", err)
	}
	rec6, err := client.Push(context.Background(), r6)
	if err != nil {
		t.Fatalf("second user push: %v", err)
	}
	if rec5.GetRecordId() != rec6.GetRecordId() {
		t.Fatalf("user dedup failed: %q vs %q", rec5.GetRecordId(), rec6.GetRecordId())
	}

	// Anti-criterion: idempotency_key must be non-empty on all three.
	if r1.GetIdempotencyKey() == "" || r3.GetIdempotencyKey() == "" || r5.GetIdempotencyKey() == "" {
		t.Fatal("empty idempotency_key — anti-criterion P0 violation")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

// scopeValue returns the first scope value for key. Empty when key absent.
func scopeValue(dims []*evidencev1.ScopeDimension, key string) string {
	for _, d := range dims {
		if d.GetKey() == key && len(d.GetValues()) > 0 {
			return d.GetValues()[0]
		}
	}
	return ""
}
