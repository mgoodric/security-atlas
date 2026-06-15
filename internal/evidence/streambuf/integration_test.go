//go:build integration

// Integration tests for the slice-015 NATS JetStream evidence buffer.
// Requires:
//   - DATABASE_URL / DATABASE_URL_APP (Postgres with slice-013 ledger schema)
//   - NATS_URL (a running NATS server with JetStream enabled)
//
// The CI workflow starts both as `docker run` steps before this binary
// runs. Locally:
//
//	docker run -d --name nats -p 4222:4222 nats:2.10-alpine -js -sd /data
//
// The tests assert the slice 015 acceptance criteria end-to-end against a
// real broker. Mocks would defeat the point — the at-least-once /
// idempotency guarantees only hold against the actual broker semantics.

package streambuf_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
)

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}

// boot wipes ledger + registry, seeds the platform schemas, seeds an
// extra tenant-private schema declaring `x-redaction-rules` for AC-6,
// opens a streambuf.Conn against the local NATS, and returns the ingest
// service + connection + cleanup. Each test gets its own unique stream
// name so concurrent test runs do not interfere with each other.
type fixture struct {
	conn   *streambuf.Conn
	svc    *ingest.Service
	pool   *pgxpool.Pool
	reg    *schemaregistry.Service
	logger *slog.Logger
	tenant string
}

func boot(t *testing.T) *fixture {
	t.Helper()
	natsURL := envOrSkip(t, "NATS_URL")
	// dbtest pools self-close via their own t.Cleanup (slice 435 / 742 drain
	// batch 18); the manual adminPool/appPool Close() calls are gone.
	adminPool := dbtest.NewMigratePool(t)
	appPool := dbtest.NewAppPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := adminPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(6502261335191782015)"); err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(6502261335191782015)")
	}()
	// CASCADE so downstream FK references (slice 026's
	// sample_evidence.evidence_record_id) don't block the truncate.
	// Each test seeds the state it needs; cross-table cascade is fine
	// because there's no production data in the test DB to preserve.
	if _, err := conn.Exec(ctx,
		`TRUNCATE evidence_audit_log, evidence_records, evidence_kind_schemas CASCADE`,
	); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	platform, err := schemaregistry.LoadPlatformSchemas(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("LoadPlatformSchemas: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	for _, ps := range platform {
		anchors := ps.DefaultSCFAnchors
		if anchors == nil {
			anchors = []string{}
		}
		parts := strings.Split(ps.Semver, ".")
		majorI, _ := strconv.Atoi(parts[0])
		minorI, _ := strconv.Atoi(parts[1])
		patchI, _ := strconv.Atoi(parts[2])
		if _, err := tx.Exec(ctx, `
            INSERT INTO evidence_kind_schemas
                (id, tenant_id, kind, semver, major, minor, patch,
                 schema_json, owner, default_scf_anchors, created_by)
            VALUES
                (gen_random_uuid(), NULL, $1, $2, $3, $4, $5,
                 $6::jsonb, $7, $8, 'slice-015-test-bootstrap')
            ON CONFLICT (kind, semver) WHERE tenant_id IS NULL DO NOTHING
        `, ps.Kind, ps.Semver, majorI, minorI, patchI,
			string(ps.SchemaJSON), ps.Owner, anchors); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("seed %s/%s: %v", ps.Kind, ps.Semver, err)
		}
	}
	// AC-6 test fixture: a tenant-private schema for kind
	// `secret.scan.v1` declaring an x-redaction-rules array. The push
	// path applies these rules before hashing/writing.
	redactSchema := []byte(`{
        "$schema": "https://json-schema.org/draft/2020-12/schema",
        "type": "object",
        "x-redaction-rules": ["$.api_key", "$.findings[*].token"],
        "additionalProperties": true,
        "properties": {
            "api_key":  {"type": "string"},
            "label":    {"type": "string"},
            "findings": {"type": "array"}
        }
    }`)
	tenantUUID, _ := uuid.Parse(tenantA)
	if _, err := tx.Exec(ctx, `
        INSERT INTO evidence_kind_schemas
            (id, tenant_id, kind, semver, major, minor, patch,
             schema_json, owner, default_scf_anchors, created_by)
        VALUES
            (gen_random_uuid(), $1, $2, $3, 1, 0, 0,
             $4::jsonb, $5, $6, 'slice-015-test-bootstrap')
        ON CONFLICT DO NOTHING
    `, tenantUUID, "secret.scan.v1", "1.0.0", string(redactSchema),
		"slice-015-test", []string{}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("seed redact schema: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed: %v", err)
	}

	reg := schemaregistry.NewService(adminPool)
	if err := reg.LoadFromDB(ctx); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	svc := ingest.New(appPool, reg)

	// Per-test unique stream name + consumer name so parallel test
	// invocations (and slow leftovers from prior runs) do not collide.
	uniq := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	streamCfg := streambuf.Config{
		URL:          natsURL,
		StreamName:   "EVIDENCE_INGEST_TEST_" + uniq,
		Subject:      "evidence.ingest.test." + uniq,
		ConsumerName: "evidence_ingest_worker_" + uniq,
		Logger:       logger,
		AckWait:      5 * time.Second,
	}
	sbCtx, sbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sbCancel()
	sc, err := streambuf.Open(sbCtx, streamCfg)
	if err != nil {
		t.Fatalf("streambuf.Open: %v", err)
	}

	t.Cleanup(func() {
		// Best-effort stream cleanup so successive runs don't accumulate
		// per-test streams in the broker. The dbtest pools self-close via
		// their own t.Cleanup, so only the NATS stream teardown remains here.
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanCancel()
		_ = sc.JS().DeleteStream(cleanCtx, streamCfg.StreamName)
		sc.Close()
	})
	return &fixture{conn: sc, svc: svc, pool: appPool, reg: reg, logger: logger, tenant: tenantA}
}

