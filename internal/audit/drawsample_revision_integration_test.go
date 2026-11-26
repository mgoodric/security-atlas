//go:build integration

package audit_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// amendPeriod cuts the next freeze revision of periodID, adding recordID,
// in its own committed transaction.
func amendPeriod(t *testing.T, app *pgxpool.Pool, tenant string, periodID, recordID uuid.UUID) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	err = pgx.BeginTxFunc(ctx, app, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return err
		}
		q := dbx.New(tx)
		p, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true},
			ID:       pgtype.UUID{Bytes: periodID, Valid: true},
		})
		if err != nil {
			return err
		}
		_, err = period.AmendInTx(ctx, q, p, period.AmendInput{
			ImportBatchID: uuid.New(), Actor: "key_test_1742", Reason: "test amendment",
			EvidenceRecordIDs: []uuid.UUID{recordID},
		})
		return err
	})
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
}

// #1742 review N-A: a sample is drawn from exactly the freeze revision it
// records, even when an amendment commits while the draw runs.
func TestDrawSample_RevisionLabelMatchesPopulation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_period_freeze_revisions WHERE tenant_id = $1`,
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`UPDATE populations SET audit_period_id = NULL WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
		if err := dbtest.DeleteAuditPeriods(ctx, admin, `tenant_id = $1`, tenant); err != nil {
			t.Logf("cleanup audit_periods: %v", err)
		}
	})
	ctrl := seedControl(t, admin, tenant)
	windowStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = seedEvidence(t, admin, tenant, ctrl, 3, windowStart)

	fwID, verID := uuid.New(), uuid.New()
	if _, err := admin.Exec(context.Background(), `INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, 'na', $2, 'test')`, fwID, "na-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `INSERT INTO framework_versions (id, tenant_id, framework_id, version) VALUES ($1, NULL, $2, '1.0')`, verID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}

	ctx := dbtest.WithTenantCtx(t, tenant)
	periods := period.NewStore(app)
	store := audit.NewStore(app)
	p, err := periods.Create(ctx, period.CreateInput{
		Name: "N-A", FrameworkVersionID: verID, PeriodStart: windowStart, PeriodEnd: windowStart.Add(30 * 24 * time.Hour), CreatedBy: "key_test_1742",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pop, err := store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID: ctrl, TimeWindowStart: windowStart, TimeWindowEnd: windowStart.Add(30 * 24 * time.Hour), CreatedBy: "key_test_1742",
	})
	if err != nil {
		t.Fatalf("CreatePopulation: %v", err)
	}
	if err := periods.AttachPopulation(ctx, p.ID, pop.ID, "key_test_1742"); err != nil {
		t.Fatalf("AttachPopulation: %v", err)
	}
	if _, err := periods.Freeze(ctx, p.ID, "key_test_1742", windowStart.Add(10*24*time.Hour)); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	// Two records observed before the freeze but written after it, each
	// added by its own revision.
	late := func(i int) uuid.UUID {
		id := uuid.New()
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO evidence_records (id, tenant_id, control_id, observed_at, ingested_at,
				provenance, result, payload, hash, control_ref)
			VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, 'test')`,
			id, tenant, ctrl, windowStart.Add(time.Duration(5+i)*time.Hour), fmt.Sprintf("hash-na-%d", i)); err != nil {
			t.Fatalf("seed late evidence: %v", err)
		}
		return id
	}
	rev2Record, rev3Record := late(0), late(1)
	amendPeriod(t, app, tenant, p.ID, rev2Record)

	// Revision 3 commits after the draw has read revision 2.
	restore := audit.SetDrawSampleAfterRevision(func() { amendPeriod(t, app, tenant, p.ID, rev3Record) })
	s, err := store.DrawSample(ctx, audit.DrawSampleInput{PopulationID: pop.ID, N: 100, Seed: "na", CreatedBy: "key_test_1742"})
	restore()
	if err != nil {
		t.Fatalf("DrawSample: %v", err)
	}
	if s.FreezeRevision == nil || *s.FreezeRevision != 2 {
		t.Fatalf("sample revision = %v, want 2", s.FreezeRevision)
	}
	if !slices.Contains(s.EvidenceRecordIDs, rev2Record) || slices.Contains(s.EvidenceRecordIDs, rev3Record) {
		t.Fatalf("sample labelled revision 2 holds %v; want revision 2's record and not revision 3's", s.EvidenceRecordIDs)
	}
	if len(s.EvidenceRecordIDs) != 4 {
		t.Fatalf("sample = %d records, want revision 2's population of 4", len(s.EvidenceRecordIDs))
	}
}
