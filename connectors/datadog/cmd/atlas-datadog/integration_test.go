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

	"github.com/mgoodric/security-atlas/connectors/datadog/internal/datadogauth"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/monitors"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/siemrules"
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

// fakeMonitorsAPI is a faked Datadog monitor surface (NO live Datadog).
type fakeMonitorsAPI struct{ monitors []monitors.RawMonitor }

func (f *fakeMonitorsAPI) ListMonitors(_ context.Context) ([]monitors.RawMonitor, error) {
	return f.monitors, nil
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
		InstanceId:        "test-instance-datadog",
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
		t.Fatal("datadog-connector not present in List — AC-1 fail")
	}
}

// TestRun_PushesMonitorRecords verifies AC-2/AC-5/AC-6/AC-9: collect from a
// faked Datadog API, build the canonical record, push through the platform's
// single Push RPC, and assert the receipt (sha256 content hash).
func TestRun_PushesMonitorRecords(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeMonitorsAPI{monitors: []monitors.RawMonitor{
		{ID: "12345", Name: "API 5xx", Type: "metric alert", Enabled: true, Message: "@slack-sec-oncall @pagerduty-primary"},
	}}
	raw, err := monitors.Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rules := alertcfg.Normalize(alertcfg.VendorDatadog, raw, fixed)
	rec, err := monrecord.Build(rules[0], "scf:MON-01", actorID("monitors"), "datadog", "prod")
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
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:datadog:monitors@") {
		t.Errorf("actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
}

// TestRun_DedupesWithinHour verifies AC-6.
func TestRun_DedupesWithinHour(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeMonitorsAPI{monitors: []monitors.RawMonitor{{ID: "1", Name: "n", Type: "t", Enabled: true}}}
	raw, _ := monitors.Collect(context.Background(), api)
	rules := alertcfg.Normalize(alertcfg.VendorDatadog, raw, fixed)
	r1, _ := monrecord.Build(rules[0], "scf:MON-01", actorID("monitors"), "datadog", "prod")
	r2, _ := monrecord.Build(rules[0], "scf:MON-01", actorID("monitors"), "datadog", "prod")
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

// TestEmittedRecords_NoSecretsOrPII verifies AC-10 + P0-488-3: even when the
// monitor message embeds an email recipient and a webhook-shaped string, the
// emitted payload carries config + target-name metadata only.
func TestEmittedRecords_NoSecretsOrPII(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }
	api := &fakeMonitorsAPI{monitors: []monitors.RawMonitor{
		{ID: "1", Name: "n", Type: "metric alert", Enabled: true,
			Message: "page @oncall@fixture.invalid via https://webhook.invalid/REDACTED-FIXTURE @slack-ops"},
	}}
	raw, _ := monitors.Collect(context.Background(), api)
	rules := alertcfg.Normalize(alertcfg.VendorDatadog, raw, fixed)
	rec, _ := monrecord.Build(rules[0], "scf:MON-01", actorID("monitors"), "datadog", "prod")

	allowed := map[string]bool{
		"source_vendor": true, "rule_id": true, "rule_name": true, "rule_type": true,
		"enabled": true, "folder": true, "notification_targets": true,
	}
	banned := []string{"fixture.invalid", "webhook.invalid", "REDACTED-FIXTURE", "oncall@", "https://"}
	assertNoSecret(t, rec, allowed, banned)
}

// fakeSIEMAPI is a faked Datadog security-monitoring rules surface (NO live
// Datadog).
type fakeSIEMAPI struct{ rules []siemrules.RawRule }

func (f *fakeSIEMAPI) ListRules(_ context.Context) ([]siemrules.RawRule, error) {
	return f.rules, nil
}

// TestRun_PushesSIEMRuleRecords verifies the slice-533 end-to-end path: collect
// from a faked Datadog Security-Monitoring API, build the datadog.siem_rule.v1
// record, push through the platform's single Push RPC, and assert the receipt
// (sha256 content hash) + the sibling kind + the actor_id.
func TestRun_PushesSIEMRuleRecords(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeSIEMAPI{rules: []siemrules.RawRule{
		{ID: "rule-aaa", Name: "Brute force on login", DetectionClass: "log_detection",
			Enabled: true, Severity: "high", Handles: []string{"@slack-sec-oncall"}},
	}}
	rules, err := siemrules.Collect(context.Background(), api, fixed)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rec, err := siemrules.Build(rules[0], "scf:THR-01", actorID("siemrules"), "datadog", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.GetEvidenceKind() != "datadog.siem_rule.v1" {
		t.Errorf("kind = %q; want datadog.siem_rule.v1", rec.GetEvidenceKind())
	}
	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt hash empty (sha256 content-hash)")
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:datadog:siemrules@") {
		t.Errorf("actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
}

// TestSIEMEmittedRecords_NoSecretsOrPII verifies P0-533: even when a rule's
// notification list embeds an email recipient and a webhook-shaped string, the
// emitted payload carries config + target-name metadata only.
func TestSIEMEmittedRecords_NoSecretsOrPII(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }
	api := &fakeSIEMAPI{rules: []siemrules.RawRule{
		{ID: "1", Name: "n", DetectionClass: "log", Enabled: true, Severity: "high",
			Handles: []string{"@oncall@fixture.invalid", "@slack-ops"}},
	}}
	rules, _ := siemrules.Collect(context.Background(), api, fixed)
	rec, _ := siemrules.Build(rules[0], "scf:THR-01", actorID("siemrules"), "datadog", "prod")

	allowed := map[string]bool{
		"rule_id": true, "rule_name": true, "detection_class": true,
		"enabled": true, "severity": true, "notification_targets": true,
	}
	banned := []string{"fixture.invalid", "webhook.invalid", "oncall@", "https://", "@example.com"}
	assertNoSecret(t, rec, allowed, banned)
}

// TestCredential_NeverLogged verifies AC-11 + P0-488-4.
func TestCredential_NeverLogged(t *testing.T) {
	const apiKey = "test-datadog-api-key-no-log"
	const appKey = "test-datadog-app-key-no-log"
	cred, err := datadogauth.Resolve(datadogauth.ResolveOpts{APIKey: apiKey, AppKey: appKey})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), apiKey) || strings.Contains(cred.String(), appKey) {
		t.Fatal("credential String leaks a key — AC-11 violation")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

// assertNoSecret walks every string in the record payload and asserts no
// banned substring (a secret URL fragment / a recipient email) appears, and
// that only allow-listed top-level keys are present.
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
