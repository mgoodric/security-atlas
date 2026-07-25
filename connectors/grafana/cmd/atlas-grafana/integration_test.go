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

	"github.com/mgoodric/security-atlas/connectors/grafana/internal/alertrules"
	"github.com/mgoodric/security-atlas/connectors/grafana/internal/grafanaauth"
	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
	"github.com/mgoodric/security-atlas/connectors/monitoring/monrecord"
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

// fakeAlertRulesAPI is a faked Grafana provisioning surface (NO live Grafana).
type fakeAlertRulesAPI struct {
	rules    []alertrules.RawRule
	contacts []alertrules.ContactPoint
}

func (f *fakeAlertRulesAPI) ListAlertRules(_ context.Context) ([]alertrules.RawRule, error) {
	return f.rules, nil
}

func (f *fakeAlertRulesAPI) ListContactPoints(_ context.Context) ([]alertrules.ContactPoint, error) {
	return f.contacts, nil
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
		InstanceId:        "test-instance-grafana",
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
		t.Fatal("grafana-connector not present in List — AC-1 fail")
	}
}

// TestRun_PushesRuleRecords verifies AC-3/AC-5/AC-6/AC-9.
func TestRun_PushesRuleRecords(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeAlertRulesAPI{
		rules:    []alertrules.RawRule{{UID: "r1", Title: "High latency", RuleType: "grafana", Paused: false, ReceiverName: "sec-oncall"}},
		contacts: []alertrules.ContactPoint{{Name: "sec-oncall", Kind: "slack"}},
	}
	raw, err := alertrules.Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rules := alertcfg.Normalize(alertcfg.VendorGrafana, raw, fixed)
	rec, err := monrecord.Build(rules[0], "scf:MON-01", actorID("alerts"), "grafana", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt hash empty (AC-6 sha256 content-hash)")
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:grafana:alerts@") {
		t.Errorf("actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
	if rec.GetPayload().AsMap()["source_vendor"] != "grafana" {
		t.Errorf("source_vendor = %v", rec.GetPayload().AsMap()["source_vendor"])
	}
}

// TestRun_DedupesWithinHour verifies AC-6.
func TestRun_DedupesWithinHour(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeAlertRulesAPI{rules: []alertrules.RawRule{{UID: "r1", Title: "t"}}}
	raw, _ := alertrules.Collect(context.Background(), api)
	rules := alertcfg.Normalize(alertcfg.VendorGrafana, raw, fixed)
	r1, _ := monrecord.Build(rules[0], "scf:MON-01", actorID("alerts"), "grafana", "prod")
	r2, _ := monrecord.Build(rules[0], "scf:MON-01", actorID("alerts"), "grafana", "prod")
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

// TestEmittedRecords_NoSecretsOrPII verifies AC-10 + P0-488-3: the emitted
// payload carries only the contact-point NAME, never its secret settings.
func TestEmittedRecords_NoSecretsOrPII(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }
	api := &fakeAlertRulesAPI{
		rules:    []alertrules.RawRule{{UID: "r1", Title: "t", ReceiverName: "sec-oncall"}},
		contacts: []alertrules.ContactPoint{{Name: "sec-oncall", Kind: "slack"}},
	}
	raw, _ := alertrules.Collect(context.Background(), api)
	rules := alertcfg.Normalize(alertcfg.VendorGrafana, raw, fixed)
	rec, _ := monrecord.Build(rules[0], "scf:MON-01", actorID("alerts"), "grafana", "prod")

	allowed := map[string]bool{
		"source_vendor": true, "rule_id": true, "rule_name": true, "rule_type": true,
		"enabled": true, "folder": true, "notification_targets": true,
	}
	// The contact-point NAME ("sec-oncall") is allowed; secret-shaped strings
	// are not. The ContactPoint struct has no settings field, so a secret
	// cannot have reached the record at all.
	banned := []string{"webhook.invalid", "https://", "integrationKey", "REDACTED-FIXTURE"}
	assertNoSecret(t, rec, allowed, banned)
}

// TestCredential_NeverLogged verifies AC-11 + P0-488-4.
func TestCredential_NeverLogged(t *testing.T) {
	const token = "test-grafana-token-no-log"
	cred, err := grafanaauth.Resolve(grafanaauth.ResolveOpts{BaseURL: "https://g", Token: token})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), token) {
		t.Fatal("credential String leaks the token — AC-11 violation")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

func assertNoSecret(t *testing.T, rec *evidencev1.EvidenceRecord, allowed map[string]bool, banned []string) {
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
				t.Errorf("payload string %q contains banned substring %q (secret/PII leak)", x, b)
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
