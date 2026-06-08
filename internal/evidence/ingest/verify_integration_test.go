//go:build integration

// Slice 464 — AC-3 integration test for the `atlas evidence verify`
// ledger-integrity walk. Proves:
//
//   1. A correctly-baselined ledger reports ZERO mismatches.
//   2. A deliberately-corrupted record (its `payload` column mutated in
//      place, outside the ingest path) is REPORTED by the verify walk.
//
// The verify contract is "the stored `hash` equals the canonical hash of
// the record AS RECONSTRUCTABLE FROM THE LEDGER" (slice 464 decisions log
// D1: the original wire `scope` is not persisted, so the ingest
// scope-inclusive hash is not reproducible from a ledger row; the verify
// re-derives over the persisted columns). This test establishes the
// baseline by writing the ledger-reconstructable hash into the `hash`
// column for the seeded record, then corrupts the payload and asserts
// detection. Both the baseline write and the corruption use the admin
// (BYPASSRLS) pool, simulating an out-of-band mutation of the append-only
// table.

package ingest_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// TestEvidenceVerify_ProductionRecordValidates_474 is the load-bearing
// slice-474 proof (AC-1 + AC-2). A record pushed through the REAL ingest
// `Process` — with NO baseline-stamp workaround — verifies clean, because
// ingest now persists the canonical wire scope (`scope_canonical`) the hash
// was computed over and the verify walk reconstructs it. Then corrupting a
// hash-contributing column is still detected (tamper-evidence preserved).
func TestEvidenceVerify_ProductionRecordValidates_474(t *testing.T) {
	svc, _, _ := boot(t)
	adminPool := openPool(t, envOrSkip(t, "DATABASE_URL"))
	defer adminPool.Close()
	cred := mkCred(tenantA, nil, "")

	// Push a real, scope-bearing record through ingest.
	receipt, decision, err := svc.Process(context.Background(), record(t), cred)
	if err != nil {
		t.Fatalf("Process: %v (decision=%s)", err, decision)
	}

	// AC-1: the freshly-ingested record verifies clean against its OWN
	// stored hash — no baseline stamp. This is the production-record case
	// slice 464 could not validate.
	row := getEvidenceRowByID(t, adminPool, receipt.RecordID)
	if len(row.ScopeCanonical) == 0 || string(row.ScopeCanonical) == "null" {
		t.Fatalf("scope_canonical not persisted on production record: %q", string(row.ScopeCanonical))
	}
	ok, recomputed, err := ingest.VerifyLedgerRow(row)
	if err != nil {
		t.Fatalf("VerifyLedgerRow (production clean): %v", err)
	}
	if !ok {
		t.Fatalf("production record did NOT verify against its ingest hash: stored=%s recomputed=%s", row.Hash, recomputed)
	}
	if recomputed != receipt.Hash {
		t.Fatalf("verify recomputed hash != ingest receipt hash: receipt=%s recomputed=%s", receipt.Hash, recomputed)
	}
	t.Logf("AC-1: production record verifies clean — hash=%s", short12(row.Hash))

	// AC-2 (tamper still detected): mutate the payload column out-of-band.
	corrupt := []byte(`{"tool":"semgrep-TAMPERED","tool_version":"1.96.0","ruleset":"p/owasp-top-ten","findings_count":999,"scanned_files":1247}`)
	updateEvidenceJSONB(t, adminPool, receipt.RecordID, "payload", corrupt)
	if ok, recomputed, err = ingest.VerifyLedgerRow(getEvidenceRowByID(t, adminPool, receipt.RecordID)); err != nil {
		t.Fatalf("VerifyLedgerRow (payload corrupt): %v", err)
	} else if ok {
		t.Fatalf("payload corruption NOT detected on production record")
	}
	t.Logf("AC-2 (payload): corruption detected — recomputed=%s", short12(recomputed))

	// AC-2 (scope tamper): a fresh record, then corrupt the scope_canonical
	// column. The scope is now inside the per-record hash envelope, so
	// tampering with it must be detected — this is the tamper-evidence the
	// scope-free option (rejected) would have dropped.
	receipt2, _, err := svc.Process(context.Background(), record(t), cred)
	if err != nil {
		t.Fatalf("Process (scope-tamper case): %v", err)
	}
	tampered := []byte(`[{"key":"environment","values":["TAMPERED"]}]`)
	updateEvidenceJSONB(t, adminPool, receipt2.RecordID, "scope_canonical", tampered)
	if ok, recomputed, err = ingest.VerifyLedgerRow(getEvidenceRowByID(t, adminPool, receipt2.RecordID)); err != nil {
		t.Fatalf("VerifyLedgerRow (scope corrupt): %v", err)
	} else if ok {
		t.Fatalf("scope_canonical tamper NOT detected — scope is not covered by the hash")
	}
	t.Logf("AC-2 (scope): tamper detected — recomputed=%s", short12(recomputed))
}

