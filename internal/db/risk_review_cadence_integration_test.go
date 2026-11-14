//go:build integration

// #2059 — a risk's review_due_at defaults from the organisation risk review
// cadence. Asserts against a live Postgres:
//
//  1. The migration backfills every existing risk without a review date to
//     created_at + cadence (365 by default, also for a bare-UUID tenant with
//     no tenants row) and keeps a review date that was already set.
//  2. After the migration, an insert without a review date gets
//     created_at + the tenant's cadence, and changing the cadence leaves
//     existing review dates alone.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	riskReviewCadenceUp   = "../../migrations/sql/20261015000000_risk_review_cadence.sql"
	riskReviewCadenceDown = "../../migrations/sql/20261015000000_risk_review_cadence.down.sql"
)

// insertRiskAt inserts a risk with a fixed created_at and an optional review
// date, returning its id.
func insertRiskAt(ctx context.Context, t *testing.T, tx pgx.Tx, tenant string, createdAt time.Time, reviewDue *time.Time) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO risks (id, tenant_id, title, category, treatment, created_at, review_due_at)
		VALUES ($1, $2, 'review cadence 2059', 'operational', 'avoid', $3, $4)
	`, id, tenant, createdAt, reviewDue); err != nil {
		t.Fatalf("INSERT risks: %v", err)
	}
	return id
}

func reviewDueOf(ctx context.Context, t *testing.T, tx pgx.Tx, id string) *time.Time {
	t.Helper()
	var due *time.Time
	if err := tx.QueryRow(ctx, `SELECT review_due_at FROM risks WHERE id = $1`, id).Scan(&due); err != nil {
		t.Fatalf("read review_due_at: %v", err)
	}
	return due
}

func assertDue(t *testing.T, what string, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: review_due_at is NULL, want %s", what, want.UTC())
		return
	}
	if !got.Equal(want) {
		t.Errorf("%s: review_due_at = %s, want %s", what, got.UTC(), want.UTC())
	}
}

func TestRiskReviewCadenceMigration_BackfillsRisksWithoutReviewDate(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(riskReviewCadenceUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(riskReviewCadenceDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// An atlas_migrate transaction that always rolls back, so the schema and
	// data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Back to the pre-#2059 schema: nothing defaults review_due_at.
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}

	tenant := uuid.NewString()
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, false)`,
		tenant, "cadence-2059-"+tenant[:8],
	); err != nil {
		t.Fatalf("INSERT tenants: %v", err)
	}
	bareTenant := uuid.NewString() // no tenants row: predates slice 144

	created := time.Date(2025, 3, 10, 14, 30, 0, 0, time.UTC)
	explicit := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	noDate := insertRiskAt(ctx, t, tx, tenant, created, nil)
	withDate := insertRiskAt(ctx, t, tx, tenant, created, &explicit)
	bareNoDate := insertRiskAt(ctx, t, tx, bareTenant, created, nil)
	if due := reviewDueOf(ctx, t, tx, noDate); due != nil {
		t.Fatalf("before the migration a risk without a date must stay NULL; got %s", due)
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply up migration: %v", err)
	}

	yearLater := created.AddDate(0, 0, 365)
	assertDue(t, "backfilled risk", reviewDueOf(ctx, t, tx, noDate), yearLater)
	assertDue(t, "risk with a set date", reviewDueOf(ctx, t, tx, withDate), explicit)
	assertDue(t, "bare-UUID tenant's risk", reviewDueOf(ctx, t, tx, bareNoDate), yearLater)

	// New inserts read the tenant's cadence; a changed cadence leaves
	// existing dates alone.
	if _, err := tx.Exec(ctx,
		`UPDATE tenants SET risk_review_cadence_days = 90 WHERE id = $1`, tenant); err != nil {
		t.Fatalf("set cadence: %v", err)
	}
	later := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	fresh := insertRiskAt(ctx, t, tx, tenant, later, nil)
	assertDue(t, "risk inserted after a cadence change", reviewDueOf(ctx, t, tx, fresh), later.AddDate(0, 0, 90))
	assertDue(t, "backfilled risk after a cadence change", reviewDueOf(ctx, t, tx, noDate), yearLater)
	keptExplicit := insertRiskAt(ctx, t, tx, tenant, later, &explicit)
	assertDue(t, "risk inserted with a date", reviewDueOf(ctx, t, tx, keptExplicit), explicit)

	// The cadence is bounded.
	if _, err := tx.Exec(ctx, `SAVEPOINT bound`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE tenants SET risk_review_cadence_days = 0 WHERE id = $1`, tenant); err == nil {
		t.Error("a cadence of 0 days must be refused by tenants_risk_review_cadence_days_chk")
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT bound`); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}
}
