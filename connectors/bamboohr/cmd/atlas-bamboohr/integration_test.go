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

	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/bamboohrauth"
	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/workers"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/hris/workerrecord"
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

// fakeWorkersAPI is a faked BambooHR worker surface (NO live BambooHR).
type fakeWorkersAPI struct{ workers []workers.RawWorker }

func (f *fakeWorkersAPI) ListWorkers(_ context.Context) ([]workers.RawWorker, error) {
	return f.workers, nil
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
		InstanceId:        "test-instance-bamboohr",
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
		t.Fatal("bamboohr-connector not present in List — AC-1 fail")
	}
}

// TestRun_PushesWorkerRecords verifies AC-3/AC-5/AC-6/AC-9.
func TestRun_PushesWorkerRecords(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeWorkersAPI{workers: []workers.RawWorker{
		{ID: "42", Status: "Active", HireDate: "2024-01-15", JobTitle: "Software Engineer",
			Department: "Engineering", ManagerAssignmentID: "7", WorkEmail: "a.engineer@corp.example"},
	}}
	raw, err := workers.Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	wks := worker.Normalize(worker.HRISBambooHR, raw, fixed)
	rec, err := workerrecord.Build(wks[0], "scf:IAC-22", actorID("workers"), "bamboohr", "prod")
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
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:bamboohr:workers@") {
		t.Errorf("actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
}

// TestRun_DedupesWithinHour verifies AC-6.
func TestRun_DedupesWithinHour(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeWorkersAPI{workers: []workers.RawWorker{{ID: "1", Status: "Active"}}}
	raw, _ := workers.Collect(context.Background(), api)
	wks := worker.Normalize(worker.HRISBambooHR, raw, fixed)
	r1, _ := workerrecord.Build(wks[0], "scf:IAC-22", actorID("workers"), "bamboohr", "prod")
	r2, _ := workerrecord.Build(wks[0], "scf:IAC-22", actorID("workers"), "bamboohr", "prod")
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

// TestEmittedRecords_NoSensitivePII is the LOAD-BEARING over-collection guard
// (AC-10 + P0-491-3): the emitted payload carries worker-lifecycle facts only —
// no SSN / compensation / address / bank / benefits / performance / DOB /
// personal-contact keys or substrings.
func TestEmittedRecords_NoSensitivePII(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }
	api := &fakeWorkersAPI{workers: []workers.RawWorker{
		{ID: "42", Status: "Inactive", HireDate: "2024-01-15", TerminationDate: "2026-05-31",
			JobTitle: "Software Engineer", Department: "Engineering", ManagerAssignmentID: "7",
			WorkEmail: "a.engineer@corp.example"},
	}}
	raw, _ := workers.Collect(context.Background(), api)
	wks := worker.Normalize(worker.HRISBambooHR, raw, fixed)
	rec, _ := workerrecord.Build(wks[0], "scf:IAC-22", actorID("workers"), "bamboohr", "prod")

	allowed := map[string]bool{
		"source_hris": true, "worker_id": true, "employment_status": true,
		"start_date": true, "end_date": true, "title": true, "department": true,
		"manager_assignment_id": true, "work_email": true,
	}
	banned := []string{"ssn", "national_id", "salary", "compensation", "payrate", "bank",
		"routing", "iban", "address", "benefit", "health", "performance", "rating",
		"dob", "birth", "gender", "ethnicity"}
	assertNoBanned(t, rec, allowed, banned)
}

// TestCredential_NeverLogged verifies AC-11 + P0-491-4.
func TestCredential_NeverLogged(t *testing.T) {
	const secret = "fake-bamboo-secret-no-log"
	cred, err := bamboohrauth.Resolve(bamboohrauth.ResolveOpts{CompanyDomain: "acme", APIKey: secret})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), secret) {
		t.Fatal("credential String leaks the key — AC-11 violation")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

func assertNoBanned(t *testing.T, rec *evidencev1.EvidenceRecord, allowed map[string]bool, banned []string) {
	t.Helper()
	pm := rec.GetPayload().AsMap()
	for k := range pm {
		if !allowed[k] {
			t.Errorf("non-allow-listed payload key %q (over-collection guard P0-491-3)", k)
		}
	}
	walk(t, pm, banned)
}

func walk(t *testing.T, v any, banned []string) {
	t.Helper()
	switch x := v.(type) {
	case string:
		for _, b := range banned {
			if strings.Contains(strings.ToLower(x), b) {
				t.Errorf("payload string %q contains banned substring %q (over-collection)", x, b)
			}
		}
	case map[string]any:
		for k := range x {
			for _, b := range banned {
				if strings.Contains(strings.ToLower(k), b) {
					t.Errorf("payload key %q contains banned substring %q (over-collection)", k, b)
				}
			}
			walk(t, x[k], banned)
		}
	case []any:
		for _, vv := range x {
			walk(t, vv, banned)
		}
	}
}
