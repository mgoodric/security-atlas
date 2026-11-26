//go:build integration

// #1742: historical evidence import against a real Postgres with RLS. Each
// test pins one acceptance criterion of the issue.
//
// Required env: DATABASE_URL (migrate role, fixtures and cleanup) and
// DATABASE_URL_APP (atlas_app, which the importer and stores run as).

package historicalimport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/historicalimport"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
)

type fixture struct {
	admin    *pgxpool.Pool
	app      *pgxpool.Pool
	tenant   string
	ctrl     uuid.UUID
	fw       uuid.UUID
	svc      *ingest.Service
	importer *historicalimport.Importer
	periods  *period.Store
	audits   *audit.Store
	ctx      context.Context
}

func setup(t *testing.T) *fixture {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_period_freeze_revisions WHERE tenant_id = $1`,
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`UPDATE populations SET audit_period_id = NULL WHERE tenant_id = $1`,
			`DELETE FROM sample_audit_log WHERE tenant_id = $1`,
			`DELETE FROM sample_evidence WHERE tenant_id = $1`,
			`DELETE FROM samples WHERE tenant_id = $1`,
			`DELETE FROM populations WHERE tenant_id = $1`,
			`DELETE FROM evidence_audit_log WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
		if err := dbtest.DeleteAuditPeriods(ctx, admin, `tenant_id = $1`, tenant); err != nil {
			t.Logf("cleanup audit_periods: %v", err)
		}
	})

	fwID, verID := uuid.New(), uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, '#1742 test framework', $2, 'test')`, fwID, "f1742-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')`, verID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	ctrl := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, '#1742 control', 'AAA', 'automated', 'test-bundle-1742')`, ctrl, tenant); err != nil {
		t.Fatalf("seed control: %v", err)
	}

	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	svc := ingest.New(app, reg)
	return &fixture{
		admin: admin, app: app, tenant: tenant, ctrl: ctrl, fw: verID,
		svc:      svc,
		importer: historicalimport.New(app, svc),
		periods:  period.NewStore(app),
		audits:   audit.NewStore(app),
		ctx:      dbtest.WithTenantCtx(t, tenant),
	}
}

func (f *fixture) adminCred() credstore.Credential {
	return credstore.Credential{ID: "key_admin_" + uuid.NewString()[:8], TenantID: f.tenant, IsAdmin: true}
}

// attestation is a manual.attestation 1.3.0 record for the fixture's
// control, observed at observed.
func (f *fixture) attestation(t *testing.T, observed time.Time) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, err := structpb.NewStruct(map[string]any{
		"attestor":         "ciso@example.com",
		"statement":        "Access reviews completed for FY25",
		"result":           "pass",
		"period_start":     "2025-01-01",
		"period_end":       "2025-12-31",
		"report_reference": "FY25 SOC 2 Type II report",
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: "import-" + uuid.NewString(),
		EvidenceKind:   "manual.attestation.v1",
		SchemaVersion:  "1.3.0",
		ControlId:      f.ctrl.String(),
		Scope:          []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}},
		ObservedAt:     timestamppb.New(observed),
		Result:         evidencev1.Result_RESULT_PASS,
		Payload:        payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "human",
			ActorId:   "ciso@example.com",
		},
	}
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.admin.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f *fixture) evidenceCount(t *testing.T) int {
	return f.count(t, `SELECT count(*) FROM evidence_records WHERE tenant_id = $1`, f.tenant)
}

// frozenPeriod creates a period over [start, end] and freezes it at frozenAt.
func (f *fixture) frozenPeriod(t *testing.T, name string, start, end, frozenAt time.Time) period.Period {
	t.Helper()
	p, err := f.periods.Create(f.ctx, period.CreateInput{
		Name: name, FrameworkVersionID: f.fw, PeriodStart: start, PeriodEnd: end, CreatedBy: "key_test_1742",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	p, err = f.periods.Freeze(f.ctx, p.ID, "key_test_1742", frozenAt)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	return p
}

// AC: a non-admin credential calling the import path gets refused, and
// nothing is written (no evidence, no audit row).
func TestImport_NonAdminRefusedWritesNothing(t *testing.T) {
	f := setup(t)
	nonAdmin := credstore.Credential{ID: "key_conn_" + uuid.NewString()[:8], TenantID: f.tenant}
	system := ingest.SystemCredential("system:test", f.tenant)
	system.IsAdmin = true
	for _, cred := range []credstore.Credential{nonAdmin, system} {
		_, err := f.importer.Import(context.Background(), cred, historicalimport.Request{
			Source:  "FY25 report",
			Records: []*evidencev1.EvidenceRecord{f.attestation(t, time.Now().Add(-400*24*time.Hour))},
		})
		if !errors.Is(err, historicalimport.ErrNotAdmin) {
			t.Fatalf("cred %s: err = %v, want ErrNotAdmin", cred.ID, err)
		}
	}
	if n := f.evidenceCount(t); n != 0 {
		t.Fatalf("evidence rows = %d, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM evidence_audit_log WHERE tenant_id = $1`, f.tenant); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
}