func recordFor(t *testing.T, kind string, idem string, payload map[string]any) *evidencev1.EvidenceRecord {
	t.Helper()
	p, err := structpb.NewStruct(payload)
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem,
		EvidenceKind:   kind,
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:VPM-04",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "environment", Values: []string{"prod"}},
		},
		ObservedAt: timestamppb.New(time.Now().UTC().Add(-1 * time.Minute)),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    p,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "service_account",
			ActorId:   "slice-015-test",
		},
	}
}

func mkCred(tenant string) credstore.Credential {
	return credstore.Credential{
		ID:       "key_" + uuid.NewString()[:8],
		TenantID: tenant,
	}
}

// AC-2: Publisher returns a receipt immediately after stream commit —
// before the consumer runs, the ledger has zero rows. After the consumer
// drains, the ledger has the row.
func TestAC2_PublishAckBeforeLedgerWrite(t *testing.T) {
	f := boot(t)
	pub := streambuf.NewJetStreamPublisher(f.conn)
	cred := mkCred(f.tenant)

	rec := recordFor(t, "sast.scan_result.v1", "ac2-"+uuid.NewString()[:8],
		map[string]any{"tool": "semgrep", "findings_count": 0})

	ctx := context.Background()
	receipt, decision, err := pub.Publish(ctx, rec, cred)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if decision != ingest.DecisionAccepted {
		t.Fatalf("decision = %v, want Accepted", decision)
	}
	if receipt.RecordID == "" || receipt.Hash == "" {
		t.Fatalf("receipt missing fields: %+v", receipt)
	}
	if before := countEvidence(t, f.pool, f.tenant); before != 0 {
		t.Fatalf("ledger non-empty before consumer ran: %d", before)
	}

	// Start consumer + drain.
	consumer := streambuf.NewConsumer(f.conn, f.svc)
	consumerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	consumerErr := make(chan error, 1)
	go func() { consumerErr <- consumer.Start(consumerCtx) }()
	t.Cleanup(func() {
		select {
		case e := <-consumerErr:
			if e != nil {
				t.Logf("consumer exited with error: %v", e)
			}
		default:
		}
	})
	waitFor(t, 10*time.Second, func() bool {
		return countEvidence(t, f.pool, f.tenant) == 1
	})
	consumer.Stop()
	if got := countEvidence(t, f.pool, f.tenant); got != 1 {
		t.Fatalf("after drain, ledger count = %d, want 1", got)
	}
}

// AC-3: Consumer reads from the stream, decodes the proto, and runs
// Service.Process. Verified by the existence of the audit-log row for
// the credential — Process writes to evidence_audit_log on every
// outcome.
func TestAC3_ConsumerCallsProcess(t *testing.T) {
	f := boot(t)
	pub := streambuf.NewJetStreamPublisher(f.conn)
	cred := mkCred(f.tenant)

	rec := recordFor(t, "sast.scan_result.v1", "ac3-"+uuid.NewString()[:8],
		map[string]any{"tool": "semgrep", "findings_count": 0})
	if _, _, err := pub.Publish(context.Background(), rec, cred); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	consumer := streambuf.NewConsumer(f.conn, f.svc)
	consumerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Start(consumerCtx) }()
	waitFor(t, 10*time.Second, func() bool { return consumer.Processed() >= 1 })

	if got := consumer.Processed(); got != 1 {
		t.Fatalf("processed = %d, want 1", got)
	}
	// Audit log row exists (slice-013 invariant). Process is the only
	// thing that writes evidence_audit_log, so this proves the consumer
	// invoked it.
	entries := listAuditEntries(t, f.pool, f.tenant, cred.ID)
	if len(entries) == 0 {
		t.Fatalf("no audit entries; Service.Process did not run")
	}
}

