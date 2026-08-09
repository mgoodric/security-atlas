//go:build integration

// Integration tests for OE-435 (slice 356b chaos gap G-1): push credentials
// must survive an atlas process restart.
//
// The slice-356b chaos run proved the opposite on the shipped tree: a
// `docker compose restart atlas` at t=+10s of a 60s 1/s push stream turned
// every subsequent push into `Unauthenticated` — 49 of 60 evidence records
// lost, and the pre-restart credential never worked again. Root cause (its
// decisions log D4): the credstore was memory-only.
//
// These tests reproduce that scenario at the integration tier against real
// Postgres with RLS enforced — issue via the AdminCredentials gRPC service
// (the connector/CI issuance path the chaos run used), "restart" by
// constructing a fresh api.Server + apikeystore.Persister over the same
// database, then push through the real gRPC auth interceptor. They FAIL if
// a credential is lost across the restart.
//
// Run via: just test-integration equivalent for ./internal/auth
// (needs DATABASE_URL_APP + DATABASE_URL; see TestMain in
// integration_test.go, whose pools and hasher this file reuses).
package auth_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// atlasProcess is one simulated atlas process: an api.Server with credstore
// persistence attached, serving gRPC on bufconn. Constructing a second one
// over the same pools IS the restart — nothing in-memory survives into it.
type atlasProcess struct {
	srv    *api.Server
	dialer func(context.Context, string) (net.Conn, error)
}

func bootAtlas(t *testing.T) *atlasProcess {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the load-at-boot scan needs the BYPASSRLS pool")
	}
	srv := api.New(api.Config{RotationGrace: time.Hour})
	// Production order (cmd/atlas/main.go): bootstrap issuance happens
	// before the persistence attach; the attach rehydrates from api_keys.
	persister := apikeystore.NewPersister(appPool, adminPool, testHasher)
	if _, err := srv.AttachCredstorePersistence(persister); err != nil {
		t.Fatalf("AttachCredstorePersistence: %v", err)
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(listener) }()
	t.Cleanup(func() {
		srv.GRPC.GracefulStop()
		_ = listener.Close()
	})
	return &atlasProcess{
		srv:    srv,
		dialer: func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) },
	}
}

func (p *atlasProcess) dial(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(p.dialer),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func credBearerCtx(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func pushRecord(idemKey string) *evidencev1.EvidenceRecord {
	payload, _ := structpb.NewStruct(map[string]any{"tool": "semgrep"})
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey,
		EvidenceKind:   "sast.scan_result.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:VPM-04",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "environment", Values: []string{"prod"}},
		},
		ObservedAt:        timestamppb.New(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)),
		Result:            evidencev1.Result_RESULT_PASS,
		Payload:           payload,
		SourceAttribution: &evidencev1.SourceAttribution{ActorType: "service_account", ActorId: "chaos.probe"},
	}
}

// issueTenantCred mints a tenant push credential the way the chaos run's
// ensure-chaos-tenant.sh did: a bootstrap admin bearer calling the
// AdminCredentials.Issue RPC.
func issueTenantCred(t *testing.T, p *atlasProcess, tenantID string) (id, bearer string) {
	t.Helper()
	// The bootstrap bearer lives on its own ops tenant so the target
	// tenant's api_keys rows are exactly the ones the tests issue for it.
	_, adminBearer, err := p.srv.IssueBootstrapCredential(uuid.New().String())
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	stub := adminv1.NewAdminCredentialsServiceClient(p.dial(t))
	resp, err := stub.Issue(credBearerCtx(adminBearer), &adminv1.IssueRequest{TenantId: tenantID})
	if err != nil {
		t.Fatalf("AdminCredentials.Issue: %v", err)
	}
	return resp.GetHandle().GetId(), resp.GetBearerToken()
}

func mustPush(t *testing.T, p *atlasProcess, bearer, idemKey, credID string) {
	t.Helper()
	stub := evidencev1.NewEvidenceIngestServiceClient(p.dial(t))
	resp, err := stub.Push(credBearerCtx(bearer), &evidencev1.PushRequest{Record: pushRecord(idemKey)})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := resp.GetReceipt().GetCredentialId(); credID != "" && got != credID {
		t.Fatalf("receipt credential_id %q, want %q — credential identity changed", got, credID)
	}
}

func mustPushFail(t *testing.T, p *atlasProcess, bearer, idemKey string, wantCode codes.Code) {
	t.Helper()
	stub := evidencev1.NewEvidenceIngestServiceClient(p.dial(t))
	_, err := stub.Push(credBearerCtx(bearer), &evidencev1.PushRequest{Record: pushRecord(idemKey)})
	if status.Code(err) != wantCode {
		t.Fatalf("Push: got %v, want %v", err, wantCode)
	}
}

// === The headline AC: issue → restart → push ===
//
// This is the exact sequence the chaos run measured as 49/60 records lost:
// a credential proven working before the restart must keep working after.
func TestCredPersist_IssueRestartPush(t *testing.T) {
	tenant := uuid.New().String()

	atlas1 := bootAtlas(t)
	credID, bearer := issueTenantCred(t, atlas1, tenant)
	// Tick 0-10 equivalent: proven working before the restart.
	mustPush(t, atlas1, bearer, "pre-restart", credID)

	// The restart. Fresh process, fresh in-memory state, same database.
	atlas2 := bootAtlas(t)
	// Tick 11+ equivalent: on the shipped tree this returned
	// Unauthenticated forever. It must now succeed, under the same
	// credential identity.
	mustPush(t, atlas2, bearer, "post-restart", credID)
}