func TestEvidenceVerify_DetectsCorruption_AC3(t *testing.T) {
	svc, appPool, _ := boot(t)
	adminPool := openPool(t, envOrSkip(t, "DATABASE_URL"))
	defer adminPool.Close()
	cred := mkCred(tenantA, nil, "")

	// Push one clean record through the real ingest path.
	receipt, decision, err := svc.Process(context.Background(), record(t), cred)
	if err != nil {
		t.Fatalf("Process: %v (decision=%s)", err, decision)
	}
	if receipt.RecordID == "" {
		t.Fatalf("empty record id")
	}

	// Read the persisted row back (admin pool, BYPASSRLS).
	row := getEvidenceRowByID(t, adminPool, receipt.RecordID)

	// Establish the verify baseline: stamp the ledger-reconstructable hash
	// into the `hash` column so a clean walk reports zero mismatches.
	baseline, err := ingest.LedgerRowHash(row)
	if err != nil {
		t.Fatalf("LedgerRowHash: %v", err)
	}
	updateEvidenceText(t, adminPool, receipt.RecordID, "hash", baseline)
	row = getEvidenceRowByID(t, adminPool, receipt.RecordID)

	// (1) Clean ledger → zero mismatches.
	ok, recomputed, err := ingest.VerifyLedgerRow(row)
	if err != nil {
		t.Fatalf("VerifyLedgerRow (clean): %v", err)
	}
	if !ok {
		t.Fatalf("clean record reported as mismatch: stored=%s recomputed=%s", row.Hash, recomputed)
	}

	// The verify CLI walks under atlas_app + RLS; assert that role sees
	// exactly the one record for the tenant.
	if got := countEvidenceRecords(t, appPool, tenantA); got != 1 {
		t.Fatalf("expected 1 evidence record, got %d", got)
	}

	// (2) Deliberately corrupt the record: mutate the payload column in
	// place (an out-of-band tamper). The stored `hash` is now stale.
	corrupt := []byte(`{"tool":"semgrep-TAMPERED","tool_version":"1.96.0","ruleset":"p/owasp-top-ten","findings_count":999,"scanned_files":1247}`)
	updateEvidenceJSONB(t, adminPool, receipt.RecordID, "payload", corrupt)
	corruptedRow := getEvidenceRowByID(t, adminPool, receipt.RecordID)

	ok, recomputed, err = ingest.VerifyLedgerRow(corruptedRow)
	if err != nil {
		t.Fatalf("VerifyLedgerRow (corrupt): %v", err)
	}
	if ok {
		t.Fatalf("corrupted record NOT detected: stored=%s recomputed=%s", corruptedRow.Hash, recomputed)
	}
	if recomputed == corruptedRow.Hash {
		t.Fatalf("recomputed hash equals stored hash on corrupt record (expected divergence)")
	}
	t.Logf("AC-3: corruption detected — stored=%s recomputed=%s", short12(corruptedRow.Hash), short12(recomputed))
}

// getEvidenceRowByID reads one ledger row by id via the admin (BYPASSRLS)
// pool. Mirrors the column order sqlc emits for evidence_records.
func getEvidenceRowByID(t *testing.T, pool *pgxpool.Pool, id string) dbx.EvidenceRecord {
	t.Helper()
	ctx := context.Background()
	var r dbx.EvidenceRecord
	err := pool.QueryRow(ctx,
		`SELECT id, tenant_id, evidence_query_id, control_id, scope_id,
		        observed_at, ingested_at, provenance, result, payload,
		        payload_uri, hash, freshness_class, valid_until, created_at,
		        idempotency_key, evidence_kind, schema_version, credential_id,
		        ingestion_path, source_attribution, control_ref, scope_canonical
		   FROM evidence_records WHERE id = $1`, id,
	).Scan(
		&r.ID, &r.TenantID, &r.EvidenceQueryID, &r.ControlID, &r.ScopeID,
		&r.ObservedAt, &r.IngestedAt, &r.Provenance, &r.Result, &r.Payload,
		&r.PayloadUri, &r.Hash, &r.FreshnessClass, &r.ValidUntil, &r.CreatedAt,
		&r.IdempotencyKey, &r.EvidenceKind, &r.SchemaVersion, &r.CredentialID,
		&r.IngestionPath, &r.SourceAttribution, &r.ControlRef, &r.ScopeCanonical,
	)
	if err != nil {
		t.Fatalf("getEvidenceRowByID(%s): %v", id, err)
	}
	return r
}

// updateEvidenceText mutates a text column on a ledger row out-of-band via
// the admin pool (the append-only RLS surface blocks atlas_app UPDATE; the
// migrate role can update — this simulates external tampering / a baseline
// stamp).
func updateEvidenceText(t *testing.T, pool *pgxpool.Pool, id, col, val string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE evidence_records SET "+col+" = $1 WHERE id = $2", val, id); err != nil {
		t.Fatalf("update %s: %v", col, err)
	}
}

// updateEvidenceJSONB mutates a jsonb column on a ledger row out-of-band.
func updateEvidenceJSONB(t *testing.T, pool *pgxpool.Pool, id, col string, val []byte) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE evidence_records SET "+col+" = $1::jsonb WHERE id = $2", string(val), id); err != nil {
		t.Fatalf("update %s: %v", col, err)
	}
}

func short12(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