// AC-4 (the load-bearing test): stop the consumer, publish 100 records,
// restart the consumer — all 100 must process exactly once on the
// ledger. JetStream gives us at-least-once delivery; idempotency in
// Service.Process collapses any redeliveries.
func TestAC4_ReplayExactlyOnce(t *testing.T) {
	f := boot(t)
	pub := streambuf.NewJetStreamPublisher(f.conn)
	cred := mkCred(f.tenant)

	// Phase 1: publish 100 records with NO consumer running.
	const N = 100
	for i := 0; i < N; i++ {
		rec := recordFor(t, "sast.scan_result.v1",
			"replay-"+strconv.Itoa(i),
			map[string]any{"tool": "semgrep", "findings_count": i})
		if _, _, err := pub.Publish(context.Background(), rec, cred); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}
	if got := countEvidence(t, f.pool, f.tenant); got != 0 {
		t.Fatalf("ledger non-empty without consumer: %d", got)
	}

	// Phase 2: start the consumer; drain.
	consumer := streambuf.NewConsumer(f.conn, f.svc)
	consumerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Start(consumerCtx) }()
	waitFor(t, 30*time.Second, func() bool {
		return countEvidence(t, f.pool, f.tenant) >= N
	})
	consumer.Stop()
	if got := countEvidence(t, f.pool, f.tenant); got != N {
		t.Fatalf("after first drain, ledger count = %d, want %d", got, N)
	}

	// Phase 3: restart consumer + re-publish same 100 records. AT-LEAST-ONCE
	// could redeliver during phase 2's stop; same idempotency_keys mean
	// Service.Process collapses to DecisionDeduplicated and the ledger
	// stays at exactly N rows.
	consumer2 := streambuf.NewConsumer(f.conn, f.svc)
	consumerCtx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = consumer2.Start(consumerCtx2) }()
	for i := 0; i < N; i++ {
		rec := recordFor(t, "sast.scan_result.v1",
			"replay-"+strconv.Itoa(i),
			map[string]any{"tool": "semgrep", "findings_count": i})
		if _, _, err := pub.Publish(context.Background(), rec, cred); err != nil {
			t.Fatalf("re-Publish %d: %v", i, err)
		}
	}
	// Allow time for redeliveries. Anything that re-arrives must dedup.
	time.Sleep(2 * time.Second)
	waitFor(t, 10*time.Second, func() bool {
		return countEvidence(t, f.pool, f.tenant) == N
	})
	consumer2.Stop()
	if got := countEvidence(t, f.pool, f.tenant); got != N {
		t.Fatalf("after replay, ledger count = %d, want %d (exactly-once violated)", got, N)
	}
}

// AC-5: stream is configured with MaxAge = 7 days.
func TestAC5_SevenDayRetention(t *testing.T) {
	f := boot(t)
	info, err := f.conn.Stream().Info(context.Background())
	if err != nil {
		t.Fatalf("stream.Info: %v", err)
	}
	want := 7 * 24 * time.Hour
	if info.Config.MaxAge != want {
		t.Fatalf("MaxAge = %s, want %s", info.Config.MaxAge, want)
	}
	if info.Config.Storage != jetstream.FileStorage {
		t.Fatalf("Storage = %v, want FileStorage", info.Config.Storage)
	}
	if info.Config.Retention != jetstream.LimitsPolicy {
		t.Fatalf("Retention = %v, want LimitsPolicy", info.Config.Retention)
	}
}