// AC: an admin import of records dated 400 days ago succeeds, with
// ingestion_path=import, batch provenance and an audit-log row per record.
// ingested_at is the real write time.
func TestImport_AdminBackdated400DaysAccepted(t *testing.T) {
	f := setup(t)
	cred := f.adminCred()
	before := time.Now().Add(-time.Minute)
	observed := time.Now().Add(-400 * 24 * time.Hour).UTC().Truncate(time.Second)
	recs := []*evidencev1.EvidenceRecord{f.attestation(t, observed), f.attestation(t, observed.Add(time.Hour))}

	res, err := f.importer.Import(context.Background(), cred, historicalimport.Request{Source: "FY25 SOC 2 Type II report", Records: recs})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Receipts) != 2 {
		t.Fatalf("receipts = %d, want 2", len(res.Receipts))
	}
	rows, err := f.admin.Query(context.Background(), `
		SELECT ingestion_path, provenance, observed_at, ingested_at
		FROM evidence_records WHERE tenant_id = $1`, f.tenant)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		var path string
		var prov []byte
		var obs, ing time.Time
		if err := rows.Scan(&path, &prov, &obs, &ing); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if path != "import" {
			t.Errorf("ingestion_path = %q, want import", path)
		}
		var p map[string]any
		if err := json.Unmarshal(prov, &p); err != nil {
			t.Fatalf("provenance: %v", err)
		}
		if p["import_batch_id"] != res.BatchID.String() || p["import_source"] != "FY25 SOC 2 Type II report" || p["importer"] != cred.ID || p["ingestion_path"] != "import" {
			t.Errorf("provenance = %s", prov)
		}
		if obs.After(time.Now().Add(-399 * 24 * time.Hour)) {
			t.Errorf("observed_at = %s, want the real date 400 days ago", obs)
		}
		if ing.Before(before) {
			t.Errorf("ingested_at = %s, backdated; want the real write time", ing)
		}
	}
	if n != 2 {
		t.Fatalf("evidence rows = %d, want 2", n)
	}
	if got := f.count(t, `SELECT count(*) FROM evidence_audit_log
		WHERE tenant_id = $1 AND import_batch_id = $2 AND decision = 'accepted' AND record_id IS NOT NULL`,
		f.tenant, res.BatchID); got != 2 {
		t.Fatalf("accepted audit rows for the batch = %d, want 2", got)
	}
}

// AC: the same backdated record through the push path (which every
// transport, REST, gRPC, the stream consumer and attest, runs through
// ingest.Service.Process / ProcessInTx on a non-import Service) is still
// rejected with the observed_at skew error.
func TestImport_LivePathsKeepSkewCheck(t *testing.T) {
	f := setup(t)
	rec := f.attestation(t, time.Now().Add(-400*24*time.Hour))
	cred := credstore.Credential{ID: "key_" + uuid.NewString()[:8], TenantID: f.tenant, IsAdmin: true}
	_, decision, err := f.svc.Process(context.Background(), rec, cred)
	if !errors.Is(err, ingest.ErrObservedAtSkew) || decision != ingest.DecisionRejectedObservedAtSkew {
		t.Fatalf("push: decision=%s err=%v, want observed_at skew", decision, err)
	}
	for _, path := range []string{"push", "pull", "subscribe", "webhook", ingest.DomainWriterPath} {
		_, _, err := f.svc.WithPath(path).Process(context.Background(), f.attestation(t, time.Now().Add(-400*24*time.Hour)), cred)
		if !errors.Is(err, ingest.ErrObservedAtSkew) {
			t.Fatalf("path %s: err = %v, want ErrObservedAtSkew", path, err)
		}
	}
	if n := f.evidenceCount(t); n != 0 {
		t.Fatalf("evidence rows = %d, want 0", n)
	}
}

