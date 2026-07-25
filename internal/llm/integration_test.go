//go:build integration

// Integration tests for slice 498: the internal/llm substrate against real
// Postgres. Covers the load-bearing DB-layer guarantees:
//
//   - AC-10 smoke path: stub client -> ai_generations write -> read back.
//   - AC-12 cross-tenant isolation: tenant B cannot read tenant A's rows.
//   - AC-12 append-only: ai_generations has no UPDATE/DELETE path (RLS +
//     grants), so an UPDATE/DELETE is denied at the DB layer.
//   - AC-9  DB-layer ai_assisted <-> human_approver enforcement: a table that
//     adopts the reusable ai_assist_human_approver_guard CHECK rejects a
//     direct human_approved=true / NULL-approver write at the DB, not just in
//     Go.
//
// Memory rule: "Never mock the DB" -- every test exercises the real
// migration, the real RLS policies, the real CHECK function, and the real
// sqlc writer.
//
// Run with:  go test -tags=integration -p 1 ./internal/llm/...

package llm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant returns a unique tenant id and registers cleanup of its
// ai_generations rows via the admin (BYPASSRLS) pool. Pure tenant-scoped
// DELETE, so it delegates to dbtest.SeedTenant (slice 435 / 742 drain).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, "ai_generations")
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func validReq(surface llm.Surface) llm.GenerateRequest {
	return llm.GenerateRequest{
		Surface:       surface,
		PromptVersion: "v1",
		SystemPrompt:  "you are a compliance assistant",
		Context:       map[string]any{"evidence_id": "ev-1", "freshness_days": 12},
		MaxTokens:     256,
		Timeout:       5 * time.Second,
	}
}

// ----- AC-10: smoke path (client -> ai_generations write -> read back) -----

func TestSmokePath_StubClientToAuditWrite(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	svc := llm.NewService(llm.NewStubClient(), llm.NewAuditWriter(app))
	res, row, err := svc.GenerateAndRecord(ctx, validReq(llm.SurfaceQuestionnaire), "answer-42")
	if err != nil {
		t.Fatalf("GenerateAndRecord: %v", err)
	}
	if res.Text == "" {
		t.Fatal("empty draft from stub")
	}
	if row.Surface != string(llm.SurfaceQuestionnaire) {
		t.Errorf("row.Surface = %q", row.Surface)
	}
	if row.RawDraft != res.Text {
		t.Errorf("row.RawDraft = %q, want %q", row.RawDraft, res.Text)
	}
	if row.SurfaceSubject != "answer-42" {
		t.Errorf("row.SurfaceSubject = %q", row.SurfaceSubject)
	}
	if row.ModelProvider == "" || row.ModelName == "" || row.ModelVersion == "" {
		t.Errorf("provenance not captured: %+v", row)
	}
	// Read back via the writer's tenant-scoped pool.
	writer := llm.NewAuditWriter(app)
	rows, err := writer.ListBySubject(ctx, llm.SurfaceQuestionnaire, "answer-42")
	if err != nil {
		t.Fatalf("ListBySubject: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListBySubject returned %d rows, want 1", len(rows))
	}
}

// ----- AC-12: cross-tenant isolation -----

func TestCrossTenantIsolation(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	writer := llm.NewAuditWriter(app)

	// Tenant A writes a generation.
	ctxA := tenantCtx(t, tenantA)
	if _, err := writer.Write(ctxA, llm.Generation{
		Surface:        llm.SurfaceSummary,
		PromptVersion:  "v1",
		ModelName:      "stub",
		ModelVersion:   "0",
		ModelProvider:  "stub",
		SystemPrompt:   "sys",
		RawDraft:       "tenant A secret draft",
		SurfaceSubject: "s1",
	}); err != nil {
		t.Fatalf("tenant A Write: %v", err)
	}

	// Tenant B must see ZERO of tenant A's rows.
	ctxB := tenantCtx(t, tenantB)
	countB, err := writer.CountForTenant(ctxB)
	if err != nil {
		t.Fatalf("tenant B count: %v", err)
	}
	if countB != 0 {
		t.Fatalf("tenant B saw %d rows of tenant A; RLS leak", countB)
	}

	// Tenant A sees its own row.
	countA, err := writer.CountForTenant(ctxA)
	if err != nil {
		t.Fatalf("tenant A count: %v", err)
	}
	if countA != 1 {
		t.Fatalf("tenant A saw %d rows, want 1", countA)
	}
}

// ----- AC-12: append-only (no UPDATE/DELETE path) -----