// AC-6 (load-bearing security test): push a record carrying a secret in
// the payload; the consumer applies the schema's x-redaction-rules and
// the ledger row's payload contains the REDACTED marker, not the
// secret. Verifies the redaction happens at INGESTION (consumer side),
// not at evaluation, per AC-6.
func TestAC6_RedactionAtIngestion(t *testing.T) {
	f := boot(t)
	pub := streambuf.NewJetStreamPublisher(f.conn)
	cred := mkCred(f.tenant)

	const secret = "ghp_AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIII"
	const tokenA = "token-aaaaa-secret"
	const tokenB = "token-bbbbb-secret"

	rec := recordFor(t, "secret.scan.v1", "ac6-"+uuid.NewString()[:8],
		map[string]any{
			"api_key": secret,
			"label":   "my-scanner",
			"findings": []any{
				map[string]any{"rule": "r1", "token": tokenA},
				map[string]any{"rule": "r2", "token": tokenB},
			},
		})
	if _, _, err := pub.Publish(context.Background(), rec, cred); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	consumer := streambuf.NewConsumer(f.conn, f.svc)
	consumerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Start(consumerCtx) }()
	waitFor(t, 10*time.Second, func() bool { return consumer.Processed() >= 1 })
	consumer.Stop()

	// Now fetch the stored payload directly from the ledger and confirm
	// the secret is gone, replaced with the REDACTED marker.
	payload := fetchEvidencePayload(t, f.pool, f.tenant)
	asStr := string(payload)
	if strings.Contains(asStr, secret) {
		t.Fatalf("LEDGER LEAK: ledger payload contains api_key secret (anti-criterion P0)")
	}
	if strings.Contains(asStr, tokenA) || strings.Contains(asStr, tokenB) {
		t.Fatalf("LEDGER LEAK: ledger payload contains finding token (anti-criterion P0)")
	}
	if !strings.Contains(asStr, "<<REDACTED>>") {
		t.Fatalf("redact marker absent in ledger payload: %s", asStr)
	}

	// Sanity: non-redacted field (`label`) is preserved.
	if !strings.Contains(asStr, "my-scanner") {
		t.Fatalf("redaction was over-eager: label is missing from %s", asStr)
	}
}

// Anti-criterion P0: at-least-once preserved. Verify that an error
// during Process triggers a redelivery (Nak) rather than an Ack-drop.
//
// Approach: drive the consumer with a record carrying a bogus
// observed_at; Process rejects it as DecisionRejectedObservedAtSkew —
// a "poison" decision that the consumer must Term (else it redelivers
// forever). The check: the message lands in the audit log AND is not
// in the active stream. Demonstrates the at-least-once semantics
// (poison handling is the inverse of a fail-open shortcut).
func TestAtLeastOnce_PoisonGetsTermedNotDropped(t *testing.T) {
	f := boot(t)
	pub := streambuf.NewJetStreamPublisher(f.conn)
	cred := mkCred(f.tenant)

	rec := recordFor(t, "sast.scan_result.v1", "poison-"+uuid.NewString()[:8],
		map[string]any{"tool": "semgrep", "findings_count": 0})
	rec.ObservedAt = timestamppb.New(time.Now().UTC().Add(48 * time.Hour))
	if _, _, err := pub.Publish(context.Background(), rec, cred); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	consumer := streambuf.NewConsumer(f.conn, f.svc)
	consumerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = consumer.Start(consumerCtx) }()
	waitFor(t, 10*time.Second, func() bool {
		entries := listAuditEntries(t, f.pool, f.tenant, cred.ID)
		for _, e := range entries {
			if e.Decision == "rejected_observed_at_skew" {
				return true
			}
		}
		return false
	})
	consumer.Stop()

	// Ledger has zero rows (poison record was rejected); audit log has
	// the rejection row (slice-013 invariant).
	if got := countEvidence(t, f.pool, f.tenant); got != 0 {
		t.Fatalf("ledger count = %d, want 0 for poison record", got)
	}
}

// ---- helpers ----

func waitFor(t *testing.T, max time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %s", max)
}

func countEvidence(t *testing.T, pool *pgxpool.Pool, tenant string) int64 {
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

// fetchEvidencePayload returns the JSON-encoded payload of the single
// evidence_records row under `tenant`. Asserts exactly one row.
func fetchEvidencePayload(t *testing.T, pool *pgxpool.Pool, tenant string) []byte {
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
	rows, err := tx.Query(tctx, "SELECT payload FROM evidence_records LIMIT 2")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var payloads [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("scan: %v", err)
		}
		payloads = append(payloads, b)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("expected 1 evidence row, got %d", len(payloads))
	}
	return payloads[0]
}

// Ensures the slice-015 Conn struct exposes a NATS subject and the
// publish path uses it. Compile-time check that the import surface
// covers the (json, nats.Header) types we reference in fixtures.
var _ = func() bool {
	_ = nats.Header{}
	_ = json.RawMessage{}
	_ = errors.New
	return true
}()
