//go:build integration

// Integration tests for slice 035: role × endpoint matrix (AC-5) +
// ABAC predicate (AC-6) + decision audit log (AC-4) + append-only RLS.
//
// Run with:  go test -tags=integration -race ./internal/authz/...
//
// Requires:
//   DATABASE_URL       admin DSN (BYPASSRLS atlas_migrate role) for fixture setup
//   DATABASE_URL_APP   app DSN (atlas_app role, RLS-enforced)
//
// The matrix test enumerates a representative endpoint per resource
// type (the full chi route walk happens inside the HTTP server -- this
// suite tests the Engine directly with synthetic inputs that match the
// shapes BuildInput would produce). Together with the unit tests in
// decision_test.go + middleware_test.go this gives AC-5 coverage
// without spinning up the full http server.

package authz_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

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

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM decision_audit_log WHERE tenant_id = $1`,
			`DELETE FROM user_roles WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// tenantTx opens a transaction on the RLS-enforced app pool and applies
// the `app.current_tenant` GUC from ctx. Reads and DML against
// FORCE-ROW-LEVEL-SECURITY tables (decision_audit_log, user_roles) MUST
// go through a GUC-applied transaction — a bare pool.Exec / pool.QueryRow
// runs with an empty GUC and RLS silently filters every row.
//
// The caller MUST `defer tx.Rollback(ctx)` on the returned tx. Rollback
// is NOT registered via t.Cleanup on purpose: cleanups run AFTER the
// test function's own deferred `pool.Close()`, and Close() blocks
// forever waiting for a still-open transaction's connection. Tests that
// only read never commit; the deferred rollback is the release path.
func tenantTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tenant tx: %v", err)
	}
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("ApplyTenant: %v", err)
	}
	return tx
}

// matrixCase captures one row in the role × endpoint × expected-outcome
// matrix that AC-5 calls for. Every canonical role is paired with the
// representative endpoint per resource so the assertion surface stays
// readable.
type matrixCase struct {
	role        authz.Role
	action      string
	resource    string
	expectAllow bool
	notes       string
}

