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

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opaccount"
	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
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
				t.Errorf("supported_kinds count = %d; want 1", len(h.GetSupportedKinds()))
			}
			profiles := strings.Join(h.GetProfilesSupported(), ",")
			if !strings.Contains(profiles, "pull") {
				t.Errorf("profiles_supported = %q; want pull", profiles)
			}
			if strings.Contains(profiles, "push") {
				t.Errorf("profiles_supported = %q; slice 046 is pull-only (canvas §4.2)", profiles)
			}
		}
	}
	if !found {
		t.Fatal("1password-connector not present in List response — AC-1 fail")
	}
}

// TestRun_PushesOrgPolicy verifies AC-2/AC-3/AC-4: the run path pulls
// from a fake 1Password API, builds a canonical record, and pushes it
// through the platform's Push RPC.
func TestRun_PushesOrgPolicy(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"id": "acme-corp",
			"name": "Acme Corp",
			"two_factor_required": true,
			"minimum_password_length": 14,
			"domain_restrictions_enabled": true,
			"active_member_count": 47
		}`))
	})
	opSrv := httptest.NewServer(mux)
	t.Cleanup(opSrv.Close)

	creds, err := opauth.Resolve(opauth.ResolveOpts{Token: "test-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	api := opaccount.NewClient(opSrv.Client(), opSrv.URL, creds)
	state, err := opaccount.Inspect(context.Background(), api, nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if state.Result != opaccount.ResultPass {
		t.Fatalf("unexpected inspect result: %+v", state)
	}

	rec, err := buildOrgPolicyRecord(state, "prod", "scf:IAC-10")
	if err != nil {
		t.Fatalf("buildOrgPolicyRecord: %v", err)
	}
	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push org_policy: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt hash empty")
	}

	// AC-4: provenance + scope tags.
	if got := scopeValue(rec.GetScope(), "org"); got != "acme-corp" {
		t.Errorf("scope.org = %q; want acme-corp", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("scope.environment = %q; want prod", got)
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:1password:org_policy@") {
		t.Errorf("actor_id = %q; want connector:1password:org_policy@<version>", rec.GetSourceAttribution().GetActorId())
	}
	if rec.GetSourceAttribution().GetActorType() != "connector" {
		t.Errorf("actor_type = %q; want connector", rec.GetSourceAttribution().GetActorType())
	}
}

// TestRun_OrgPolicyDedupes verifies the idempotency_key shape: two
// builds in the same hour for the same org collapse to one ledger row.
// Anti-criterion: idempotency_key must be derived from kind|org|hour.
func TestRun_OrgPolicyDedupes(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	state := &opaccount.PolicyState{
		OrgID:                     "acme-corp",
		TwoFactorRequired:         true,
		MinimumPasswordLength:     14,
		DomainRestrictionsEnabled: true,
		ActiveMembers:             47,
		Result:                    opaccount.ResultPass,
		ObservedAt:                time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
	}
	r1, err := buildOrgPolicyRecord(state, "prod", "scf:IAC-10")
	if err != nil {
		t.Fatalf("build r1: %v", err)
	}
	r2, err := buildOrgPolicyRecord(state, "prod", "scf:IAC-10")
	if err != nil {
		t.Fatalf("build r2: %v", err)
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
	// Anti-criterion: idempotency_key on the wire must be non-empty and
	// 64 hex chars (sha256). Empty key would let the ledger duplicate.
	if got := r1.GetIdempotencyKey(); len(got) != 64 {
		t.Fatalf("idempotency_key length = %d; want 64 hex chars", len(got))
	}
}

// TestRun_OrgPolicyRotatesAcrossHour verifies the key correctly rotates
// when observed_at crosses an hour boundary — the slice's freshness
// guarantee.
func TestRun_OrgPolicyRotatesAcrossHour(t *testing.T) {
	hourA := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	hourB := time.Date(2026, 5, 11, 13, 0, 0, 0, time.UTC)
	stateA := &opaccount.PolicyState{OrgID: "acme-corp", TwoFactorRequired: true, MinimumPasswordLength: 14, Result: opaccount.ResultPass, ObservedAt: hourA}
	stateB := &opaccount.PolicyState{OrgID: "acme-corp", TwoFactorRequired: true, MinimumPasswordLength: 14, Result: opaccount.ResultPass, ObservedAt: hourB}
	rA, _ := buildOrgPolicyRecord(stateA, "prod", "scf:IAC-10")
	rB, _ := buildOrgPolicyRecord(stateB, "prod", "scf:IAC-10")
	if rA.GetIdempotencyKey() == rB.GetIdempotencyKey() {
		t.Fatal("idempotency_key identical across hour boundary; freshness lost")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

// scopeValue returns the first scope value for key. Empty when absent.
func scopeValue(dims []*evidencev1.ScopeDimension, key string) string {
	for _, d := range dims {
		if d.GetKey() == key && len(d.GetValues()) > 0 {
			return d.GetValues()[0]
		}
	}
	return ""
}
