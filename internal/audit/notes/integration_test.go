//go:build integration

// Integration tests for slice 025: auditor role + scoped read-only access.
// Real Postgres only -- RLS cannot be tested against a fake DB, and the
// author-only-read guarantee is only meaningful against a real table.
//
// Run with: go test -tags=integration -race ./internal/audit/notes/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); used by the harness
//                       to seed audit_periods outside the tenant GUC.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       notes.Store + auditor.Store run against this so RLS
//                       is enforced.

package notes_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness helpers (mirrored from slice 028's integration_test.go) -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

// freshTenant cleans up the slice-025 + slice-028 tables for this tenant
// after the test, so reruns don't accumulate.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_notes WHERE tenant_id = $1`,
			`DELETE FROM auditor_assignments WHERE tenant_id = $1`,
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`UPDATE populations SET audit_period_id = NULL, frozen_at = NULL WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func seedFrameworkVersion(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fwID := uuid.New()
	versionID := uuid.New()
	slug := fmt.Sprintf("slice025-%s", uuid.NewString()[:8])
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Slice 025 test framework', $2, 'test')
	`, fwID, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')
	`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	return versionID
}

// seedPeriod inserts an open audit_periods row using the admin pool.
// Slice 025's Store doesn't write periods -- it consumes them.
func seedPeriod(t *testing.T, admin *pgxpool.Pool, tenant string, fwID uuid.UUID, name string, start, end time.Time) uuid.UUID {
	t.Helper()
	periodID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO audit_periods (
			id, tenant_id, name, framework_version_id, period_start, period_end,
			status, created_by, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			'open', 'test-seed', now(), now()
		)
	`, periodID, tenant, name, fwID, start, end); err != nil {
		t.Fatalf("seed audit_period: %v", err)
	}
	return periodID
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// ===== AC-4 + P0-2: notes are auditor-only / author-only visible =====

// TestNotes_AuthorCreatesAndReads: happy path -- auditor A creates a
// note, reads it back, and lists it.
func TestNotes_AuthorCreatesAndReads(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-4 period A",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	n, err := store.Create(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Walkthrough notes for CC6.1.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n.ID == uuid.Nil {
		t.Fatalf("expected non-nil note id")
	}
	if n.Visibility != "auditor_only" {
		t.Fatalf("expected visibility=auditor_only, got %q", n.Visibility)
	}

	got, err := store.Get(ctx, n.ID, "auditor-A")
	if err != nil {
		t.Fatalf("Get(author=A): %v", err)
	}
	if got.Body != "Walkthrough notes for CC6.1." {
		t.Fatalf("unexpected body: %q", got.Body)
	}

	rows, err := store.ListForAuthorAndPeriod(ctx, periodID, "auditor-A")
	if err != nil {
		t.Fatalf("List(A): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 note for A, got %d", len(rows))
	}
}

// TestNotes_SecondAuditorCannotReadFirstAuditorsNote: P0-2 / AC-4.
// Auditor B looks up auditor A's note id -> ErrNotFound; lists for B
// returns empty.
func TestNotes_SecondAuditorCannotReadFirstAuditorsNote(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "P0-2 period",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "finding",
		Body:          "Private note from auditor A.",
	})
	if err != nil {
		t.Fatalf("Create(A): %v", err)
	}

	// Auditor B cannot Get auditor A's note.
	if _, err := store.Get(ctx, created.ID, "auditor-B"); !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for B reading A's note, got %v", err)
	}

	// Auditor B's list for this period is empty.
	rows, err := store.ListForAuthorAndPeriod(ctx, periodID, "auditor-B")
	if err != nil {
		t.Fatalf("List(B): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 notes for B, got %d", len(rows))
	}

	// And the grc_engineer-as-auditee path: a "grc-user" UserID also
	// sees an empty list (the query layer enforces author_user_id =
	// caller; auditees never satisfy this for an auditor's row).
	rows, err = store.ListForAuthorAndPeriod(ctx, periodID, "grc-user")
	if err != nil {
		t.Fatalf("List(grc-user): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 notes visible to grc-user (P0-2), got %d", len(rows))
	}
}

// TestNotes_RejectsInvalidScopeType: defense-in-depth -- the Store
// rejects unknown scope_types before hitting the DB CHECK constraint.
func TestNotes_RejectsInvalidScopeType(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "invalid scope",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	_, err := store.Create(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "not-a-real-scope",
		Body:          "should be rejected",
	})
	if !errors.Is(err, notes.ErrInvalidScopeType) {
		t.Fatalf("expected ErrInvalidScopeType, got %v", err)
	}
}

// TestNotes_RejectsEmptyBody: defense-in-depth -- empty body is
// rejected at the Store layer before the DB CHECK fires.
func TestNotes_RejectsEmptyBody(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "empty body",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	_, err := store.Create(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "period",
		Body:          "   ",
	})
	if err == nil {
		t.Fatalf("expected empty-body error, got nil")
	}
}

// ===== AC-5 + AC-6: auditor assignments drive /v1/me/audit-period(s) =====

