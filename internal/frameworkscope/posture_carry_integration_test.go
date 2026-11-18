//go:build integration

// Integration tests for carrying a posture target forward to a new
// framework version (#2406): activating a scope on N+1 copies the tenant's
// target on N when N+1 has none, logs the copy as carry_over with a system
// actor, never overwrites a target already set, writes nothing on a second
// activation, and copies only the activating tenant's own target.

package frameworkscope_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type postureVals struct {
	Target, Warning, Critical float64
	Direction, UpdatedBy      string
}

func seedPostureTarget(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID, v postureVals) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_posture_targets
			(tenant_id, framework_version_id, target_value, warning_threshold,
			 critical_threshold, direction, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		tenant, fvID, v.Target, v.Warning, v.Critical, v.Direction, v.UpdatedBy); err != nil {
		t.Fatalf("seed posture target: %v", err)
	}
}

// readPostureTarget returns the tenant's target on the version, or nil.
func readPostureTarget(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID) *postureVals {
	t.Helper()
	var v postureVals
	err := admin.QueryRow(context.Background(), `
		SELECT target_value::float8, warning_threshold::float8,
		       critical_threshold::float8, direction, updated_by
		  FROM framework_posture_targets
		 WHERE tenant_id = $1 AND framework_version_id = $2`, tenant, fvID).
		Scan(&v.Target, &v.Warning, &v.Critical, &v.Direction, &v.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatalf("read posture target: %v", err)
	}
	return &v
}

type carryAuditRow struct {
	Actor  string
	Before *string
	After  map[string]any
}