func TestAppendOnly_NoUpdateNoDelete(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	writer := llm.NewAuditWriter(app)
	row, err := writer.Write(ctx, llm.Generation{
		Surface:       llm.SurfaceGapExplanation,
		PromptVersion: "v1",
		ModelName:     "stub",
		ModelVersion:  "0",
		ModelProvider: "stub",
		SystemPrompt:  "sys",
		RawDraft:      "original",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// A direct UPDATE under the app role + tenant context must be denied:
	// there is no UPDATE RLS policy (and no UPDATE grant), so the row is
	// invisible to UPDATE -- 0 rows affected, which we treat as denied.
	rowID := uuid.UUID(row.ID.Bytes)
	affected := mutateUnderTenant(t, app, tenant, `UPDATE ai_generations SET raw_draft = 'tampered' WHERE id = $1`, rowID)
	if affected != 0 {
		t.Fatalf("UPDATE affected %d rows; append-only violated", affected)
	}

	// Likewise DELETE.
	affected = mutateUnderTenant(t, app, tenant, `DELETE FROM ai_generations WHERE id = $1`, rowID)
	if affected != 0 {
		t.Fatalf("DELETE affected %d rows; append-only violated", affected)
	}

	// Row is unchanged when read via admin (BYPASSRLS).
	var draft string
	if err := admin.QueryRow(context.Background(),
		`SELECT raw_draft FROM ai_generations WHERE id = $1`, rowID).Scan(&draft); err != nil {
		t.Fatalf("admin read: %v", err)
	}
	if draft != "original" {
		t.Fatalf("raw_draft = %q, want unchanged 'original'", draft)
	}
}

// mutateUnderTenant runs a write statement inside a tenant-scoped tx under the
// app role and returns rows affected.
func mutateUnderTenant(t *testing.T, app *pgxpool.Pool, tenant, sql string, args ...any) int64 {
	t.Helper()
	ctx := tenantCtx(t, tenant)
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		// A permission error (no grant) is also "denied" -- return 0.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			t.Logf("mutate denied by DB: %s", pgErr.Message)
			return 0
		}
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return tag.RowsAffected()
}

// ----- AC-9: DB-layer ai_assisted <-> human_approver enforcement -----
//
// ai_generations itself carries no approval columns (it is a draft ledger).
// AC-9 proves the REUSABLE enforcement template at the DB layer: a table that
// adopts the ai_assist_human_approver_guard CHECK rejects the forbidden write
// directly, not merely in Go. We create an ephemeral adopter table (the exact
// shape a consumer like 440/441/471 will use) and prove the CHECK fires.

func TestReusableCheckTemplate_RejectsAtDBLayer(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()

	// Create an ephemeral adopter table using the shipped reusable CHECK.
	// Dropped on cleanup. Uses admin (DDL) role.
	_, err := admin.Exec(ctx, `
		CREATE TEMP TABLE adopter_test (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			ai_assisted     BOOLEAN NOT NULL DEFAULT TRUE,
			human_approved  BOOLEAN NOT NULL DEFAULT FALSE,
			human_approver  TEXT NULL,
			CONSTRAINT adopter_ai_assist_invariant
				CHECK (ai_assist_human_approver_guard(
					ai_assisted, human_approved, human_approver))
		)`)
	if err != nil {
		t.Fatalf("create adopter table: %v", err)
	}

	// 1. Forbidden write: ai_assisted + human_approved + NULL approver.
	_, err = admin.Exec(ctx, `
		INSERT INTO adopter_test (ai_assisted, human_approved, human_approver)
		VALUES (TRUE, TRUE, NULL)`)
	if err == nil {
		t.Fatal("DB accepted human_approved=true with NULL approver; CHECK not enforced")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("expected 23514 check_violation, got %v", err)
	}
	if pgErr.ConstraintName != "adopter_ai_assist_invariant" {
		t.Errorf("violated constraint = %q", pgErr.ConstraintName)
	}

	// 2. Forbidden write: empty-string approver (confused-deputy guard).
	_, err = admin.Exec(ctx, `
		INSERT INTO adopter_test (ai_assisted, human_approved, human_approver)
		VALUES (TRUE, TRUE, '')`)
	if err == nil {
		t.Fatal("DB accepted empty-string approver; length guard not enforced")
	}

	// 3. Allowed: ai_assisted + human_approved + real approver.
	if _, err := admin.Exec(ctx, `
		INSERT INTO adopter_test (ai_assisted, human_approved, human_approver)
		VALUES (TRUE, TRUE, 'user-7')`); err != nil {
		t.Fatalf("DB rejected a valid approved row: %v", err)
	}

	// 4. Allowed: ai_assisted, not yet approved (the common draft state).
	if _, err := admin.Exec(ctx, `
		INSERT INTO adopter_test (ai_assisted, human_approved, human_approver)
		VALUES (TRUE, FALSE, NULL)`); err != nil {
		t.Fatalf("DB rejected an unapproved draft row: %v", err)
	}
}

// ----- DB CHECK: surface + provenance constraints -----

func TestSurfaceAndProvenanceCheck(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	writer := llm.NewAuditWriter(app)

	// Unknown surface rejected by the Go validator before the DB.
	_, err := writer.Write(ctx, llm.Generation{
		Surface: "bogus", PromptVersion: "v1", ModelName: "m", ModelVersion: "1",
		ModelProvider: "stub", SystemPrompt: "s",
	})
	if !errors.Is(err, llm.ErrInvalidGeneration) {
		t.Fatalf("bogus surface = %v, want ErrInvalidGeneration", err)
	}

	// A raw INSERT with an unknown surface is rejected at the DB CHECK.
	rawErr := rawInsertUnknownSurface(t, app, tenant)
	var pgErr *pgconn.PgError
	if !errors.As(rawErr, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("raw unknown-surface insert = %v, want 23514", rawErr)
	}
}

func rawInsertUnknownSurface(t *testing.T, app *pgxpool.Pool, tenant string) error {
	t.Helper()
	ctx := tenantCtx(t, tenant)
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO ai_generations
			(tenant_id, surface, prompt_version, model_name, model_version, model_provider, system_prompt, raw_draft)
		VALUES ($1, 'bogus', 'v1', 'm', '1', 'stub', 's', 'd')`, tenant)
	return err
}

var _ = pgx.ErrNoRows
