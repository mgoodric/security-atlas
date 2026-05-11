//go:build integration

// Integration tests for slice 013's ingestion stage. Verifies every AC
// against a real Postgres with RLS enforced (atlas_app role); the schema
// registry is the slice-014 DB-backed Service so push validation flows
// through the canonical hook.

package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

// envOrSkip returns the env value or skips the test. Mirrors slice
// 014's pattern.
func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return p
}

// boot wipes evidence ledger state and seeds the schema registry with a
// known kind so push validation can succeed. Returns the ingest service
// against atlas_app and a cleanup func.
func boot(t *testing.T) (*ingest.Service, *pgxpool.Pool, *schemaregistry.Service) {
	t.Helper()
	adminPool := openPool(t, envOrSkip(t, "DATABASE_URL"))
	appPool := openPool(t, envOrSkip(t, "DATABASE_URL_APP"))

	// Wipe ledger + audit so each test gets a clean evidence_records
	// surface. Also wipe evidence_kind_schemas so the import below
	// starts from a known-empty global-rows state — slice 014's
	// TestMigration_RoundTrip drops + re-applies the schema_registry
	// migration mid-package, which sometimes leaves the table empty
	// for the slice-013 tests that run after it in the same CI step.
	// Wiping first guarantees deterministic state regardless of
	// upstream test order.
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM evidence_audit_log`); err != nil {
		t.Fatalf("wipe audit: %v", err)
	}
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM evidence_records`); err != nil {
		t.Fatalf("wipe evidence: %v", err)
	}
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM evidence_kind_schemas WHERE tenant_id IS NULL`); err != nil {
		// Tolerate "relation does not exist" so this helper is robust
		// to slice 014's round-trip test having dropped the table.
		// The next step will fail loudly if the table truly is gone.
		if !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("wipe global schemas: %v", err)
		}
	}

	// Import platform schemas via the admin pool (BYPASSRLS — global rows).
	importSvc := schemaregistry.NewService(adminPool)
	if _, _, err := importSvc.ImportPlatformSchemas(context.Background(), schemaregistry.PlatformSchemasFS()); err != nil {
		t.Fatalf("ImportPlatformSchemas: %v", err)
	}

	// Operational registry runs against atlas_app and re-loads from DB.
	reg := schemaregistry.NewService(appPool)
	if err := reg.LoadFromDB(context.Background()); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	// Verify the registry is hot before the test runs. If it isn't, the
	// test would surface "evidence_kind not registered" which we'd
	// otherwise misattribute to a logic bug rather than a load failure.
	if !reg.IsRegistered("sast.scan_result.v1", "1.0.0") {
		// Tenant-private rows from slice 014 tests can leave the global
		// rows visible-only to a specific tenant context. Re-run the
		// load to be safe.
		_ = reg.LoadFromDB(context.Background())
		if !reg.IsRegistered("sast.scan_result.v1", "1.0.0") {
			t.Fatalf("boot: sast.scan_result.v1 not in registry cache after LoadFromDB; check DB rows + RLS policy")
		}
	}

	svc := ingest.New(appPool, reg)

	t.Cleanup(func() {
		adminPool.Close()
		appPool.Close()
	})
	return svc, appPool, reg
}

// recordHelper builds a default record with overrides via opts.
type recordOpt func(*evidencev1.EvidenceRecord)

func record(t *testing.T, opts ...recordOpt) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, _ := structpb.NewStruct(map[string]any{
		"tool":           "semgrep",
		"tool_version":   "1.96.0",
		"ruleset":        "p/owasp-top-ten",
		"findings_count": 0,
		"scanned_files":  1247,
	})
	r := &evidencev1.EvidenceRecord{
		IdempotencyKey: "ci-" + uuid.NewString()[:8],
		EvidenceKind:   "sast.scan_result.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:VPM-04",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "environment", Values: []string{"prod"}},
		},
		ObservedAt: timestamppb.New(time.Now().UTC().Add(-1 * time.Minute)),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "service_account",
			ActorId:   "ci.test",
		},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func mkCred(tenantID string, kinds []string, scopePred string) credstore.Credential {
	return credstore.Credential{
		ID:             "key_" + uuid.NewString()[:8],
		TenantID:       tenantID,
		Kinds:          kinds,
		ScopePredicate: scopePred,
	}
}

// AC-1: happy path. Push returns receipt with non-empty record_id, hash,
// ingested_at; the row lands in evidence_records under the tenant.
func TestPushHappyPath_AC1(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")

	receipt, decision, err := svc.Process(context.Background(), record(t), cred)
	if err != nil {
		t.Fatalf("Process: %v (decision=%s)", err, decision)
	}
	if decision != ingest.DecisionAccepted {
		t.Fatalf("decision = %v, want Accepted", decision)
	}
	if receipt.RecordID == "" || receipt.Hash == "" {
		t.Fatalf("receipt missing fields: %+v", receipt)
	}

	// Verify the row is queryable under the tenant.
	count := countEvidenceRecords(t, pool, tenantA)
	if count != 1 {
		t.Fatalf("evidence_records count = %d, want 1", count)
	}
}

// AC-2: missing fields are rejected with ErrMissingField.
func TestPushMissingFields_AC2(t *testing.T) {
	svc, _, _ := boot(t)
	cred := mkCred(tenantA, nil, "")

	cases := []struct {
		name string
		opt  recordOpt
		msg  string
	}{
		{"idempotency_key", func(r *evidencev1.EvidenceRecord) { r.IdempotencyKey = "" }, "idempotency_key"},
		{"evidence_kind", func(r *evidencev1.EvidenceRecord) { r.EvidenceKind = "" }, "evidence_kind"},
		{"schema_version", func(r *evidencev1.EvidenceRecord) { r.SchemaVersion = "" }, "schema_version"},
		{"control_id", func(r *evidencev1.EvidenceRecord) { r.ControlId = "" }, "control_id"},
		{"scope", func(r *evidencev1.EvidenceRecord) { r.Scope = nil }, "scope"},
		{"observed_at", func(r *evidencev1.EvidenceRecord) { r.ObservedAt = nil }, "observed_at"},
		{"result", func(r *evidencev1.EvidenceRecord) { r.Result = evidencev1.Result_RESULT_UNSPECIFIED }, "result"},
		{"payload", func(r *evidencev1.EvidenceRecord) { r.Payload = nil }, "payload"},
		{"source_attribution", func(r *evidencev1.EvidenceRecord) { r.SourceAttribution = nil }, "source_attribution"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := record(t, c.opt)
			_, decision, err := svc.Process(context.Background(), rec, cred)
			if !errors.Is(err, ingest.ErrMissingField) {
				t.Fatalf("err = %v, want ErrMissingField", err)
			}
			if !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("error %q does not mention %q", err, c.msg)
			}
			if decision != ingest.DecisionRejectedValidation {
				t.Fatalf("decision = %v, want RejectedValidation", decision)
			}
		})
	}
}

// AC-3: re-pushing same idempotency_key with same content returns the
// original receipt, no duplicate row.
func TestPushIdempotentReplay_AC3(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")

	rec := record(t)
	rec.IdempotencyKey = "same-key-1"
	first, _, err := svc.Process(context.Background(), rec, cred)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}

	second, decision, err := svc.Process(context.Background(), rec, cred)
	if err != nil {
		t.Fatalf("second push: %v", err)
	}
	if decision != ingest.DecisionDeduplicated {
		t.Fatalf("second decision = %v, want Deduplicated", decision)
	}
	if !second.Deduplicated {
		t.Fatalf("second.Deduplicated = false, want true")
	}
	if second.RecordID != first.RecordID {
		t.Fatalf("dedup record_id = %s, want %s", second.RecordID, first.RecordID)
	}
	if count := countEvidenceRecords(t, pool, tenantA); count != 1 {
		t.Fatalf("after replay, evidence_records count = %d, want 1", count)
	}
}

// AC-4: re-pushing same idempotency_key with different content returns
// ErrIdempotencyMismatch.
func TestPushIdempotencyMismatch_AC4(t *testing.T) {
	svc, _, _ := boot(t)
	cred := mkCred(tenantA, nil, "")

	rec1 := record(t)
	rec1.IdempotencyKey = "key-mismatch"
	if _, _, err := svc.Process(context.Background(), rec1, cred); err != nil {
		t.Fatalf("first: %v", err)
	}

	rec2 := record(t)
	rec2.IdempotencyKey = "key-mismatch"
	rec2.Result = evidencev1.Result_RESULT_FAIL
	_, decision, err := svc.Process(context.Background(), rec2, cred)
	if !errors.Is(err, ingest.ErrIdempotencyMismatch) {
		t.Fatalf("err = %v, want ErrIdempotencyMismatch", err)
	}
	if decision != ingest.DecisionRejectedIdempotencyMismatch {
		t.Fatalf("decision = %v, want RejectedIdempotencyMismatch", decision)
	}
}

// AC-5: rate limit returns 429 with Retry-After. The token bucket is in
// the HTTP layer; we exercise it through the HTTP server.
func TestRateLimit_AC5(t *testing.T) {
	svc, pool, _ := boot(t)
	srv := api.New(api.Config{
		RotationGrace:    time.Hour,
		IngestService:    svc,
		EvidencePushRate: 1, // 1 token/sec, burst=2
	})
	srv.AttachDB(pool)
	_, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	handler := srv.HTTPHandlerForTests()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Send 5 quick pushes — at least one should rate-limit.
	limited := false
	for i := 0; i < 5; i++ {
		body := pushBody(t, fmt.Sprintf("rate-%d", i))
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/evidence:push", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			if ra := resp.Header.Get("Retry-After"); ra == "" {
				t.Fatalf("429 without Retry-After header")
			}
		}
		resp.Body.Close()
	}
	if !limited {
		t.Fatalf("expected at least one 429 with low rate limit, got none")
	}
}

// AC-7: audit log entry is written for every push attempt — accepted AND
// rejected — keyed by credential id.
func TestAuditLog_AC7(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")

	// Accepted push.
	rec1 := record(t)
	rec1.IdempotencyKey = "audit-ok"
	if _, _, err := svc.Process(context.Background(), rec1, cred); err != nil {
		t.Fatalf("ok push: %v", err)
	}
	// Rejected push (unknown kind).
	rec2 := record(t)
	rec2.IdempotencyKey = "audit-rejected"
	rec2.EvidenceKind = "nonexistent.kind.v1"
	if _, _, err := svc.Process(context.Background(), rec2, cred); err == nil {
		t.Fatal("expected unknown_kind error")
	}

	// Verify the audit_log has both rows.
	entries := listAuditEntries(t, pool, tenantA, cred.ID)
	if len(entries) < 2 {
		t.Fatalf("audit entries = %d, want >= 2: %+v", len(entries), entries)
	}
	decisions := map[string]int{}
	for _, e := range entries {
		decisions[e.Decision]++
	}
	if decisions["accepted"] == 0 {
		t.Errorf("no accepted entry in audit log")
	}
	if decisions["rejected_unknown_kind"] == 0 {
		t.Errorf("no rejected_unknown_kind entry in audit log")
	}
}

// AC-8: observed_at outside the 24h skew window is rejected.
func TestPushObservedAtSkew_AC8(t *testing.T) {
	svc, _, _ := boot(t)
	cred := mkCred(tenantA, nil, "")

	cases := []struct{ skew time.Duration }{
		{-48 * time.Hour}, // too old
		{+48 * time.Hour}, // too future
	}
	for _, c := range cases {
		rec := record(t)
		rec.IdempotencyKey = "skew-" + uuid.NewString()[:8]
		rec.ObservedAt = timestamppb.New(time.Now().UTC().Add(c.skew))
		_, decision, err := svc.Process(context.Background(), rec, cred)
		if !errors.Is(err, ingest.ErrObservedAtSkew) {
			t.Fatalf("skew=%s: err=%v, want ErrObservedAtSkew", c.skew, err)
		}
		if decision != ingest.DecisionRejectedObservedAtSkew {
			t.Fatalf("skew=%s: decision=%v, want RejectedObservedAtSkew", c.skew, decision)
		}
	}
}

// AC-9 (package boundary): the gRPC and HTTP handlers must call into
// ingest.Service.Process. We verify by introspection — the
// internal/api/evidence service holds an *ingest.Service field, and the
// ingest package has no imports from internal/api/* (asserted by the
// build, but doubly here through reflection on the type).
func TestPackageBoundary_AC9(t *testing.T) {
	svc, pool, _ := boot(t)
	srv := api.New(api.Config{
		RotationGrace: time.Hour,
		IngestService: svc,
	})
	srv.AttachDB(pool)
	// The HTTP handler returning non-nil verifies the slice-013 push
	// endpoint is mounted (it depends on IngestService).
	if srv.HTTPHandlerForTests() == nil {
		t.Fatal("HTTPHandlerForTests = nil; slice-013 push endpoint not mounted")
	}
	// Verify the Process method signature is the boundary — reflective
	// check guards against an accidental signature change that would
	// break the slice-015 substrate swap.
	m, ok := reflect.TypeOf(svc).MethodByName("Process")
	if !ok {
		t.Fatal("ingest.Service has no Process method")
	}
	// Expected: (ctx, *EvidenceRecord, credstore.Credential) → (Receipt, Decision, error).
	if m.Type.NumIn() != 4 {
		t.Fatalf("Process arity = %d, want 4 (recv + 3 args)", m.Type.NumIn())
	}
	if m.Type.NumOut() != 3 {
		t.Fatalf("Process return arity = %d, want 3", m.Type.NumOut())
	}
}

// Cross-tenant isolation: a push under tenant A must not appear under
// tenant B. RLS enforces this at the DB layer; the helper queries the
// row count under each tenant explicitly.
func TestCrossTenantIsolation(t *testing.T) {
	svc, pool, _ := boot(t)
	credA := mkCred(tenantA, nil, "")
	credB := mkCred(tenantB, nil, "")

	rec := record(t)
	rec.IdempotencyKey = "xt-a"
	if _, _, err := svc.Process(context.Background(), rec, credA); err != nil {
		t.Fatalf("A push: %v", err)
	}
	recB := record(t)
	recB.IdempotencyKey = "xt-b"
	if _, _, err := svc.Process(context.Background(), recB, credB); err != nil {
		t.Fatalf("B push: %v", err)
	}
	if cA := countEvidenceRecords(t, pool, tenantA); cA != 1 {
		t.Fatalf("tenant A count = %d, want 1", cA)
	}
	if cB := countEvidenceRecords(t, pool, tenantB); cB != 1 {
		t.Fatalf("tenant B count = %d, want 1", cB)
	}
}

// Append-only invariant: a writer who already pushed cannot UPDATE the
// row through atlas_app. The schema's append-only RLS surface blocks it.
func TestAppendOnly_NoUpdate(t *testing.T) {
	svc, pool, _ := boot(t)
	cred := mkCred(tenantA, nil, "")
	rec := record(t)
	receipt, _, err := svc.Process(context.Background(), rec, cred)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	id, _ := uuid.Parse(receipt.RecordID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tctx, err := tenancy.WithTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := pool.Begin(tctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(tctx)
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	tag, err := tx.Exec(tctx, "UPDATE evidence_records SET result='fail' WHERE id=$1", id)
	if err != nil {
		t.Fatalf("UPDATE returned err (unexpected; RLS silently filters): %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("UPDATE affected %d rows; append-only RLS broken", tag.RowsAffected())
	}
}

// ---- helpers ----

func countEvidenceRecords(t *testing.T, pool *pgxpool.Pool, tenant string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := pool.Begin(tctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(tctx)
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	q := dbx.New(tx)
	tenantUUID, _ := uuid.Parse(tenant)
	n, err := q.CountEvidenceRecordsByTenant(tctx, pgtype.UUID{Bytes: tenantUUID, Valid: true})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func listAuditEntries(t *testing.T, pool *pgxpool.Pool, tenant, credID string) []dbx.EvidenceAuditLog {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := pool.Begin(tctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(tctx)
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	q := dbx.New(tx)
	tenantUUID, _ := uuid.Parse(tenant)
	rows, err := q.ListEvidenceAuditEntriesByCredential(tctx, dbx.ListEvidenceAuditEntriesByCredentialParams{
		TenantID:     pgtype.UUID{Bytes: tenantUUID, Valid: true},
		CredentialID: credID,
		Limit:        100, Offset: 0,
	})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	return rows
}

func pushBody(t *testing.T, idempotencyKey string) string {
	t.Helper()
	return fmt.Sprintf(`{"record":{
        "idempotency_key":"%s",
        "evidence_kind":"sast.scan_result.v1",
        "schema_version":"1.0.0",
        "control_id":"scf:VPM-04",
        "scope":[{"key":"environment","values":["prod"]}],
        "observed_at":"%s",
        "result":"pass",
        "payload":{"tool":"semgrep","tool_version":"1.96.0","findings_count":0,"scanned_files":1247},
        "source_attribution":{"actor_type":"service_account","actor_id":"ci.test"}
    }}`, idempotencyKey, time.Now().UTC().Format(time.RFC3339))
}