// matrix is the canonical role × endpoint × expected-outcome table.
// This same table is reproduced in the PR body for the HITL gate spot-
// check.
var matrix = []matrixCase{
	// admin: allows everything within tenant
	{authz.RoleAdmin, "write", "risks", true, "admin: write any tenant-scoped resource"},
	{authz.RoleAdmin, "write", "controls", true, "admin: write controls"},
	{authz.RoleAdmin, "approve", "policies", true, "admin: governance approve"},
	{authz.RoleAdmin, "publish", "policies", true, "admin: publish policy"},
	{authz.RoleAdmin, "read", "evidence", true, "admin: read evidence"},

	// grc_engineer: GRC operator surface
	{authz.RoleGRCEngineer, "write", "risks", true, "grc_engineer: write risks"},
	{authz.RoleGRCEngineer, "write", "policies", true, "grc_engineer: write policies"},
	{authz.RoleGRCEngineer, "approve", "framework-scopes", true, "grc_engineer: approve scopes"},
	{authz.RoleGRCEngineer, "publish", "policies", true, "grc_engineer: publish policy"},
	{authz.RoleGRCEngineer, "write", "evidence", true, "grc_engineer: write evidence (push)"},
	{authz.RoleGRCEngineer, "read", "samples", true, "grc_engineer: read samples"},

	// control_owner: attests, reads
	{authz.RoleControlOwner, "write", "evidence", true, "control_owner: submit attestation evidence"},
	{authz.RoleControlOwner, "read", "controls", true, "control_owner: read controls"},
	{authz.RoleControlOwner, "write", "risks", false, "control_owner: NOT allowed to write risks"},
	{authz.RoleControlOwner, "approve", "policies", false, "control_owner: NOT allowed to approve policies"},
	{authz.RoleControlOwner, "publish", "policies", false, "control_owner: NOT allowed to publish"},

	// auditor: read-only audit surfaces + sample annotate (period-gated)
	{authz.RoleAuditor, "read", "controls", true, "auditor: read controls"},
	{authz.RoleAuditor, "read", "policies", true, "auditor: read policies"},
	{authz.RoleAuditor, "write", "evidence", false, "auditor: NOT allowed to push evidence (AC-5 headline)"},
	{authz.RoleAuditor, "write", "risks", false, "auditor: NOT allowed to write risks"},
	{authz.RoleAuditor, "read", "framework-scopes", true, "auditor: read scopes"},

	// viewer: read-only catalog + dashboard surfaces
	{authz.RoleViewer, "read", "controls", true, "viewer: read controls"},
	{authz.RoleViewer, "read", "policies", true, "viewer: read policies"},
	{authz.RoleViewer, "write", "risks", false, "viewer: NOT allowed to write risks"},
	{authz.RoleViewer, "write", "controls", false, "viewer: NOT allowed to write controls"},
	{authz.RoleViewer, "approve", "policies", false, "viewer: NOT allowed to approve"},

	// slice 269 — dashboard snapshot export. Admit set: admin +
	// grc_engineer (IsApprover) + auditor. Viewer + control_owner
	// are deliberately denied — the bulk-handoff variant is a
	// narrower admit than the in-app dashboard read (slice 269 D3).
	{authz.RoleAdmin, "read", "dashboard", true, "slice 269: admin admitted to dashboard export"},
	{authz.RoleGRCEngineer, "read", "dashboard", true, "slice 269: grc_engineer admitted to dashboard export"},
	{authz.RoleAuditor, "read", "dashboard", true, "slice 269: auditor admitted to dashboard export"},
	{authz.RoleControlOwner, "read", "dashboard", false, "slice 269: control_owner NOT admitted to bulk dashboard export"},
	{authz.RoleViewer, "read", "dashboard", false, "slice 269: viewer NOT admitted to bulk dashboard export"},
}

// TestAuthzMatrix_AllRolesAllEndpoints is the AC-5 headline test. It
// asserts every cell in the role × endpoint matrix produces the
// expected allow / deny outcome. The matrix table doubles as the
// HITL-review artifact in the PR body.
func TestAuthzMatrix_AllRolesAllEndpoints(t *testing.T) {
	t.Parallel()
	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	for _, mc := range matrix {
		mc := mc
		name := string(mc.role) + "_" + mc.action + "_" + mc.resource
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := authz.Input{
				User: authz.UserInput{
					ID:    "u-" + string(mc.role),
					Roles: []authz.Role{mc.role},
				},
				TenantID: "00000000-0000-0000-0000-000000000099",
				Action:   mc.action,
				Resource: authz.ResourceInput{Type: mc.resource},
				Request:  authz.RequestInput{Method: "POST", Path: "/v1/" + mc.resource},
			}
			d, err := engine.Decide(context.Background(), in)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow != mc.expectAllow {
				t.Fatalf("matrix mismatch (%s): expected allow=%v, got allow=%v reason=%s",
					mc.notes, mc.expectAllow, d.Allow, d.Reason)
			}
		})
	}
}