// AC: imported records more than 24h in the future are rejected, and the
// batch writes nothing.
func TestImport_FutureSkewRejected(t *testing.T) {
	f := setup(t)
	recs := []*evidencev1.EvidenceRecord{
		f.attestation(t, time.Now().Add(-30*24*time.Hour)),
		f.attestation(t, time.Now().Add(25*time.Hour)),
	}
	_, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{Source: "s", Records: recs})
	var rerr *historicalimport.RecordError
	if !errors.As(err, &rerr) || rerr.Index != 1 || !errors.Is(err, ingest.ErrObservedAtSkew) {
		t.Fatalf("err = %v, want record[1] observed_at skew", err)
	}
	if n := f.evidenceCount(t); n != 0 {
		t.Fatalf("evidence rows = %d, want 0 (the batch rolls back whole)", n)
	}
}

// AC: an import touching a frozen period without amend_frozen fails
// atomically and names the period. Every record is audited as rejected
// under the batch.
func TestImport_FrozenPeriodRefusedAtomically(t *testing.T) {
	f := setup(t)
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	p := f.frozenPeriod(t, "FY25 SOC 2", start, end, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))

	recs := []*evidencev1.EvidenceRecord{
		f.attestation(t, time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)), // in the frozen window
		f.attestation(t, time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)), // before it
	}
	_, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{Source: "s", Records: recs})
	var fpe *historicalimport.FrozenPeriodError
	if !errors.As(err, &fpe) {
		t.Fatalf("err = %v, want FrozenPeriodError", err)
	}
	if len(fpe.Hits) != 1 || fpe.Hits[0].PeriodID != p.ID || fpe.Hits[0].Records != 1 || fpe.Hits[0].Name != "FY25 SOC 2" {
		t.Fatalf("hits = %+v, want the FY25 period with 1 record", fpe.Hits)
	}
	if n := f.evidenceCount(t); n != 0 {
		t.Fatalf("evidence rows = %d, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM evidence_audit_log
		WHERE tenant_id = $1 AND decision = 'rejected_validation' AND import_batch_id IS NOT NULL`, f.tenant); n != 2 {
		t.Fatalf("rejected audit rows = %d, want 2", n)
	}
	got, err := f.periods.Get(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FreezeRevision != 1 || got.AmendedByImport() {
		t.Fatalf("period revision = %d, want 1 and not amended", got.FreezeRevision)
	}
}

// AC: a frozen period hit by the import but not named in amend_frozen
// still fails the whole import, even when another hit period is named.
func TestImport_UnnamedFrozenPeriodFailsWhole(t *testing.T) {
	f := setup(t)
	named := f.frozenPeriod(t, "H1", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC), time.Date(2025, 7, 15, 0, 0, 0, 0, time.UTC))
	unnamed := f.frozenPeriod(t, "H2", time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	recs := []*evidencev1.EvidenceRecord{
		f.attestation(t, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)),
		f.attestation(t, time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)),
	}
	_, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: recs, AmendFrozen: []uuid.UUID{named.ID}, AmendReason: "H1 results",
	})
	var fpe *historicalimport.FrozenPeriodError
	if !errors.As(err, &fpe) || len(fpe.Hits) != 1 || fpe.Hits[0].PeriodID != unnamed.ID {
		t.Fatalf("err = %v, want FrozenPeriodError naming only H2", err)
	}
	if n := f.evidenceCount(t); n != 0 {
		t.Fatalf("evidence rows = %d, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_period_freeze_revisions WHERE tenant_id = $1`, f.tenant); n != 0 {
		t.Fatalf("freeze revisions = %d, want 0", n)
	}
}

