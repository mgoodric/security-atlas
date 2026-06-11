//go:build integration

// Integration tests for slice 030: OSCAL SSP + POA&M export pipeline.
//
// These run against a REAL Postgres (RLS enforced via the app role) and,
// where available, a REAL Python oscal-bridge subprocess. The bridge is
// optional: if Python or compliance-trestle is not installed the
// bridge-dependent tests skip with a clear marker (decision D2). The
// Aggregate-side tests — including the constitutional invariant-10
// "refuse a non-frozen period" check — run with no bridge at all.
//
// Run with: go test -tags=integration -race ./internal/oscal/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS), seeds fixtures.
//   DATABASE_URL_APP  - application role DSN, the Exporter runs under it.
// Optional env:
//   OSCAL_BRIDGE_PYTHON - python interpreter with compliance-trestle +
//                         grpcio (defaults to "python3"); if the bridge
//                         cannot start, bridge-dependent tests skip.

package oscal_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
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
	t.Cleanup(pool.Close)
	return pool
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// freshTenant returns a tenant id and registers cleanup of every table
// slice 030 reads from.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_notes WHERE tenant_id = $1`,
			`DELETE FROM walkthroughs WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`UPDATE populations SET audit_period_id = NULL, frozen_at = NULL WHERE tenant_id = $1`,
			`DELETE FROM populations WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM policies WHERE tenant_id = $1`,
			`DELETE FROM scope_cells WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
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
	fwID, versionID := uuid.New(), uuid.New()
	slug := fmt.Sprintf("slice030-%s", uuid.NewString()[:8])
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		 VALUES ($1, NULL, 'Slice 030 test framework', $2, 'test')`, fwID, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		 VALUES ($1, NULL, $2, '1.0')`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	return versionID
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, family string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id, owner_role)
		 VALUES ($1, $2, $3, $4, 'automated', $5, 'control_owner')`,
		id, tenant, "Slice 030 control "+family, family, "bundle-030-"+family); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

func seedPeriod(t *testing.T, admin *pgxpool.Pool, tenant string, fwVersion uuid.UUID, frozen bool) (uuid.UUID, time.Time) {
	t.Helper()
	id := uuid.New()
	frozenAt := time.Now().UTC().Add(-24 * time.Hour)
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO audit_periods (id, tenant_id, name, framework_version_id, period_start, period_end, status, created_by)
		 VALUES ($1, $2, 'SOC 2 2026 Q2', $3, $4, $5, 'open', 'tester')`,
		id, tenant, fwVersion,
		frozenAt.Add(-90*24*time.Hour), frozenAt); err != nil {
		t.Fatalf("seed audit period: %v", err)
	}
	if frozen {
		if _, err := admin.Exec(context.Background(),
			`UPDATE audit_periods SET status='frozen', frozen_at=$2, frozen_by='tester',
			        frozen_hash=decode('00','hex') WHERE id=$1`,
			id, frozenAt); err != nil {
			t.Fatalf("freeze audit period: %v", err)
		}
	}
	return id, frozenAt
}

// startBridge starts the Python oscal-bridge on a free loopback port.
// Returns the address and a stop func. Skips the test if the bridge
// cannot start (Python / trestle not installed) — decision D2.
func startBridge(t *testing.T) (addr string, stop func()) {
	t.Helper()
	py := os.Getenv("OSCAL_BRIDGE_PYTHON")
	if py == "" {
		py = "python3"
	}
	// Pick a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	addr = fmt.Sprintf("127.0.0.1:%d", port)

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	bridgeDir := filepath.Join(repoRoot, "oscal-bridge")

	cmd := exec.Command(py, "-m", "atlas_oscal_bridge.server", "--address", addr)
	cmd.Dir = bridgeDir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+bridgeDir)
	if err := cmd.Start(); err != nil {
		t.Skipf("oscal-bridge could not start (%s): %v — skipping bridge-dependent test", py, err)
	}
	// Poll the port until the gRPC server is accepting connections.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			return addr, func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }
		}
		// If the process already exited, the bridge env is unusable.
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Skipf("oscal-bridge exited during startup — skipping bridge-dependent test")
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Skipf("oscal-bridge did not become ready on %s — skipping bridge-dependent test", addr)
	return "", func() {}
}

