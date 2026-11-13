//go:build integration

// #1751: the MCP evidence appliers write through ingest.Service.Process,
// not a raw INSERT into evidence_records. These tests drive the real
// confirm flow against Postgres with the real platform schema registry
// and assert the MCP path is refused and audited the same way as the
// same record pushed straight into Process (the REST/gRPC path).

package writeproposals_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type auditRow struct {
	CredentialID string
	Decision     string
	ReasonCode   string
	EvidenceKind string
}

// ingestHarness boots the production wiring: a DB-backed schema registry
// seeded with the bundled platform schemas, an ingest.Service on the app
// role, and the default appliers registered against it.
func ingestHarness(t *testing.T) (*pgxpool.Pool, *ingest.Service, *writeproposals.Store, string) {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reg := schemaregistry.NewService(admin)
	if _, _, err := reg.ImportPlatformSchemas(ctx, schemaregistry.PlatformSchemasFS()); err != nil {
		t.Fatalf("ImportPlatformSchemas: %v", err)
	}
	svc := ingest.New(app, reg)
	store := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(app), svc)
	tenant := dbtest.SeedTenant(t, admin,
		"evidence_audit_log", "evidence_records", "controls", "mcp_write_proposals")
	return admin, svc, store, tenant
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'MCP override target', 'GOV', 'manual_attested', $3)
	`, id, tenant, "legacy_"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

func approverCtx(t *testing.T, tenant string) (context.Context, credstore.Credential) {
	t.Helper()
	cred := credstore.Credential{
		ID:         "key_" + uuid.NewString()[:8],
		TenantID:   tenant,
		IsApprover: true,
	}
	return authctx.WithCredential(ctxFor(t, tenant), cred), cred
}

func auditRowsFor(t *testing.T, admin *pgxpool.Pool, tenant, idemKey string) []auditRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT credential_id, decision, reason_code, COALESCE(evidence_kind, '')
		FROM evidence_audit_log
		WHERE tenant_id = $1 AND idempotency_key = $2
		ORDER BY received_at
	`, tenant, idemKey)
	if err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.CredentialID, &r.Decision, &r.ReasonCode, &r.EvidenceKind); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func countLedger(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_records WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count evidence_records: %v", err)
	}
	return n
}

// An approved push_evidence proposal observed 30 days ago is refused at
// confirm with the ingest skew error, writes nothing to the ledger, stays
// ai_proposed, and leaves a rejected_observed_at_skew audit row under the
// approver's credential: the same decision a direct push of the same
// record gets.
func TestConfirmPushEvidence_BackdatedIsRefusedAndAuditedLikeAPI(t *testing.T) {
	admin, svc, store, tenant := ingestHarness(t)
	ctx, cred := approverCtx(t, tenant)
	controlID := seedControl(t, admin, tenant)
	observed := time.Now().UTC().Add(-30 * 24 * time.Hour)

	in, _ := json.Marshal(map[string]any{
		"control_id":  controlID.String(),
		"kind":        "manual.attestation.v1",
		"result":      "pass",
		"observed_at": observed.Format(time.RFC3339),
		"payload":     map[string]any{"attestor": "ops", "statement": "backdated"},
	})
	create := validCreate()
	create.ToolName = writeproposals.ToolPushEvidence
	create.ToolInput = in
	p, err := store.Create(ctx, create)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = store.Confirm(ctx, p.ID, cred.ID)
	if !errors.Is(err, ingest.ErrObservedAtSkew) {
		t.Fatalf("Confirm err = %v, want ingest.ErrObservedAtSkew", err)
	}
	if n := countLedger(t, admin, tenant); n != 0 {
		t.Fatalf("evidence_records = %d after refused confirm, want 0", n)
	}
	got, err := store.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != writeproposals.StateAIProposed {
		t.Fatalf("state = %q after refused confirm, want ai_proposed", got.State)
	}

	mcpAudit := auditRowsFor(t, admin, tenant, "mcp-proposal:"+p.ID.String())
	if len(mcpAudit) != 1 {
		t.Fatalf("MCP audit rows = %d, want 1: %+v", len(mcpAudit), mcpAudit)
	}
	if mcpAudit[0].Decision != "rejected_observed_at_skew" ||
		mcpAudit[0].CredentialID != cred.ID ||
		mcpAudit[0].EvidenceKind != "manual.attestation.v1" {
		t.Fatalf("MCP audit row = %+v, want rejected_observed_at_skew by %s for manual.attestation.v1", mcpAudit[0], cred.ID)
	}

	// The API path: the same record pushed straight into Process.
	payload, _ := structpb.NewStruct(map[string]any{"attestor": "ops", "statement": "backdated"})
	apiKey := "api-" + uuid.NewString()
	_, decision, err := svc.Process(ctx, &evidencev1.EvidenceRecord{
		IdempotencyKey:    apiKey,
		EvidenceKind:      "manual.attestation.v1",
		SchemaVersion:     "1.0.0",
		ControlId:         controlID.String(),
		Scope:             []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}},
		ObservedAt:        timestamppb.New(observed),
		Result:            evidencev1.Result_RESULT_PASS,
		Payload:           payload,
		SourceAttribution: &evidencev1.SourceAttribution{ActorType: "human", ActorId: "ops"},
	}, cred)
	if !errors.Is(err, ingest.ErrObservedAtSkew) || decision != ingest.DecisionRejectedObservedAtSkew {
		t.Fatalf("API Process = (%v, %v), want ErrObservedAtSkew / RejectedObservedAtSkew", decision, err)
	}
	apiAudit := auditRowsFor(t, admin, tenant, apiKey)
	if len(apiAudit) != 1 {
		t.Fatalf("API audit rows = %d, want 1", len(apiAudit))
	}
	if apiAudit[0].Decision != mcpAudit[0].Decision || apiAudit[0].CredentialID != mcpAudit[0].CredentialID {
		t.Fatalf("API audit %+v and MCP audit %+v disagree", apiAudit[0], mcpAudit[0])
	}
	if !strings.HasPrefix(mcpAudit[0].ReasonCode, "observed_at skew") ||
		!strings.HasPrefix(apiAudit[0].ReasonCode, "observed_at skew") {
		t.Fatalf("reason codes = %q / %q, want observed_at skew", mcpAudit[0].ReasonCode, apiAudit[0].ReasonCode)
	}
}

