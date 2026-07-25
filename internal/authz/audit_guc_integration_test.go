//go:build integration

// Integration test for slice 065 bug #1: the decision-audit writer must
// apply the `app.current_tenant` GUC inside the transaction that runs the
// INSERT, or the `decision_audit_log.tenant_write` RLS `WITH CHECK` policy
// rejects every row.
//
// Run with:  go test -tags=integration -race ./internal/authz/...
//
// Requires:
//   DATABASE_URL       admin DSN (BYPASSRLS atlas_migrate role) for cleanup
//   DATABASE_URL_APP   app DSN (atlas_app role, RLS-enforced) — the writer
//                      pool. atlas_app is NOSUPERUSER NOBYPASSRLS so the
//                      RLS policy is actually enforced; using atlas_migrate
//                      here would mask the bug entirely (BYPASSRLS skips
//                      WITH CHECK).

package authz_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// TestAuditWriter_TenantGUCApplied is the slice-065 bug-#1 headline test.
//
// It proves three things about (*AuditWriter).Write against the
// RLS-enforced atlas_app pool:
//
//  1. With a tenant context set, Write succeeds — the writer applies the
//     `app.current_tenant` GUC inside its own transaction so the
//     decision_audit_log.tenant_write WITH CHECK passes. (This is the
//     regression: before the fix Write used pool.Exec OUTSIDE a tx, the
//     GUC was empty, and this INSERT 500'd on every authenticated request.)
//
//  2. With NO tenant context, Write fails loudly — the writer refuses to
//     run the INSERT because tenancy.ApplyTenant cannot resolve a tenant.
//
//  3. The RLS WITH CHECK is genuinely the enforcer: a raw INSERT through
//     the same atlas_app pool WITHOUT applying the GUC is rejected by
//     Postgres. This is the control that proves point 1 is meaningful and
//     not just an artifact of a permissive table.
func TestAuditWriter_TenantGUCApplied(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)

	tenant := freshTenant(t, adminPool)
	writer := authz.NewAuditWriter(appPool)

	rec := authz.AuditRecord{
		TenantID:      tenant,
		UserID:        "user-guc",
		UserRoles:     []authz.Role{authz.RoleAdmin},
		Action:        "write",
		ResourceType:  "risks",
		Result:        "allow",
		Reason:        "slice-065 guc test",
		RequestPath:   "/v1/risks",
		RequestMethod: "POST",
	}

	t.Run("with_tenant_ctx_insert_succeeds", func(t *testing.T) {
		ctx, err := tenancy.WithTenant(context.Background(), tenant)
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		id, err := writer.Write(ctx, rec)
		if err != nil {
			t.Fatalf("Write with tenant ctx should succeed, got: %v", err)
		}
		if id == uuid.Nil {
			t.Fatal("Write returned uuid.Nil on the success path")
		}

		// Confirm the row actually landed and is visible to the
		// same-tenant read through the RLS-enforced pool.
		readCtx, err := tenancy.WithTenant(context.Background(), tenant)
		if err != nil {
			t.Fatalf("WithTenant (read): %v", err)
		}
		tx, err := appPool.Begin(readCtx)
		if err != nil {
			t.Fatalf("begin read tx: %v", err)
		}
		defer func() { _ = tx.Rollback(readCtx) }()
		if err := tenancy.ApplyTenant(readCtx, tx); err != nil {
			t.Fatalf("ApplyTenant (read): %v", err)
		}
		var got string
		if err := tx.QueryRow(readCtx,
			`SELECT reason FROM decision_audit_log WHERE decision_id = $1`, id,
		).Scan(&got); err != nil {
			t.Fatalf("read-back the written row: %v", err)
		}
		if got != rec.Reason {
			t.Fatalf("read-back reason = %q, want %q", got, rec.Reason)
		}
	})

	t.Run("without_tenant_ctx_write_fails", func(t *testing.T) {
		// context.Background() carries no tenant. The writer must refuse
		// to run the INSERT rather than silently emitting an untenanted
		// row (which RLS would reject anyway, but the writer should fail
		// loudly and locally).
		_, err := writer.Write(context.Background(), rec)
		if err == nil {
			t.Fatal("Write without tenant ctx should fail, got nil error")
		}
	})

	t.Run("raw_insert_without_guc_is_rejected_by_rls", func(t *testing.T) {
		// Control: prove the RLS WITH CHECK is the real gate. A raw
		// INSERT through the atlas_app pool that does NOT apply the
		// `app.current_tenant` GUC must be rejected by Postgres — this is
		// exactly the failure mode the old pool.Exec-outside-a-tx code
		// hit on every request.
		ctx := context.Background()
		tenantUUID, err := uuid.Parse(tenant)
		if err != nil {
			t.Fatalf("parse tenant: %v", err)
		}
		_, err = appPool.Exec(ctx, `
			INSERT INTO decision_audit_log
				(decision_id, tenant_id, user_id, user_roles,
				 action, resource_type, resource_id,
				 result, reason, policy_hits,
				 request_path, request_method)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`,
			uuid.New(), tenantUUID, "user-raw", []string{"admin"},
			"write", "risks", "",
			"allow", "raw insert no guc", []string{},
			"/v1/risks", "POST",
		)
		if err == nil {
			t.Fatal("raw INSERT without the app.current_tenant GUC should be " +
				"rejected by the decision_audit_log RLS WITH CHECK, got nil error")
		}
	})
}