// ===== Invariant 10: refuse a non-frozen period (no bridge needed) =====

func TestExport_RefusesNonFrozenPeriod(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, _ := seedPeriod(t, admin, tenant, fwVersion, false /* not frozen */)

	signer, _ := oscal.NewEphemeralSigner()
	// A nil bridge is fine — Aggregate must reject the period BEFORE any
	// bridge call. This is the constitutional invariant-10 enforcement.
	e := oscal.NewExporter(app, nil, signer)
	_, err := e.Export(ctxFor(t, tenant), oscal.ExportInput{
		AuditPeriodID: periodID,
		SystemName:    "Test System",
	})
	if !errors.Is(err, oscal.ErrPeriodNotFrozen) {
		t.Fatalf("expected ErrPeriodNotFrozen for an open period, got %v", err)
	}
}

func TestExport_RejectsUnknownPeriod(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	signer, _ := oscal.NewEphemeralSigner()
	e := oscal.NewExporter(app, nil, signer)
	_, err := e.Export(ctxFor(t, tenant), oscal.ExportInput{
		AuditPeriodID: uuid.New(),
		SystemName:    "Test System",
	})
	if !errors.Is(err, oscal.ErrPeriodNotFound) {
		t.Fatalf("expected ErrPeriodNotFound, got %v", err)
	}
}

// ===== Slice 457: cross-tenant download isolation (invariant #6) =====
//
// The slice-457 browser download surface
// (POST /v1/audit-periods/{id}/oscal-export:download) serves the signed
// bundle as a downloadable artifact. Its headline threat is
// information-disclosure across tenants: a Tenant-B operator must NOT be
// able to download Tenant-A's bundle. The download handler adds no
// cross-tenant reach — it reuses this exact Exporter, which reads under
// the request's tenant context. This test pins the property at the
// authoritative RLS boundary: a frozen period seeded for tenant A,
// exported under tenant B's context, must resolve to ErrPeriodNotFound
// (the same denial the download serves as a 404). No bridge is needed —
// RLS denies the read in the Aggregate stage, before any bridge call.
func TestExport_CrossTenantPeriodIsNotExportable(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)

	// A FROZEN period that genuinely exists — under tenant A.
	periodID, _ := seedPeriod(t, admin, tenantA, fwVersion, true /* frozen */)

	signer, _ := oscal.NewEphemeralSigner()
	e := oscal.NewExporter(app, nil, signer)

	// Sanity: tenant A CAN see its own frozen period reaches the bridge
	// stage (a nil bridge surfaces as ErrBridgeUnavailable — NOT
	// ErrPeriodNotFound). This proves the period resolves for its owner,
	// so the cross-tenant denial below is genuine isolation, not a
	// not-found that would fire for anyone.
	_, ownErr := e.Export(ctxFor(t, tenantA), oscal.ExportInput{
		AuditPeriodID: periodID,
		SystemName:    "Tenant A System",
	})
	if errors.Is(ownErr, oscal.ErrPeriodNotFound) {
		t.Fatalf("tenant A could not see its OWN frozen period; isolation test is invalid: %v", ownErr)
	}

	// The isolation assertion: tenant B asks to export tenant A's period.
	// RLS scopes the read to tenant B, which has no such period -> the
	// Aggregate stage returns ErrPeriodNotFound. Tenant B never sees a
	// byte of tenant A's bundle.
	_, err := e.Export(ctxFor(t, tenantB), oscal.ExportInput{
		AuditPeriodID: periodID,
		SystemName:    "Tenant B System",
	})
	if !errors.Is(err, oscal.ErrPeriodNotFound) {
		t.Fatalf("cross-tenant export of tenant A's period under tenant B should be ErrPeriodNotFound, got %v", err)
	}
}