// === Revocation must also survive a restart ===
//
// The durability cut both ways in review: a revoke recorded only in memory
// would silently resurrect the key at the next boot.
func TestCredPersist_RevokeSurvivesRestart(t *testing.T) {
	tenant := uuid.New().String()

	atlas1 := bootAtlas(t)
	credID, bearer := issueTenantCred(t, atlas1, tenant)
	mustPush(t, atlas1, bearer, "before-revoke", credID)

	stub := adminv1.NewAdminCredentialsServiceClient(atlas1.dial(t))
	if _, err := stub.Revoke(credBearerCtx(bearer), &adminv1.RevokeRequest{Id: credID}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	mustPushFail(t, atlas1, bearer, "after-revoke", codes.Unauthenticated)

	atlas2 := bootAtlas(t)
	mustPushFail(t, atlas2, bearer, "after-revoke-restart", codes.Unauthenticated)
}

// === Rotation state must survive a restart ===
//
// The successor authenticates after the restart; the rotated-out
// predecessor keeps authenticating inside its grace window (RotationGrace
// is 1h here), exactly as it would have without the restart.
func TestCredPersist_RotateSurvivesRestart(t *testing.T) {
	tenant := uuid.New().String()

	atlas1 := bootAtlas(t)
	predID, predBearer := issueTenantCred(t, atlas1, tenant)

	stub := adminv1.NewAdminCredentialsServiceClient(atlas1.dial(t))
	resp, err := stub.Rotate(credBearerCtx(predBearer), &adminv1.RotateRequest{Id: predID})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	succID, succBearer := resp.GetHandle().GetId(), resp.GetBearerToken()

	atlas2 := bootAtlas(t)
	mustPush(t, atlas2, succBearer, "successor-post-restart", succID)
	mustPush(t, atlas2, predBearer, "pred-in-grace-post-restart", predID)
}

// === Bootstrap credentials stay memory-only, and keep working ===
//
// cmd/atlas issues bootstrap credentials BEFORE the persistence attach;
// they authenticate via the credstore's legacy-hash fallback and must not
// leak into api_keys (they are re-minted from the environment each boot).
// bootAtlas attaches before issuance, so this test mirrors the production
// order explicitly.
func TestCredPersist_BootstrapCredStaysMemoryOnly(t *testing.T) {
	tenant := uuid.New()

	srv := api.New(api.Config{RotationGrace: time.Hour})
	_, adminBearer, err := srv.IssueBootstrapCredential(tenant.String())
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	if _, err := srv.AttachCredstorePersistence(apikeystore.NewPersister(appPool, adminPool, testHasher)); err != nil {
		t.Fatalf("AttachCredstorePersistence: %v", err)
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(listener) }()
	t.Cleanup(func() {
		srv.GRPC.GracefulStop()
		_ = listener.Close()
	})
	p := &atlasProcess{srv: srv, dialer: func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }}

	// The pre-attach bootstrap credential still authenticates...
	mustPush(t, p, adminBearer, "bootstrap-push", "")

	// ...and left no row behind for its tenant.
	if n := countKeysAs(t, tenant, tenant); n != 0 {
		t.Fatalf("bootstrap credential leaked %d row(s) into api_keys", n)
	}
}

// countKeysAs counts rowTenant's api_keys rows through the RLS-bound app
// pool while the session's tenant context is asTenant.
func countKeysAs(t *testing.T, asTenant, rowTenant uuid.UUID) int {
	t.Helper()
	ctx := withTenant(t, asTenant)
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE tenant_id = $1`, rowTenant).Scan(&n); err != nil {
		t.Fatalf("count api_keys: %v", err)
	}
	return n
}

// === Persisted rows are tenant-scoped, RLS-enforced, hashed at rest ===
func TestCredPersist_RLSAndHashedAtRest(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()

	atlas1 := bootAtlas(t)
	credID, bearer := issueTenantCred(t, atlas1, tenantA.String())

	// Tenant A sees its row through the RLS-bound app pool; tenant B
	// sees nothing (constitutional invariant #6).
	if n := countKeysAs(t, tenantA, tenantA); n != 1 {
		t.Fatalf("tenant A sees %d api_keys rows, want 1", n)
	}
	if n := countKeysAs(t, tenantB, tenantA); n != 0 {
		t.Fatalf("tenant B sees %d of tenant A's api_keys rows; RLS bypassed", n)
	}

	// At rest the row holds the HMAC of the bearer — never the plaintext.
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping at-rest hash assertion")
	}
	var storedHash []byte
	var last4 string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT token_hash, last4 FROM api_keys WHERE tenant_id = $1`, tenantA).Scan(&storedHash, &last4); err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	want := testHasher.Hash(bearer)
	if string(storedHash) != string(want) {
		t.Fatalf("stored token_hash is not HMAC-SHA256(bearer) — credential %s would not round-trip", credID)
	}
	if string(storedHash) == bearer || last4 != bearer[len(bearer)-4:] {
		t.Fatalf("at-rest row leaks more than last4 of the plaintext")
	}
}
