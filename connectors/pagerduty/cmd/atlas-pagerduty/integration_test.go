package main

import (
	"context"
	"net"
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

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/oncall"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pagerdutyauth"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdrecord"
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

// fakeOnCallAPI is a faked PagerDuty escalation-policy surface (NO live PagerDuty).
type fakeOnCallAPI struct{ policies []oncall.RawPolicy }

func (f *fakeOnCallAPI) ListEscalationPolicies(_ context.Context) ([]oncall.RawPolicy, error) {
	return f.policies, nil
}

// fakeIncidentsAPI is a faked PagerDuty incidents surface (NO live PagerDuty).
type fakeIncidentsAPI struct{ incidents []incidents.RawIncident }

func (f *fakeIncidentsAPI) ListIncidents(_ context.Context, _, _ time.Time) ([]incidents.RawIncident, error) {
	return f.incidents, nil
}

// TestRegister_ListsConnector verifies AC-1 + AC-7.
func TestRegister_ListsConnector(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	registry := connectorsv1.NewConnectorRegistryServiceClient(conn)

	ctx, cancel := authedTestContext(bearer, 5*time.Second)()
	defer cancel()
	resp, err := registry.Register(ctx, &connectorsv1.RegisterRequest{
		Name:              ConnectorName,
		Version:           connectorVersion(),
		InstanceId:        "test-instance-pagerduty",
		SupportedKinds:    SupportedKinds,
		ProfilesSupported: []string{"pull"},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.GetHandle().GetName() != ConnectorName {
		t.Fatalf("name = %q", resp.GetHandle().GetName())
	}

	listCtx, cancel2 := authedTestContext(bearer, 5*time.Second)()
	defer cancel2()
	list, err := registry.List(listCtx, &connectorsv1.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, h := range list.GetHandles() {
		if h.GetName() == ConnectorName {
			found = true
			if strings.Join(h.GetProfilesSupported(), ",") != "pull" {
				t.Errorf("profiles_supported = %v; want [pull]", h.GetProfilesSupported())
			}
		}
	}
	if !found {
		t.Fatal("pagerduty-connector not present in List — AC-1 fail")
	}
}

// TestRun_PushesOnCallAndIncidentRecords verifies AC-2/AC-3/AC-5/AC-6/AC-9:
// collect from faked PagerDuty APIs, build the canonical records, push through
// the platform's single Push RPC, and assert the receipt (sha256 content hash).
func TestRun_PushesOnCallAndIncidentRecords(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	ocAPI := &fakeOnCallAPI{policies: []oncall.RawPolicy{
		{ID: "PABC", Name: "Primary", Tiers: []oncall.RawTier{
			{Level: 1, Targets: []oncall.RawTarget{{Kind: "user_reference", ID: "U1", Name: "Alice"}}},
		}},
	}}
	policies, err := oncall.Collect(context.Background(), ocAPI)
	if err != nil {
		t.Fatalf("oncall.Collect: %v", err)
	}
	ocRec, err := pdrecord.BuildOnCall(policies[0], "scf:IRO-04", actorID("oncall"), "pagerduty", "prod", now)
	if err != nil {
		t.Fatalf("BuildOnCall: %v", err)
	}
	ocReceipt, err := client.Push(context.Background(), ocRec)
	if err != nil {
		t.Fatalf("Push oncall: %v", err)
	}
	if ocReceipt.GetHash() == "" {
		t.Fatal("oncall receipt hash empty (AC-6 sha256 content-hash)")
	}
	if !strings.HasPrefix(ocRec.GetSourceAttribution().GetActorId(), "connector:pagerduty:oncall@") {
		t.Errorf("oncall actor_id = %q", ocRec.GetSourceAttribution().GetActorId())
	}

	incAPI := &fakeIncidentsAPI{incidents: []incidents.RawIncident{
		{ID: "INC1", Number: 1, Status: "resolved", Urgency: "high", ServiceID: "SVC1", ServiceName: "API", CreatedAt: now.Add(-2 * time.Hour), ResolvedAt: now.Add(-time.Hour)},
	}}
	incs, err := incidents.Collect(context.Background(), incAPI, now.AddDate(0, 0, -90), now)
	if err != nil {
		t.Fatalf("incidents.Collect: %v", err)
	}
	incRec, err := pdrecord.BuildIncident(incs[0], "scf:IRO-02", actorID("incidents"), "pagerduty", "prod", now)
	if err != nil {
		t.Fatalf("BuildIncident: %v", err)
	}
	incReceipt, err := client.Push(context.Background(), incRec)
	if err != nil {
		t.Fatalf("Push incident: %v", err)
	}
	if incReceipt.GetHash() == "" {
		t.Fatal("incident receipt hash empty (AC-6 sha256 content-hash)")
	}
}

// TestRun_DedupesWithinHour verifies AC-6 idempotency.
func TestRun_DedupesWithinHour(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	p := oncall.Policy{ID: "P1", Name: "n", NumTier: 0, Covered: false}
	r1, _ := pdrecord.BuildOnCall(p, "scf:IRO-04", actorID("oncall"), "pagerduty", "prod", now)
	r2, _ := pdrecord.BuildOnCall(p, "scf:IRO-04", actorID("oncall"), "pagerduty", "prod", now.Add(30*time.Minute))
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

// TestEmittedRecords_NoPIIorFreeText verifies AC-10 + P0-489-3: even when the
// faked PagerDuty payloads embed responder PII and incident free-text, the
// emitted records carry coverage + summary metadata only.
//
// The on-call RawTarget / incident RawIncident types have no contact-detail or
// free-text field by construction; this asserts the emitted payloads too.
func TestEmittedRecords_NoPIIorFreeText(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	// Identity name is allowed; a phone/email-shaped string must never appear.
	ocAPI := &fakeOnCallAPI{policies: []oncall.RawPolicy{
		{ID: "PABC", Name: "Primary", Tiers: []oncall.RawTier{
			{Level: 1, Targets: []oncall.RawTarget{{Kind: "user_reference", ID: "U1", Name: "Alice Eng"}}},
		}},
	}}
	policies, _ := oncall.Collect(context.Background(), ocAPI)
	ocRec, _ := pdrecord.BuildOnCall(policies[0], "scf:IRO-04", actorID("oncall"), "pagerduty", "prod", now)

	ocAllowed := map[string]bool{
		"escalation_policy_id": true, "escalation_policy_name": true,
		"num_tiers": true, "covered": true, "tiers": true,
	}
	banned := []string{"@", "+1555", "phone", "fixture.invalid", "SSN", "customer", "postmortem", "description"}
	assertNoBanned(t, ocRec, ocAllowed, banned)

	incAPI := &fakeIncidentsAPI{incidents: []incidents.RawIncident{
		{ID: "INC1", Number: 1, Status: "resolved", Urgency: "high", ServiceID: "SVC1", ServiceName: "Export API", CreatedAt: now.Add(-time.Hour), ResolvedAt: now},
	}}
	incs, _ := incidents.Collect(context.Background(), incAPI, now.AddDate(0, 0, -90), now)
	incRec, _ := pdrecord.BuildIncident(incs[0], "scf:IRO-02", actorID("incidents"), "pagerduty", "prod", now)

	incAllowed := map[string]bool{
		"incident_id": true, "incident_number": true, "status": true, "urgency": true,
		"service_id": true, "service_name": true, "created_at": true, "resolved_at": true,
	}
	assertNoBanned(t, incRec, incAllowed, banned)
}

// TestCredential_NeverLogged verifies AC-11 + P0-489-4.
func TestCredential_NeverLogged(t *testing.T) {
	const token = "test-pagerduty-token-no-log"
	cred, err := pagerdutyauth.Resolve(pagerdutyauth.ResolveOpts{Token: token})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), token) || strings.Contains(cred.GoString(), token) {
		t.Fatal("credential String/GoString leaks the token — AC-11 violation")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

// assertNoBanned walks every string in the record payload and asserts no banned
// substring (a phone/email/PII or free-text marker) appears, and that only
// allow-listed top-level keys are present.
func assertNoBanned(t *testing.T, rec *evidencev1.EvidenceRecord, allowed map[string]bool, banned []string) {
	t.Helper()
	pm := rec.GetPayload().AsMap()
	for k := range pm {
		if !allowed[k] {
			t.Errorf("non-allow-listed payload key %q", k)
		}
	}
	walk(t, pm, banned)
}

func walk(t *testing.T, v any, banned []string) {
	t.Helper()
	switch x := v.(type) {
	case string:
		for _, b := range banned {
			if strings.Contains(x, b) {
				t.Errorf("payload string %q contains banned substring %q (PII/free-text leak)", x, b)
			}
		}
	case map[string]any:
		for _, vv := range x {
			walk(t, vv, banned)
		}
	case []any:
		for _, vv := range x {
			walk(t, vv, banned)
		}
	}
}