func readCarryAudit(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID) []carryAuditRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT actor, before::text, after
		  FROM framework_posture_target_audit_log
		 WHERE tenant_id = $1 AND framework_version_id = $2 AND action = 'carry_over'
		 ORDER BY occurred_at, id`, tenant, fvID)
	if err != nil {
		t.Fatalf("read carry audit: %v", err)
	}
	defer rows.Close()
	var out []carryAuditRow
	for rows.Next() {
		var r carryAuditRow
		var after []byte
		if err := rows.Scan(&r.Actor, &r.Before, &after); err != nil {
			t.Fatalf("scan carry audit: %v", err)
		}
		if err := json.Unmarshal(after, &r.After); err != nil {
			t.Fatalf("decode after: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("carry audit rows: %v", err)
	}
	return out
}

func TestPostureTargetCarry_ActivationCopiesAndAudits(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	f := seedCarryFixture(t, admin)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	seedPostureTarget(t, admin, tenant, f.FromVersionID, postureVals{
		Target: 92.5, Warning: 80, Critical: 60.25, Direction: "higher_is_better", UpdatedBy: "person-a",
	})
	if got := readPostureTarget(t, admin, tenant, f.ToVersionID); got != nil {
		t.Fatalf("N+1 has a target before activation: %+v", got)
	}

	scope := mustActivate(t, ctx, store, f.ToVersionID, "N+1 scope", `{"op":"true"}`)

	got := readPostureTarget(t, admin, tenant, f.ToVersionID)
	if got == nil {
		t.Fatal("activation on N+1 did not carry the N target")
	}
	want := postureVals{Target: 92.5, Warning: 80, Critical: 60.25, Direction: "higher_is_better",
		UpdatedBy: frameworkscope.PostureTargetCarryActor}
	if *got != want {
		t.Fatalf("carried target = %+v; want %+v", *got, want)
	}

	audit := readCarryAudit(t, admin, tenant, f.ToVersionID)
	if len(audit) != 1 {
		t.Fatalf("carry_over audit rows = %d; want 1", len(audit))
	}
	a := audit[0]
	if a.Actor != frameworkscope.PostureTargetCarryActor {
		t.Errorf("audit actor = %q; want %q", a.Actor, frameworkscope.PostureTargetCarryActor)
	}
	if a.Before != nil {
		t.Errorf("audit before = %q; want NULL", *a.Before)
	}
	if a.After["target_value"] != 92.5 || a.After["warning_threshold"] != 80.0 ||
		a.After["critical_threshold"] != 60.25 || a.After["direction"] != "higher_is_better" {
		t.Errorf("audit after values = %v", a.After)
	}
	src, _ := a.After["carried_over_from"].(map[string]any)
	if src["framework_version_id"] != f.FromVersionID.String() || src["framework_version"] != "2017" {
		t.Errorf("audit carried_over_from = %v; want version %s (2017)", src, f.FromVersionID)
	}
	if a.After["activated_by"] != "key_test" {
		t.Errorf("audit activated_by = %v; want key_test", a.After["activated_by"])
	}
	if a.After["framework_scope_id"] != scope.ID.String() {
		t.Errorf("audit framework_scope_id = %v; want %s", a.After["framework_scope_id"], scope.ID)
	}

	// The source is never written.
	if src := readPostureTarget(t, admin, tenant, f.FromVersionID); src == nil || src.UpdatedBy != "person-a" {
		t.Errorf("N target changed: %+v", src)
	}

	// Re-activation (a new revision on N+1) is idempotent: a target someone
	// has since edited stays, and no second carry is logged.
	if _, err := admin.Exec(context.Background(), `
		UPDATE framework_posture_targets SET target_value = 95, updated_by = 'person-b'
		 WHERE tenant_id = $1 AND framework_version_id = $2`, tenant, f.ToVersionID); err != nil {
		t.Fatalf("edit N+1 target: %v", err)
	}
	mustActivate(t, ctx, store, f.ToVersionID, "N+1 scope v2", `{"op":"true"}`)
	if got := readPostureTarget(t, admin, tenant, f.ToVersionID); got == nil || got.Target != 95 || got.UpdatedBy != "person-b" {
		t.Errorf("re-activation overwrote the N+1 target: %+v", got)
	}
	if n := len(readCarryAudit(t, admin, tenant, f.ToVersionID)); n != 1 {
		t.Errorf("carry_over audit rows after re-activation = %d; want 1", n)
	}
}

func TestPostureTargetCarry_ExistingTargetNotOverwritten(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	f := seedCarryFixture(t, admin)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	seedPostureTarget(t, admin, tenant, f.FromVersionID, postureVals{
		Target: 90, Warning: 80, Critical: 60, Direction: "higher_is_better", UpdatedBy: "person-a",
	})
	seedPostureTarget(t, admin, tenant, f.ToVersionID, postureVals{
		Target: 70, Warning: 50, Critical: 40, Direction: "higher_is_better", UpdatedBy: "person-b",
	})

	mustActivate(t, ctx, store, f.ToVersionID, "N+1 scope", `{"op":"true"}`)

	want := postureVals{Target: 70, Warning: 50, Critical: 40, Direction: "higher_is_better", UpdatedBy: "person-b"}
	if got := readPostureTarget(t, admin, tenant, f.ToVersionID); got == nil || *got != want {
		t.Fatalf("N+1 target = %+v; want it left as %+v", got, want)
	}
	if n := len(readCarryAudit(t, admin, tenant, f.ToVersionID)); n != 0 {
		t.Errorf("carry_over audit rows = %d; want 0", n)
	}
}

func TestPostureTargetCarry_TenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	f := seedCarryFixture(t, admin)
	store := frameworkscope.NewStore(app)
	ctxA, _ := tenancy.WithTenant(context.Background(), tenantA)
	ctxB, _ := tenancy.WithTenant(context.Background(), tenantB)

	// Only B has a target on N. A's activation on N+1 must not copy it.
	seedPostureTarget(t, admin, tenantB, f.FromVersionID, postureVals{
		Target: 88, Warning: 77, Critical: 66, Direction: "higher_is_better", UpdatedBy: "person-b",
	})
	mustActivate(t, ctxA, store, f.ToVersionID, "A N+1 scope", `{"op":"true"}`)
	if got := readPostureTarget(t, admin, tenantA, f.ToVersionID); got != nil {
		t.Fatalf("tenant A got a target from tenant B's N: %+v", got)
	}
	if got := readPostureTarget(t, admin, tenantB, f.ToVersionID); got != nil {
		t.Fatalf("tenant A's activation wrote tenant B's N+1: %+v", got)
	}
	if n := len(readCarryAudit(t, admin, tenantA, f.ToVersionID)); n != 0 {
		t.Errorf("tenant A carry_over audit rows = %d; want 0", n)
	}

	// B's own activation carries B's target, and only into B.
	mustActivate(t, ctxB, store, f.ToVersionID, "B N+1 scope", `{"op":"true"}`)
	if got := readPostureTarget(t, admin, tenantB, f.ToVersionID); got == nil || got.Target != 88 {
		t.Fatalf("tenant B N+1 target = %+v; want 88 carried", got)
	}
	if got := readPostureTarget(t, admin, tenantA, f.ToVersionID); got != nil {
		t.Fatalf("tenant B's carry wrote tenant A: %+v", got)
	}
	if n := len(readCarryAudit(t, admin, tenantB, f.ToVersionID)); n != 1 {
		t.Errorf("tenant B carry_over audit rows = %d; want 1", n)
	}
}

func TestPostureTargetCarry_NoMigrationNoCopy(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	// A version no migration leads into has no prior target: activation
	// writes nothing.
	fvID := seedFrameworkVersion(t, admin, tenant)
	mustActivate(t, ctx, store, fvID, "lone scope", `{"op":"true"}`)
	if got := readPostureTarget(t, admin, tenant, fvID); got != nil {
		t.Fatalf("version with no migration got a target: %+v", got)
	}
}
