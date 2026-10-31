//go:build integration

// #1702 — tenant-composite evidence FKs on sample_evidence and
// sample_annotations (C6). Asserts against a live Postgres:
//
//  1. As a tenant-A app session, a sample_evidence or sample_annotations
//     row that references tenant B's evidence id is refused with 23503;
//     the same insert against tenant A's own evidence succeeds.
//  2. The migration's pre-check aborts when a cross-tenant link already
//     exists, and re-applies cleanly when none does.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	sampleTenantFKUp   = "../../migrations/sql/20260808000000_sample_evidence_tenant_fk.sql"
	sampleTenantFKDown = "../../migrations/sql/20260808000000_sample_evidence_tenant_fk.down.sql"

	pgErrCheckViolation = "23514"
)

// seedSampleFixture inserts, under whatever tenant GUC is active, a control,
// one evidence record, a population and a sample, and returns the evidence
// and sample ids.
func seedSampleFixture(ctx context.Context, t *testing.T, tx pgx.Tx, tenant string) (evidenceID, sampleID string) {
	t.Helper()
	controlID := uuid.NewString()
	evidenceID = uuid.NewString()
	populationID := uuid.NewString()
	sampleID = uuid.NewString()

	mustInsertControl(ctx, t, tx, tenant, controlID, "IAC-1702")
	if _, err := tx.Exec(ctx, `
		INSERT INTO evidence_records
			(id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
		VALUES
			($1, $2, $3, 'scf:IAC-1702', now(), '{"connector_id":"test"}'::jsonb, 'pass', 'sha-1702')
	`, evidenceID, tenant, controlID); err != nil {
		t.Fatalf("INSERT evidence_records: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO populations
			(id, tenant_id, control_id, scope_predicate, time_window_start, time_window_end, created_by)
		VALUES
			($1, $2, $3, '{}'::jsonb, now() - interval '1 day', now(), 'test-1702')
	`, populationID, tenant, controlID); err != nil {
		t.Fatalf("INSERT populations: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO samples (id, tenant_id, population_id, n, seed, created_by)
		VALUES ($1, $2, $3, 1, 'seed-1702', 'test-1702')
	`, sampleID, tenant, populationID); err != nil {
		t.Fatalf("INSERT samples: %v", err)
	}
	return evidenceID, sampleID
}

func insertSampleEvidence(ctx context.Context, tx pgx.Tx, tenant, sampleID, evidenceID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO sample_evidence (sample_id, tenant_id, evidence_record_id, ordinal)
		VALUES ($1, $2, $3, 0)
	`, sampleID, tenant, evidenceID)
	return err
}

func insertSampleAnnotation(ctx context.Context, tx pgx.Tx, tenant, sampleID, evidenceID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO sample_annotations (id, tenant_id, sample_id, evidence_record_id, result, annotated_by)
		VALUES ($1, $2, $3, $4, 'passed', 'test-1702')
	`, uuid.NewString(), tenant, sampleID, evidenceID)
	return err
}

// inSavepoint runs fn in a nested transaction and rolls it back, so a
// refused insert does not abort the outer transaction.
func inSavepoint(ctx context.Context, t *testing.T, tx pgx.Tx, fn func(pgx.Tx) error) error {
	t.Helper()
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	defer func() { _ = sp.Rollback(ctx) }()
	return fn(sp)
}

func TestSampleTables_RefuseCrossTenantEvidence(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()

	// One rolled-back transaction: seed tenant B, then switch the GUC to
	// tenant A for the rest. Nothing is committed, so no cleanup is needed.
	withTenantTx(t, tenantB, false, func(ctx context.Context, tx pgx.Tx) {
		evidenceB, _ := seedSampleFixture(ctx, t, tx, tenantB)

		ctxA, err := tenancy.WithTenant(ctx, tenantA)
		if err != nil {
			t.Fatalf("WithTenant(A): %v", err)
		}
		if err := tenancy.ApplyTenant(ctxA, tx); err != nil {
			t.Fatalf("ApplyTenant(A): %v", err)
		}
		evidenceA, sampleA := seedSampleFixture(ctxA, t, tx, tenantA)

		inserts := []struct {
			name string
			fn   func(context.Context, pgx.Tx, string, string, string) error
		}{
			{"sample_evidence", insertSampleEvidence},
			{"sample_annotations", insertSampleAnnotation},
		}
		for _, ins := range inserts {
			err := inSavepoint(ctxA, t, tx, func(sp pgx.Tx) error {
				return ins.fn(ctxA, sp, tenantA, sampleA, evidenceB)
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != pgErrForeignKeyViolation {
				t.Errorf("%s referencing tenant B's evidence: want SQLSTATE %s, got %v", ins.name, pgErrForeignKeyViolation, err)
			}

			if err := inSavepoint(ctxA, t, tx, func(sp pgx.Tx) error {
				return ins.fn(ctxA, sp, tenantA, sampleA, evidenceA)
			}); err != nil {
				t.Errorf("%s referencing tenant A's own evidence: %v", ins.name, err)
			}
		}
	})
}

func TestSampleTenantFKMigration_PreCheck(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the pre-check test needs the migrate role")
	}
	up, err := os.ReadFile(sampleTenantFKUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(sampleTenantFKDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// withAdminTx runs fn in an atlas_migrate transaction that always rolls
	// back, so the schema and data are untouched for later tests.
	withAdminTx := func(fn func(context.Context, pgx.Tx)) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatalf("admin Begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		fn(ctx, tx)
	}

	t.Run("reapplies cleanly with no cross-tenant links", func(t *testing.T) {
		withAdminTx(func(ctx context.Context, tx pgx.Tx) {
			if _, err := tx.Exec(ctx, string(up)); err != nil {
				t.Fatalf("re-apply up migration: %v", err)
			}
		})
	})

	t.Run("aborts on a seeded cross-tenant link", func(t *testing.T) {
		withAdminTx(func(ctx context.Context, tx pgx.Tx) {
			// Back to the pre-migration single-column FKs, so a
			// cross-tenant row can exist as it could before #1702.
			if _, err := tx.Exec(ctx, string(down)); err != nil {
				t.Fatalf("apply down migration: %v", err)
			}
			tenantA := uuid.NewString()
			tenantB := uuid.NewString()
			evidenceB, _ := seedSampleFixture(ctx, t, tx, tenantB)
			_, sampleA := seedSampleFixture(ctx, t, tx, tenantA)
			if err := insertSampleEvidence(ctx, tx, tenantA, sampleA, evidenceB); err != nil {
				t.Fatalf("seed cross-tenant sample_evidence: %v", err)
			}
			if err := insertSampleAnnotation(ctx, tx, tenantA, sampleA, evidenceB); err != nil {
				t.Fatalf("seed cross-tenant sample_annotations: %v", err)
			}

			_, err := tx.Exec(ctx, string(up))
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != pgErrCheckViolation {
				t.Fatalf("up migration over a cross-tenant link: want SQLSTATE %s, got %v", pgErrCheckViolation, err)
			}
			if want := "sample_evidence=1, sample_annotations=1"; !strings.Contains(pgErr.Message, want) {
				t.Fatalf("pre-check message %q does not report %q", pgErr.Message, want)
			}
		})
	})
}