// AC: with amend_frozen naming the period, it gets a new freeze revision.
// The original hash is unchanged and still replays; the new hash covers the
// imported records; both are audit-logged; the period reads as amended by
// import; control state and a sample drawn from it use the latest revision.
func TestImport_AmendFrozenCutsRevision(t *testing.T) {
	f := setup(t)
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	frozenAt := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	// One record in the ledger before the freeze.
	pre := uuid.New()
	if _, err := f.admin.Exec(context.Background(), `
		INSERT INTO evidence_records (id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref)
		VALUES ($1, $2, $3, $4, $4, '{}'::jsonb, 'pass', '{}'::jsonb, 'hash-1742-pre', 'test')`,
		pre, f.tenant, f.ctrl, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed pre-freeze evidence: %v", err)
	}
	p, err := f.periods.Create(f.ctx, period.CreateInput{
		Name: "FY25", FrameworkVersionID: f.fw, PeriodStart: start, PeriodEnd: end, CreatedBy: "key_test_1742",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pop, err := f.audits.CreatePopulation(f.ctx, audit.CreatePopulationInput{
		ControlID: f.ctrl, TimeWindowStart: start, TimeWindowEnd: end.Add(24 * time.Hour), CreatedBy: "key_test_1742",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	if err := f.periods.AttachPopulation(f.ctx, p.ID, pop.ID, "key_test_1742"); err != nil {
		t.Fatalf("AttachPopulation: %v", err)
	}
	p, err = f.periods.Freeze(f.ctx, p.ID, "key_test_1742", frozenAt)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	original := append([]byte(nil), p.FrozenHash...)

	cred := f.adminCred()
	recs := []*evidencev1.EvidenceRecord{
		f.attestation(t, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)),
		f.attestation(t, time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)),
		f.attestation(t, time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)), // outside the window
	}
	res, err := f.importer.Import(context.Background(), cred, historicalimport.Request{
		Source: "FY25 report", Records: recs, AmendFrozen: []uuid.UUID{p.ID},
		AmendReason: "Auditor requested the FY25 Type II results in the period",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Amendments) != 1 || res.Amendments[0].PeriodID != p.ID {
		t.Fatalf("amendments = %+v, want one for the period", res.Amendments)
	}
	rev := res.Amendments[0].Revision
	if rev.Justification != "Auditor requested the FY25 Type II results in the period" {
		t.Fatalf("revision justification = %q", rev.Justification)
	}
	if rev.Revision != 2 || len(rev.EvidenceRecordIDs) != 2 || !bytes.Equal(rev.PreviousHash, original) || bytes.Equal(rev.FrozenHash, original) {
		t.Fatalf("revision = %d (+%d records), previous=%x new=%x original=%x", rev.Revision, len(rev.EvidenceRecordIDs), rev.PreviousHash, rev.FrozenHash, original)
	}

	got, err := f.periods.Get(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.FrozenHash, original) {
		t.Fatalf("original frozen_hash changed: %x, want %x", got.FrozenHash, original)
	}
	if !got.FrozenAt.Equal(frozenAt) {
		t.Fatalf("frozen_at = %s, want unchanged %s", got.FrozenAt, frozenAt)
	}
	if got.FreezeRevision != 2 || !got.AmendedByImport() || !bytes.Equal(got.RevisionHash, rev.FrozenHash) || len(got.Revisions) != 2 {
		t.Fatalf("period = revision %d amended=%v revisions=%d", got.FreezeRevision, got.AmendedByImport(), len(got.Revisions))
	}

	// Both revisions replay from the ledger.
	if h, err := f.periods.ReplayHash(f.ctx, p.ID, 1); err != nil || !bytes.Equal(h, original) {
		t.Fatalf("replay revision 1 = %x (%v), want original %x", h, err, original)
	}
	if h, err := f.periods.ReplayHash(f.ctx, p.ID, period.LatestRevision); err != nil || !bytes.Equal(h, rev.FrozenHash) {
		t.Fatalf("replay latest = %x (%v), want %x", h, err, rev.FrozenHash)
	}
	if _, err := f.periods.ReplayHash(f.ctx, p.ID, 3); !errors.Is(err, period.ErrNoRevision) {
		t.Fatalf("replay revision 3: err = %v, want ErrNoRevision", err)
	}

	// The period's audit log records the amendment with both hashes.
	var detail []byte
	if err := f.admin.QueryRow(context.Background(), `
		SELECT detail FROM audit_period_audit_log
		WHERE tenant_id = $1 AND audit_period_id = $2 AND action = 'period_amended_by_import'`,
		f.tenant, p.ID).Scan(&detail); err != nil {
		t.Fatalf("amend log row: %v", err)
	}
	var d map[string]any
	_ = json.Unmarshal(detail, &d)
	if d["reason"] != rev.Justification {
		t.Fatalf("amend log reason = %v", d["reason"])
	}
	if d["previous_hash"] != fmt.Sprintf("%x", original) || d["frozen_hash"] != fmt.Sprintf("%x", rev.FrozenHash) ||
		d["import_batch_id"] != res.BatchID.String() {
		t.Fatalf("amend log detail = %s", detail)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_period_audit_log
		WHERE tenant_id = $1 AND audit_period_id = $2 AND action = 'period_amended_by_import' AND actor = $3`,
		f.tenant, p.ID, cred.ID); n != 1 {
		t.Fatalf("amend log rows by the importer = %d, want 1", n)
	}

	// Control state reads the latest revision: the pre-freeze record plus
	// the two imported records in the window.
	obs, err := f.periods.ControlState(f.ctx, p.ID, f.ctrl)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(obs) != 3 {
		t.Fatalf("control state = %d observations, want 3", len(obs))
	}

	// A sample drawn from the frozen population draws from the latest
	// revision.
	s, err := f.audits.DrawSample(f.ctx, audit.DrawSampleInput{PopulationID: pop.ID, N: 10, Seed: "seed-1742", CreatedBy: "key_test_1742"})
	if err != nil {
		t.Fatalf("DrawSample: %v", err)
	}
	if len(s.EvidenceRecordIDs) != 3 {
		t.Fatalf("sample = %d records, want 3", len(s.EvidenceRecordIDs))
	}
	// The sample records the revision it drew from and its reason, and so
	// does its sample_drawn log row.
	if s.FreezeRevision == nil || *s.FreezeRevision != 2 || s.AmendReason != rev.Justification {
		t.Fatalf("sample revision = %v reason %q, want 2 and the amend reason", s.FreezeRevision, s.AmendReason)
	}
	if got, err := f.audits.GetSample(f.ctx, s.ID); err != nil || got.FreezeRevision == nil || *got.FreezeRevision != 2 || got.AmendReason != rev.Justification {
		t.Fatalf("GetSample: revision %v reason %q err %v", got.FreezeRevision, got.AmendReason, err)
	}
	if n := f.count(t, `SELECT count(*) FROM sample_audit_log
		WHERE tenant_id = $1 AND sample_id = $2 AND action = 'sample_drawn' AND freeze_revision = 2`, f.tenant, s.ID); n != 1 {
		t.Fatalf("sample_drawn log rows at revision 2 = %d, want 1", n)
	}

	// The frozen-period evidence summary reads the latest revision too, and
	// marks the amended records so their citations resolve.
	set, err := evidencesummary.NewPeriodStore(f.app).PeriodEvidenceSet(f.ctx, f.ctrl, p.ID)
	if err != nil {
		t.Fatalf("PeriodEvidenceSet: %v", err)
	}
	if set.TotalCount != 3 || len(set.Records) != 3 || len(set.Amended) != 2 {
		t.Fatalf("period evidence set: total=%d records=%d amended=%d, want 3/3/2", set.TotalCount, len(set.Records), len(set.Amended))
	}

	// An auditor assigned the period can read the records the revision
	// added: the authz hydrator places them in the period.
	for _, id := range rev.EvidenceRecordIDs {
		scope, ok, err := f.audits.EvidencePeriods(f.ctx, id)
		if err != nil || !ok {
			t.Fatalf("EvidencePeriods(%s): ok=%v err=%v", id, ok, err)
		}
		if len(scope.Periods) != 1 || scope.Periods[0] != p.ID {
			t.Fatalf("EvidencePeriods(%s) = %v, want the amended period", id, scope.Periods)
		}
	}
}

// Open periods: an imported record dated inside an open period joins it like
// any other evidence, and the period gains no revision.
func TestImport_OpenPeriodJoins(t *testing.T) {
	f := setup(t)
	p, err := f.periods.Create(f.ctx, period.CreateInput{
		Name: "FY25 open", FrameworkVersionID: f.fw,
		PeriodStart: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
		CreatedBy: "key_test_1742",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.importer.Import(context.Background(), f.adminCred(), historicalimport.Request{
		Source: "s", Records: []*evidencev1.EvidenceRecord{f.attestation(t, time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC))},
	}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	obs, err := f.periods.ControlState(f.ctx, p.ID, f.ctrl)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("open period control state = %d, want 1", len(obs))
	}
	// Freezing it afterwards captures the imported record in revision 1.
	frozen, err := f.periods.Freeze(f.ctx, p.ID, "key_test_1742", time.Time{})
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if frozen.FreezeRevision != 0 {
		// Freeze returns the row without revisions; Get stamps them.
		t.Logf("freeze revision on the Freeze return = %d", frozen.FreezeRevision)
	}
	got, err := f.periods.Get(f.ctx, p.ID)
	if err != nil || got.FreezeRevision != 1 || got.AmendedByImport() {
		t.Fatalf("Get after freeze: revision %d amended %v err %v", got.FreezeRevision, got.AmendedByImport(), err)
	}
	if obs, _ := f.periods.ControlState(f.ctx, p.ID, f.ctrl); len(obs) != 1 {
		t.Fatalf("frozen control state = %d, want 1", len(obs))
	}
}