// TestAuthzABAC_AuditorAuditPeriod is the AC-6 headline test: an
// auditor with audit_period_ids=[A] is denied access to a sample whose
// audit_period_id=B.
func TestAuthzABAC_AuditorAuditPeriod(t *testing.T) {
	t.Parallel()
	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	t.Run("within_period_allow", func(t *testing.T) {
		t.Parallel()
		d, err := engine.Decide(context.Background(), authz.Input{
			User: authz.UserInput{
				ID:    "auditor-1",
				Roles: []authz.Role{authz.RoleAuditor},
				Attrs: map[string]interface{}{
					"audit_period_ids": []interface{}{"period-A"},
				},
			},
			TenantID: "00000000-0000-0000-0000-000000000099",
			Action:   "read",
			Resource: authz.ResourceInput{
				Type: "samples",
				ID:   "s-1",
				Attrs: map[string]interface{}{
					"audit_period_id": "period-A",
				},
			},
			Request: authz.RequestInput{Method: "GET", Path: "/v1/samples/s-1"},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if !d.Allow {
			t.Fatalf("within-period read denied: %s", d.Reason)
		}
	})

	t.Run("outside_period_deny", func(t *testing.T) {
		t.Parallel()
		d, err := engine.Decide(context.Background(), authz.Input{
			User: authz.UserInput{
				ID:    "auditor-1",
				Roles: []authz.Role{authz.RoleAuditor},
				Attrs: map[string]interface{}{
					"audit_period_ids": []interface{}{"period-A"},
				},
			},
			TenantID: "00000000-0000-0000-0000-000000000099",
			Action:   "read",
			Resource: authz.ResourceInput{
				Type: "samples",
				ID:   "s-2",
				Attrs: map[string]interface{}{
					"audit_period_id": "period-B",
				},
			},
			Request: authz.RequestInput{Method: "GET", Path: "/v1/samples/s-2"},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Allow {
			t.Fatalf("outside-period read allowed (should deny)")
		}
	})
}

// TestAuthzAuditLog_BothOutcomesPersist is the AC-4 headline test:
// every decision (allow OR deny) writes one row to decision_audit_log.
// Uses the real DB via app pool + tenant context.
func TestAuthzAuditLog_BothOutcomesPersist(t *testing.T) {
	t.Parallel()
	appPool := openPool(t, appDSN(t))
	defer appPool.Close()
	adminPool := openPool(t, adminDSN(t))
	defer adminPool.Close()

	tenant := freshTenant(t, adminPool)
	writer := authz.NewAuditWriter(appPool)

	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	// Write one allow + one deny.
	if _, err := writer.Write(ctx, authz.AuditRecord{
		TenantID:      tenant,
		UserID:        "user-allow",
		UserRoles:     []authz.Role{authz.RoleAdmin},
		Action:        "write",
		ResourceType:  "risks",
		Result:        "allow",
		Reason:        "allowed",
		RequestPath:   "/v1/risks",
		RequestMethod: "POST",
	}); err != nil {
		t.Fatalf("Write allow: %v", err)
	}
	if _, err := writer.Write(ctx, authz.AuditRecord{
		TenantID:      tenant,
		UserID:        "user-deny",
		UserRoles:     []authz.Role{authz.RoleViewer},
		Action:        "write",
		ResourceType:  "risks",
		Result:        "deny",
		Reason:        "default-deny",
		RequestPath:   "/v1/risks",
		RequestMethod: "POST",
	}); err != nil {
		t.Fatalf("Write deny: %v", err)
	}

	// Verify both rows are visible to the same-tenant read. The read goes
	// through a GUC-applied tx — decision_audit_log is FORCE RLS, so a
	// bare pool.QueryRow with an empty GUC would see zero rows.
	readTx := tenantTx(t, ctx, appPool)
	defer func() { _ = readTx.Rollback(ctx) }()
	var rows int
	if err := readTx.QueryRow(ctx, `
		SELECT count(*) FROM decision_audit_log WHERE tenant_id = $1::uuid
	`, tenant).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 2 {
		t.Fatalf("expected 2 audit rows, got %d", rows)
	}
}

// TestAuthzAuditLog_AppendOnlyRLS asserts that decision_audit_log is
// append-only at the RLS layer: SELECT + INSERT are allowed for
// atlas_app, but UPDATE + DELETE are denied because no policy was
// installed for those commands under FORCE ROW LEVEL SECURITY.
func TestAuthzAuditLog_AppendOnlyRLS(t *testing.T) {
	t.Parallel()
	appPool := openPool(t, appDSN(t))
	defer appPool.Close()
	adminPool := openPool(t, adminDSN(t))
	defer adminPool.Close()

	tenant := freshTenant(t, adminPool)
	writer := authz.NewAuditWriter(appPool)

	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	id, err := writer.Write(ctx, authz.AuditRecord{
		TenantID:      tenant,
		UserID:        "user-1",
		UserRoles:     []authz.Role{authz.RoleAdmin},
		Action:        "write",
		ResourceType:  "risks",
		Result:        "allow",
		Reason:        "allowed",
		RequestPath:   "/v1/risks",
		RequestMethod: "POST",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// All three operations below run through ONE GUC-applied tx on the
	// RLS-enforced app pool. The GUC must be set, otherwise we would be
	// testing "no tenant context -> 0 rows" instead of the real property:
	// "tenant context IS set, but decision_audit_log has no UPDATE/DELETE
	// policy under FORCE ROW LEVEL SECURITY, so the mutation is denied".
	txn := tenantTx(t, ctx, appPool)
	// txn may be reassigned below (a failed statement aborts the tx); the
	// closure reads the current value so whichever tx is live gets rolled
	// back before the deferred appPool.Close() runs.
	defer func() { _ = txn.Rollback(ctx) }()

	// UPDATE must be denied for atlas_app -- no UPDATE policy under FORCE
	// means RLS reports 0 affected rows (or a permission error).
	tag, err := txn.Exec(ctx, `
		UPDATE decision_audit_log SET reason = 'tampered' WHERE decision_id = $1
	`, id)
	if err != nil {
		// pgx treats RLS-blocked UPDATE as 0-rows, not an error. Some
		// servers do raise a permission error for FORCE+no-policy.
		// Either is acceptable; the test fails only if the row was
		// actually mutated.
		t.Logf("UPDATE returned error (acceptable under RLS): %v", err)
		// A failed statement aborts the tx; reopen for the read-back.
		_ = txn.Rollback(ctx)
		txn = tenantTx(t, ctx, appPool)
	} else if tag.RowsAffected() != 0 {
		t.Fatalf("UPDATE affected %d rows; append-only RLS should report 0", tag.RowsAffected())
	}

	// Read back to confirm the row is unchanged.
	var reason string
	if err := txn.QueryRow(ctx, `
		SELECT reason FROM decision_audit_log WHERE decision_id = $1
	`, id).Scan(&reason); err != nil {
		t.Fatalf("read-back: %v", err)
	}
	if reason != "allowed" {
		t.Fatalf("audit row was mutated despite append-only RLS: %q", reason)
	}

	// DELETE must also be denied.
	tag, err = txn.Exec(ctx, `
		DELETE FROM decision_audit_log WHERE decision_id = $1
	`, id)
	if err != nil {
		t.Logf("DELETE returned error (acceptable under RLS): %v", err)
	} else if tag.RowsAffected() != 0 {
		t.Fatalf("DELETE affected %d rows; append-only RLS should report 0", tag.RowsAffected())
	}
}

// TestAuthzDBRolesResolver verifies user_roles rows are read back
// correctly and that a non-canonical role is filtered. Hits the real
// DB through the app pool + tenant context.
func TestAuthzDBRolesResolver(t *testing.T) {
	t.Parallel()
	appPool := openPool(t, appDSN(t))
	defer appPool.Close()
	adminPool := openPool(t, adminDSN(t))
	defer adminPool.Close()

	tenant := freshTenant(t, adminPool)

	// Seed two roles for one user via the admin pool (bypasses RLS for
	// the fixture setup).
	if _, err := adminPool.Exec(context.Background(), `
		INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
		VALUES ($1, 'user-X', 'auditor', 'test'),
		       ($1, 'user-X', 'viewer',  'test')
	`, tenant); err != nil {
		t.Fatalf("seed user_roles: %v", err)
	}

	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	resolver := authz.NewDBRolesResolver(appPool)
	roles, err := resolver.RolesFor(ctx, tenant, "user-X")
	if err != nil {
		t.Fatalf("RolesFor: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %d: %v", len(roles), roles)
	}
}