// TestAuditor_ListAssignmentsAfterAssign: auditor A assigned to P1 ->
// ListAssignmentsFor returns P1.
func TestAuditor_ListAssignmentsAfterAssign(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	p1 := seedPeriod(t, admin, tenant, fwID, "AC-5 Q2",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := auditor.NewStore(app)
	ctx := ctxFor(t, tenant)

	if err := store.Assign(ctx, "auditor-A", p1, "test-admin"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	rows, err := store.ListAssignmentsFor(ctx, "auditor-A")
	if err != nil {
		t.Fatalf("ListAssignmentsFor: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(rows))
	}
	if rows[0].AuditPeriodID != p1 {
		t.Fatalf("expected period %v, got %v", p1, rows[0].AuditPeriodID)
	}
	if rows[0].Name != "AC-5 Q2" {
		t.Fatalf("expected joined name AC-5 Q2, got %q", rows[0].Name)
	}
}

// TestAuditor_MultiplePeriodAssignments: AC-6 -- A is assigned to two
// historical periods; both come back in ListAssignmentsFor.
func TestAuditor_MultiplePeriodAssignments(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	p1 := seedPeriod(t, admin, tenant, fwID, "AC-6 Q1",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	p2 := seedPeriod(t, admin, tenant, fwID, "AC-6 Q2",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := auditor.NewStore(app)
	ctx := ctxFor(t, tenant)

	if err := store.Assign(ctx, "auditor-A", p1, "test-admin"); err != nil {
		t.Fatalf("Assign(p1): %v", err)
	}
	if err := store.Assign(ctx, "auditor-A", p2, "test-admin"); err != nil {
		t.Fatalf("Assign(p2): %v", err)
	}

	rows, err := store.ListAssignmentsFor(ctx, "auditor-A")
	if err != nil {
		t.Fatalf("ListAssignmentsFor: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(rows))
	}
	// Order is by period_start DESC -- p2 (Q2) first, then p1 (Q1).
	if rows[0].AuditPeriodID != p2 {
		t.Fatalf("expected p2 first, got %v", rows[0].AuditPeriodID)
	}
	if rows[1].AuditPeriodID != p1 {
		t.Fatalf("expected p1 second, got %v", rows[1].AuditPeriodID)
	}
}

// TestAuditor_AssignIsIdempotent: re-assigning the same (user, period)
// is a no-op (ON CONFLICT DO NOTHING).
func TestAuditor_AssignIsIdempotent(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	p1 := seedPeriod(t, admin, tenant, fwID, "idempotent",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := auditor.NewStore(app)
	ctx := ctxFor(t, tenant)

	for i := 0; i < 3; i++ {
		if err := store.Assign(ctx, "auditor-A", p1, "test-admin"); err != nil {
			t.Fatalf("Assign #%d: %v", i, err)
		}
	}
	rows, err := store.ListAssignmentsFor(ctx, "auditor-A")
	if err != nil {
		t.Fatalf("ListAssignmentsFor: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment after 3 inserts, got %d", len(rows))
	}
}

// TestAuditor_PeriodIDsForUserDrivesAttrsResolver: AC-2 -- the
// AttrsResolver hot path. Returns the auditor's period UUIDs as the
// OPA `audit_period_ids` attribute.
func TestAuditor_PeriodIDsForUserDrivesAttrsResolver(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	p1 := seedPeriod(t, admin, tenant, fwID, "attrs A",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	p2 := seedPeriod(t, admin, tenant, fwID, "attrs B",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := auditor.NewStore(app)
	ctx := ctxFor(t, tenant)

	if err := store.Assign(ctx, "auditor-A", p1, "test"); err != nil {
		t.Fatalf("Assign p1: %v", err)
	}
	if err := store.Assign(ctx, "auditor-A", p2, "test"); err != nil {
		t.Fatalf("Assign p2: %v", err)
	}

	got, err := store.AuditPeriodIDsFor(ctx, "auditor-A")
	if err != nil {
		t.Fatalf("AuditPeriodIDsFor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 period ids, got %d", len(got))
	}
	want := []uuid.UUID{p1, p2}
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d] got %v want %v", i, got[i], want[i])
		}
	}
}

// TestAuditor_NoAssignmentReturnsEmpty: an auditor with zero assignments
// gets an empty slice from AuditPeriodIDsFor -- which the OPA layer
// then uses to deny period-scoped reads (P0-3).
func TestAuditor_NoAssignmentReturnsEmpty(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	_ = seedFrameworkVersion(t, admin)

	store := auditor.NewStore(app)
	ctx := ctxFor(t, tenant)

	got, err := store.AuditPeriodIDsFor(ctx, "auditor-unassigned")
	if err != nil {
		t.Fatalf("AuditPeriodIDsFor: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 period ids for unassigned auditor (P0-3), got %d", len(got))
	}
}

// TestAttrsResolver_PopulatesAuditPeriodIDs: the AttrsFor hook returns
// a map with audit_period_ids as a slice of strings (the rego layer
// reads strings, not UUIDs).
func TestAttrsResolver_PopulatesAuditPeriodIDs(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	p1 := seedPeriod(t, admin, tenant, fwID, "attrs resolver",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := auditor.NewStore(app)
	if err := store.Assign(ctxFor(t, tenant), "auditor-A", p1, "test"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	resolver := auditor.NewDBAttrsResolver(app)
	got, err := resolver.AttrsFor(context.Background(), tenant, "auditor-A", nil)
	if err != nil {
		t.Fatalf("AttrsFor: %v", err)
	}
	ids, ok := got["audit_period_ids"].([]interface{})
	if !ok {
		t.Fatalf("expected audit_period_ids as []interface{}, got %T", got["audit_period_ids"])
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 id, got %d", len(ids))
	}
	if ids[0].(string) != p1.String() {
		t.Fatalf("expected %v, got %v", p1, ids[0])
	}
}
