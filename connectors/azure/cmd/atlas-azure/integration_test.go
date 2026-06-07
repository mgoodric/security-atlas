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

	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
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

// TestRegister_ListsConnector verifies AC-1 + AC-7: register surfaces this
// connector via the ConnectorRegistry List RPC with profiles_supported=[pull].
func TestRegister_ListsConnector(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	registry := connectorsv1.NewConnectorRegistryServiceClient(conn)

	ctx, cancel := authedTestContext(bearer, 5*time.Second)()
	defer cancel()
	resp, err := registry.Register(ctx, &connectorsv1.RegisterRequest{
		Name:              ConnectorName,
		Version:           connectorVersion(),
		InstanceId:        "test-instance-azure",
		SupportedKinds:    SupportedKinds,
		ProfilesSupported: []string{"pull"},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.GetHandle().GetName() != ConnectorName {
		t.Fatalf("name = %q; want %q", resp.GetHandle().GetName(), ConnectorName)
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
			if len(h.GetSupportedKinds()) != 2 {
				t.Errorf("supported_kinds = %d; want 2", len(h.GetSupportedKinds()))
			}
			if strings.Join(h.GetProfilesSupported(), ",") != "pull" {
				t.Errorf("profiles_supported = %v; want [pull]", h.GetProfilesSupported())
			}
		}
	}
	if !found {
		t.Fatal("azure-connector not present in List — AC-1 fail")
	}
}

// TestRun_PushesBothKinds verifies AC-2/AC-3/AC-5/AC-6/AC-9: collect from faked
// Graph + ARM surfaces, build canonical records, push them through the
// platform's single Push RPC, and assert the receipt (sha256 content hash).
func TestRun_PushesBothKinds(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	// Entra (faked Graph surface — NO live Azure).
	entraAPI := &fakeGraphForIntegration{assignments: []entra.RawAssignment{
		{ID: "ra-1", PrincipalID: "p-1", PrincipalType: "user", PrincipalDisplayName: "Alice",
			RoleDefinitionID: "role-1", RoleDisplayName: "Reader", DirectoryScopeID: "/"},
	}}
	assignments, err := entra.Pull(context.Background(), entraAPI, "tenant-1", fixed)
	if err != nil {
		t.Fatalf("entra.Pull: %v", err)
	}
	entraRec, err := buildEntraRecord(assignments[0], "prod", "scf:IAC-21")
	if err != nil {
		t.Fatalf("buildEntraRecord: %v", err)
	}
	entraReceipt, err := client.Push(context.Background(), entraRec)
	if err != nil {
		t.Fatalf("Push entra: %v", err)
	}
	if entraReceipt.GetHash() == "" {
		t.Fatal("entra receipt hash empty (AC-6 sha256 content-hash)")
	}
	if !strings.HasPrefix(entraRec.GetSourceAttribution().GetActorId(), "connector:azure:entra@") {
		t.Errorf("entra actor_id = %q", entraRec.GetSourceAttribution().GetActorId())
	}

	// Storage (faked ARM surface — NO live Azure).
	armAPI := &fakeARMForIntegration{accounts: []storage.RawAccount{
		{ID: "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct",
			Name: "acct", ResourceGroup: "rg", Location: "eastus", EncryptionEnabled: true,
			EncryptionKeySource: "Microsoft.Storage", HTTPSTrafficOnly: true, MinimumTLSVersion: "TLS1_2"},
	}}
	accounts, err := storage.Inspect(context.Background(), armAPI, "sub-1", fixed)
	if err != nil {
		t.Fatalf("storage.Inspect: %v", err)
	}
	storageRec, err := buildStorageRecord(accounts[0], "prod", "scf:CRY-04")
	if err != nil {
		t.Fatalf("buildStorageRecord: %v", err)
	}
	storageReceipt, err := client.Push(context.Background(), storageRec)
	if err != nil {
		t.Fatalf("Push storage: %v", err)
	}
	if storageReceipt.GetHash() == "" {
		t.Fatal("storage receipt hash empty")
	}
	if accounts[0].Result != storage.ResultPass {
		t.Errorf("hardened account should PASS; got %q", accounts[0].Result)
	}
	if got := scopeValue(storageRec.GetScope(), "cloud_account"); got != "azure:sub-1" {
		t.Errorf("storage cloud_account = %q; want azure:sub-1", got)
	}
}

// TestRun_DedupesWithinHour verifies AC-6: two records from the same resource in
// the same hour share an idempotency_key, so the platform dedup returns the same
// record_id.
func TestRun_DedupesWithinHour(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	api := &fakeARMForIntegration{accounts: []storage.RawAccount{
		{ID: "/sub/acct", Name: "acct", EncryptionEnabled: true, EncryptionKeySource: "Microsoft.Storage", HTTPSTrafficOnly: true},
	}}
	accts, _ := storage.Inspect(context.Background(), api, "sub-1", fixed)
	r1, _ := buildStorageRecord(accts[0], "prod", "scf:CRY-04")
	r2, _ := buildStorageRecord(accts[0], "prod", "scf:CRY-04")
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

// TestEmittedRecords_NoPIIOrSecrets verifies AC-10 + P0-486-3/P0-486-4: the
// emitted payloads carry ONLY config / role-assignment metadata — never blob
// contents, Key-Vault secrets, access keys, SAS tokens, or the Azure
// credential.
func TestEmittedRecords_NoPIIOrSecrets(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }
	const secret = "test-azure-client-secret"

	entraAPI := &fakeGraphForIntegration{assignments: []entra.RawAssignment{
		{ID: "ra", PrincipalID: "p", PrincipalType: "user", PrincipalDisplayName: "Alice",
			RoleDefinitionID: "role", RoleDisplayName: "Reader"},
	}}
	assignments, _ := entra.Pull(context.Background(), entraAPI, "tenant-1", fixed)
	entraRec, _ := buildEntraRecord(assignments[0], "prod", "scf:IAC-21")

	armAPI := &fakeARMForIntegration{accounts: []storage.RawAccount{
		{ID: "/sub/a", Name: "a", EncryptionEnabled: true, EncryptionKeySource: "Microsoft.Storage", HTTPSTrafficOnly: true},
	}}
	accts, _ := storage.Inspect(context.Background(), armAPI, "sub-1", fixed)
	storageRec, _ := buildStorageRecord(accts[0], "prod", "scf:CRY-04")

	// Allow-list of permitted payload keys per kind. Any key NOT in the
	// allow-list is a leak and fails the test (config/assignment metadata only).
	entraAllowed := map[string]bool{
		"assignment_id": true, "principal_id": true, "principal_type": true,
		"principal_display_name": true, "role_definition_id": true,
		"role_display_name": true, "directory_scope_id": true,
		"is_privileged": true, "tenant_id": true,
	}
	storageAllowed := map[string]bool{
		"account_id": true, "account_name": true, "subscription_id": true,
		"resource_group": true, "location": true, "encryption_enabled": true,
		"encryption_key_source": true, "https_traffic_only": true,
		"minimum_tls_version": true, "allow_blob_public_access": true,
	}
	bannedSubstrings := []string{"key_value", "secret", "sas", "connection_string",
		"access_key", "blob_content", "password", "mailbox", "email", "upn"}

	check := func(t *testing.T, rec *evidencev1.EvidenceRecord, allowed map[string]bool) {
		for k, v := range rec.GetPayload().AsMap() {
			if !allowed[k] {
				t.Errorf("payload carries non-allow-listed key %q (possible PII/secret leak)", k)
			}
			low := strings.ToLower(k)
			for _, b := range bannedSubstrings {
				if strings.Contains(low, b) {
					t.Errorf("payload key %q contains banned substring %q", k, b)
				}
			}
			// No payload value may equal the Azure credential secret.
			if s, ok := v.(string); ok && strings.Contains(s, secret) {
				t.Errorf("payload value for %q leaked the Azure secret", k)
			}
		}
	}
	check(t, entraRec, entraAllowed)
	check(t, storageRec, storageAllowed)
}

// TestCredential_NeverLogged verifies AC-11 + P0-486-4: the Azure credential's
// formatted forms never reveal the secret, so no log line can leak it.
func TestCredential_NeverLogged(t *testing.T) {
	const secret = "test-azure-secret-no-log"
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{
		TenantID: "tenant-1", ClientID: "client-1", ClientSecret: secret,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), secret) {
		t.Fatal("credential String leaks the secret — AC-11 violation")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

// --- faked Azure surfaces (NO live Azure in tests) ---

type fakeGraphForIntegration struct{ assignments []entra.RawAssignment }

func (f *fakeGraphForIntegration) ListRoleAssignments(_ context.Context) ([]entra.RawAssignment, error) {
	return f.assignments, nil
}

type fakeARMForIntegration struct{ accounts []storage.RawAccount }

func (f *fakeARMForIntegration) ListStorageAccounts(_ context.Context) ([]storage.RawAccount, error) {
	return f.accounts, nil
}
