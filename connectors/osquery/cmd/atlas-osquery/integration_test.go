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

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryauth"
	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryposture"
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

// fakeFleetServer assembles an httptest server replaying realistic Fleet
// payloads for /api/v1/fleet/hosts + /api/v1/fleet/hosts/{id}.
func fakeFleetServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/fleet/hosts", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"hosts": [
				{"id": 1, "uuid": "uuid-A", "hostname": "mac-1", "platform": "darwin", "os_version": "macOS 15.1"}
			]
		}`))
	})
	mux.HandleFunc("/api/v1/fleet/hosts/1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"host": {
				"id": 1, "uuid": "uuid-A", "hostname": "mac-1",
				"platform": "darwin", "os_version": "macOS 15.1",
				"disk_encryption_enabled": true,
				"screenlock_enabled": true,
				"firewall_enabled": true,
				"mdm_enrolled": true
			}
		}`))
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
		InstanceId:        "test-instance-osquery",
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
			if strings.Join(h.GetProfilesSupported(), ",") != "pull" {
				t.Errorf("profiles_supported = %v; want [pull]", h.GetProfilesSupported())
			}
		}
	}
	if !found {
		t.Fatal("osquery-connector not present in List response — AC-1 fail")
	}
}

// TestRun_PushesHostPosture verifies AC-2 / AC-3 / AC-4 / AC-5: the run
// path pulls from a fake Fleet API, builds a canonical record, pushes
// through the platform's Push RPC, and the record carries the documented
// scope dimensions + idempotency key shape.
func TestRun_PushesHostPosture(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)

	fleetSrv := fakeFleetServer(t)
	t.Cleanup(fleetSrv.Close)

	creds, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{Token: "fleet-test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	api := osqueryposture.NewFleetClient(fleetSrv.Client(), fleetSrv.URL, creds)
	rows, err := osqueryposture.PullFromFleet(context.Background(), api, func() time.Time {
		return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("PullFromFleet: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}

	rec, err := buildHostPostureRecord(rows[0], "example", "prod", "scf:END-04")
	if err != nil {
		t.Fatalf("buildHostPostureRecord: %v", err)
	}
	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push host_posture: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt hash empty")
	}

	// AC-4: required scope dimensions present with documented values.
	if got := scopeValue(rec.GetScope(), "org"); got != "example" {
		t.Errorf("scope.org = %q; want example", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("scope.environment = %q; want prod", got)
	}
	if got := scopeValue(rec.GetScope(), "cloud_account"); got != "workforce" {
		t.Errorf("scope.cloud_account = %q; want workforce", got)
	}
	// mdm_enrolled=true in the fixture → data_classification=restricted.
	if got := scopeValue(rec.GetScope(), "data_classification"); got != "restricted" {
		t.Errorf("scope.data_classification = %q; want restricted", got)
	}

	// Actor id format follows the cross-connector convention.
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:osquery:posture@") {
		t.Errorf("actor_id = %q; want connector:osquery:posture@<version>", rec.GetSourceAttribution().GetActorId())
	}

	// AC-5: idempotency_key is non-empty 64-char hex.
	if got := rec.GetIdempotencyKey(); len(got) != 64 {
		t.Errorf("idempotency_key length = %d; want 64 hex", len(got))
	}
}

// TestRun_DedupesOnSameHourReplay verifies AC-5 idempotency: a second
// push with the same hour-truncated observed_at returns the same record id.
func TestRun_DedupesOnSameHourReplay(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	state := osqueryposture.HostPosture{
		HostUUID:              "uuid-dedup",
		Hostname:              "host-1",
		Platform:              "darwin",
		OSVersion:             "15.1",
		DiskEncryptionEnabled: true,
		ScreenLockEnabled:     true,
		MDMEnrolled:           true,
		Result:                osqueryposture.ResultPass,
		ObservedAt:            time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
	}
	r1, err := buildHostPostureRecord(state, "example", "prod", "scf:END-04")
	if err != nil {
		t.Fatalf("buildHostPostureRecord r1: %v", err)
	}
	state2 := state
	state2.ObservedAt = time.Date(2026, 5, 11, 12, 30, 0, 0, time.UTC) // same hour
	r2, err := buildHostPostureRecord(state2, "example", "prod", "scf:END-04")
	if err != nil {
		t.Fatalf("buildHostPostureRecord r2: %v", err)
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
	if r1.GetIdempotencyKey() != r2.GetIdempotencyKey() {
		t.Fatalf("idempotency_key differs within hour: %q vs %q",
			r1.GetIdempotencyKey(), r2.GetIdempotencyKey())
	}
}

// TestBuildHostPostureRecord_RejectsMissingHostUUID pins the anti-criterion:
// no record may ship without an idempotency key derived from host_uuid.
func TestBuildHostPostureRecord_RejectsMissingHostUUID(t *testing.T) {
	state := osqueryposture.HostPosture{
		HostUUID:   "", // missing
		Hostname:   "x",
		Platform:   "darwin",
		ObservedAt: time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
	}
	if _, err := buildHostPostureRecord(state, "example", "prod", "scf:END-04"); err == nil {
		t.Fatal("expected error on missing host_uuid; got nil")
	}
}

// TestBuildHostPostureRecord_DataClassificationFromMDM verifies the
// per-host AC-4 inference: MDM-enrolled → restricted; un-enrolled → unknown.
func TestBuildHostPostureRecord_DataClassificationFromMDM(t *testing.T) {
	base := osqueryposture.HostPosture{
		HostUUID:   "uuid-1",
		Hostname:   "h",
		Platform:   "darwin",
		ObservedAt: time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
	}
	managed := base
	managed.MDMEnrolled = true
	r, err := buildHostPostureRecord(managed, "o", "e", "scf:END-04")
	if err != nil {
		t.Fatalf("managed: %v", err)
	}
	if got := scopeValue(r.GetScope(), "data_classification"); got != "restricted" {
		t.Errorf("managed data_classification = %q; want restricted", got)
	}

	unmanaged := base
	unmanaged.MDMEnrolled = false
	r2, err := buildHostPostureRecord(unmanaged, "o", "e", "scf:END-04")
	if err != nil {
		t.Fatalf("unmanaged: %v", err)
	}
	if got := scopeValue(r2.GetScope(), "data_classification"); got != "unknown" {
		t.Errorf("unmanaged data_classification = %q; want unknown", got)
	}
}

// TestRun_LocalModeNotWiredYet verifies the slice contract: --mode=local
// is wired in configuration surface but the transport returns a clear
// sentinel error rather than a silent fallthrough. Slice 047 ships the
// Fleet path; local-socket transport lands in a follow-up.
func TestRun_LocalModeNotWiredYet(t *testing.T) {
	common.endpoint = "127.0.0.1:1"
	common.token = "x"
	common.insecure = true
	t.Cleanup(func() {
		common.endpoint = ""
		common.token = ""
		common.insecure = false
	})
	err := doRun(context.Background(), runFlags{
		mode:           "local",
		org:            "example",
		environment:    "prod",
		osqueryDSocket: "/var/osquery/osquery.em",
		hostPostureCtl: "scf:END-04",
	})
	if err == nil {
		t.Fatal("expected ErrLocalSocketNotWired; got nil")
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Errorf("error = %v; want sentinel containing 'not wired'", err)
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