// ===== Full pipeline against the real Python bridge =====

func TestExport_FrozenPeriodProducesSignedBundle(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)

	// Seed a control, a scope cell, a policy, a failing evaluation, a
	// walkthrough, and an audit note — one of each input the SSP / AP /
	// AR / POA&M draw from.
	ctrlID := seedControl(t, admin, tenant, "IAC")
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
		 VALUES ($1,$2,'prod/us-east','{"env":"prod"}'::jsonb,'h1')`,
		uuid.New(), tenant); err != nil {
		t.Fatalf("seed scope cell: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO policies (id, tenant_id, title, version, body_md, status, owner_role, approver_role, created_by, effective_date)
		 VALUES ($1,$2,'Access Control Policy','2.0','# body','published','grc_engineer','ciso','tester', CURRENT_DATE)`,
		uuid.New(), tenant); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	// A failing control evaluation as of before the frozen horizon -> POA&M item.
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO control_evaluations
		   (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at, result, freshness_status, evidence_count_in_window, trigger)
		 VALUES ($1,$2,$3,NULL,$4,$5,'fail','stale',0,'manual')`,
		uuid.New(), tenant, ctrlID, uuid.New(), frozenAt.Add(-1*time.Hour)); err != nil {
		t.Fatalf("seed failing evaluation: %v", err)
	}
	// A walkthrough pinned to the period -> AR observation.
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO walkthroughs
		   (id, tenant_id, audit_period_id, control_id, narrative, canonical_hash, status, created_by)
		 VALUES ($1,$2,$3,$4,'Auditor observed the control.',sha256('walkthrough-030'::bytea),'finalized','tester')`,
		uuid.New(), tenant, periodID, ctrlID); err != nil {
		t.Fatalf("seed walkthrough: %v", err)
	}
	// An audit note on the period -> AR observation annotation.
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO audit_notes
		   (id, tenant_id, audit_period_id, author_user_id, scope_type, scope_id, body, visibility)
		 VALUES ($1,$2,$3,'auditor','control',$4,'Please clarify break-glass coverage.','auditor_only')`,
		uuid.New(), tenant, periodID, ctrlID.String()); err != nil {
		t.Fatalf("seed audit note: %v", err)
	}

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	signer, _ := oscal.NewEphemeralSigner()
	e := oscal.NewExporter(app, bridge, signer)

	bundle, err := e.Export(ctxFor(t, tenant), oscal.ExportInput{
		AuditPeriodID:     periodID,
		OrganizationName:  "Acme Security Inc.",
		SystemName:        "Acme Compliance Platform",
		SystemDescription: "The SaaS platform under SOC 2 assessment.",
		RequestedBy:       "tester",
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Four OSCAL members.
	if len(bundle.Members) != 4 {
		t.Fatalf("bundle has %d members, want 4", len(bundle.Members))
	}
	// AC-5: bundle is signed and the signature verifies.
	if bundle.Signature.Algorithm != "ed25519" {
		t.Errorf("bundle signature algorithm = %q, want ed25519", bundle.Signature.Algorithm)
	}
	if err := oscal.VerifyBundle(bundle); err != nil {
		t.Errorf("VerifyBundle: %v", err)
	}
	// The frozen horizon is carried in the bundle.
	if bundle.FrozenAt == "" {
		t.Error("bundle.FrozenAt must be populated from the frozen period")
	}

	// WriteBundle persists all members + a signed manifest.
	dir := t.TempDir()
	manifestPath, err := bundle.WriteBundle(dir)
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	for _, name := range []string{"ssp.json", "assessment-plan.json", "assessment-results.json", "poam.json", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("bundle missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest path not written: %v", err)
	}
}