// An approved update_control_state proposal lands one ledger row through
// ingest (canonical hash, ingest provenance, accepted audit row), and a
// retried apply of the same proposal deduplicates instead of writing a
// second row.
func TestConfirmUpdateControlState_IngestsThroughProcess(t *testing.T) {
	admin, _, store, tenant := ingestHarness(t)
	ctx, cred := approverCtx(t, tenant)
	controlID := seedControl(t, admin, tenant)

	in, _ := json.Marshal(map[string]any{
		"control_id": controlID.String(),
		"state":      "fail",
		"rationale":  "MFA disabled for break-glass account",
	})
	create := validCreate()
	create.ToolName = writeproposals.ToolUpdateControlState
	create.ToolInput = in
	p, err := store.Create(ctx, create)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	confirmed, err := store.Confirm(ctx, p.ID, cred.ID)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.AppliedSubject == nil {
		t.Fatal("applied_subject is nil, want the ingested record id")
	}

	var (
		kind, path, hash, provCred string
		result                     string
	)
	if err := admin.QueryRow(context.Background(), `
		SELECT evidence_kind, ingestion_path, hash, provenance->>'credential_id', result::text
		FROM evidence_records WHERE tenant_id = $1 AND id = $2
	`, tenant, *confirmed.AppliedSubject).Scan(&kind, &path, &hash, &provCred, &result); err != nil {
		t.Fatalf("load ingested record: %v", err)
	}
	if kind != writeproposals.ControlStateOverrideKind || path != "manual_upload" ||
		provCred != cred.ID || result != "fail" || strings.HasPrefix(hash, "mcp-") {
		t.Fatalf("record = kind %q path %q cred %q result %q hash %q; want ingest-written override", kind, path, provCred, result, hash)
	}
	audit := auditRowsFor(t, admin, tenant, "mcp-proposal:"+p.ID.String())
	if len(audit) != 1 || audit[0].Decision != "accepted" {
		t.Fatalf("audit rows = %+v, want one accepted", audit)
	}

	// Re-running the applier for the same proposal (a confirm retried after
	// its state flip rolled back) returns the first record instead of
	// writing, or tripping an idempotency mismatch on, a second one.
	app := dbtest.NewAppPool(t)
	applier := writeproposals.NewUpdateControlStateApplier(
		ingest.New(app, mustRegistry(t, admin)).WithPath("manual_upload"))
	var again string
	if err := pgx.BeginTxFunc(ctx, app, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return err
		}
		var aerr error
		again, aerr = applier(ctx, tx, p)
		return aerr
	}); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if again != *confirmed.AppliedSubject {
		t.Fatalf("re-apply record = %s, want dedup to %s", again, *confirmed.AppliedSubject)
	}
	if n := countLedger(t, admin, tenant); n != 1 {
		t.Fatalf("evidence_records = %d after re-apply, want 1", n)
	}
}

func mustRegistry(t *testing.T, admin *pgxpool.Pool) *schemaregistry.Service {
	t.Helper()
	reg := schemaregistry.NewService(admin)
	if err := reg.LoadFromDB(context.Background()); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	return reg
}
