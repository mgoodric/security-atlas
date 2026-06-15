//go:build integration

// Integration tests for slice 512: OSCAL component-definition import
// (vendor-claim ingest direction).
//
// These run against a REAL Postgres (RLS enforced via the atlas_app role)
// and the REAL Python oscal-bridge subprocess. The bridge is optional: if
// Python / compliance-trestle is not installed the bridge-dependent tests
// skip with a clear marker (the slice-030 D2 pattern). The load-bearing
// Go-side reconciliation + vendor-claim persistence logic is testable
// WITHOUT the bridge via the pure-Go helpers_test.go suite; these tests
// prove the end-to-end import + the no-auto-satisfy invariant + tenant
// isolation.
//
// Run with:
//   go test -tags=integration -p 1 ./internal/oscal/componentimport/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS), seeds fixtures.
//   DATABASE_URL_APP  - application role DSN, the Importer runs under it.
// Optional env:
//   OSCAL_BRIDGE_PYTHON - python interpreter with compliance-trestle +
//                         grpcio (defaults to "python3").

package componentimport_test

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

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/oscal/componentimport"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Slice 435 / 742: the appDSN/adminDSN/openPool/ctxFor pool/DSN/tenant-context
// boilerplate this file used to re-derive now lives in the shared
// internal/dbtest harness. dbtest.NewAppPool opens the RLS-enforcing atlas_app
// pool (the default for every RLS-bound assertion); dbtest.NewMigratePool opens
// the privileged BYPASSRLS pool used only for cross-tenant seeding and the
// freshTenant cleanup the app role cannot perform; dbtest.WithTenantCtx tags the
// tenant GUC context. The in-tx tenancy.ApplyTenant GUC wiring in the test
// bodies is unchanged.

// freshTenant returns a fresh tenant id and registers cleanup of the slice-512
// + slice-492-shared imported tables (children before parent) via the
// privileged migrate pool.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"imported_component_claims",
		"imported_components",
		"imported_catalog_audit_log",
		"imported_catalogs",
	)
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// startBridge starts the Python oscal-bridge on a free loopback port.
// Skips the test if the bridge cannot start (slice-030 D2 pattern).
func startBridge(t *testing.T) (string, func()) {
	t.Helper()
	py := os.Getenv("OSCAL_BRIDGE_PYTHON")
	if py == "" {
		py = "python3"
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	repoRoot, err := filepath.Abs("../../..")
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
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			return addr, func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Skipf("oscal-bridge exited during startup — skipping bridge-dependent test")
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Skipf("oscal-bridge did not become ready on %s — skipping bridge-dependent test", addr)
	return "", func() {}
}

// seedCurrentSCFAnchor seeds a minimal 'scf'-slug framework with a CURRENT
// version carrying one anchor whose scf_id matches a vendor-claim control id
// ("IAC-06"). Proves the deterministic requirement -> SCF anchor mapping
// (slice-512 D3). scf_anchors is global — seeded idempotently.
func seedCurrentSCFAnchor(t *testing.T, admin *pgxpool.Pool, scfID string) {
	t.Helper()
	ctx := context.Background()
	fwID := uuid.NewString()
	verID := uuid.NewString()
	slug := "scf"
	var existingVer string
	err := admin.QueryRow(ctx, `
		SELECT fv.id::text FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND fv.status = 'current' LIMIT 1`, slug).Scan(&existingVer)
	if err == nil && existingVer != "" {
		verID = existingVer
	} else {
		var realFwID string
		qerr := admin.QueryRow(ctx, `SELECT id::text FROM frameworks WHERE slug = $1 AND tenant_id IS NULL LIMIT 1`, slug).Scan(&realFwID)
		if qerr != nil {
			realFwID = fwID
			if _, err := admin.Exec(ctx,
				`INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
				 VALUES ($1, NULL, 'Secure Controls Framework', $2, 'SCF Council')`, realFwID, slug); err != nil {
				t.Fatalf("seed framework: %v", err)
			}
		}
		if _, err := admin.Exec(ctx,
			`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
			 VALUES ($1, NULL, $2, 'test-2026', 'current')`, verID, realFwID); err != nil {
			t.Fatalf("seed framework_version: %v", err)
		}
	}
	if _, err := admin.Exec(ctx,
		`INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		 VALUES ($1, $2, $3, 'IAC', 'Multi-Factor Authentication')
		 ON CONFLICT (framework_version_id, scf_id) DO NOTHING`,
		uuid.NewString(), verID, scfID); err != nil {
		t.Fatalf("seed scf anchor: %v", err)
	}
}

// ===== AC-10: end-to-end import of a real component-definition =====

func TestImportComponentDefinition_ImportsEndToEnd(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	seedCurrentSCFAnchor(t, admin, "IAC-06")

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	im := componentimport.NewImporter(app, bridge)
	report, err := im.Import(dbtest.WithTenantCtx(t, tenant), componentimport.Request{
		OscalJSON:   loadFixture(t, "component_definition.json"),
		SourceLabel: "Acme Cloud",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	// Two components, three vendor claims (IAC-06, ac-2, au-2).
	if report.ComponentCount != 2 {
		t.Errorf("ComponentCount = %d, want 2", report.ComponentCount)
	}
	if report.ClaimCount != 3 {
		t.Errorf("ClaimCount = %d, want 3 (IAC-06, ac-2, au-2)", report.ClaimCount)
	}
	// Only IAC-06 deterministically matched the seeded SCF anchor (D3).
	if report.MappedCount != 1 {
		t.Errorf("MappedCount = %d, want 1 (IAC-06 -> SCF anchor)", report.MappedCount)
	}
	if report.OSCALVersion == "" {
		t.Error("OSCALVersion must be echoed from the validated document")
	}
	if report.Title != "Acme Cloud Platform Component Definition" {
		t.Errorf("Title = %q, want the declared metadata title", report.Title)
	}

	ctx := context.Background()
	// The import is persisted as a component_definition-kind,
	// component-source provenance row.
	var (
		source, kind, importedBy, sha, title string
		count                                int
	)
	if err := admin.QueryRow(ctx,
		`SELECT source, kind, imported_by, source_sha256, catalog_title, control_count
		 FROM imported_catalogs WHERE id = $1 AND tenant_id = $2`,
		report.ImportID, tenant).Scan(&source, &kind, &importedBy, &sha, &title, &count); err != nil {
		t.Fatalf("read imported_catalogs: %v", err)
	}
	if source != "oscal-component-import" {
		t.Errorf("source = %q, want oscal-component-import", source)
	}
	if kind != "component_definition" {
		t.Errorf("kind = %q, want component_definition", kind)
	}
	if importedBy != "grc-tester" {
		t.Errorf("imported_by = %q, want grc-tester", importedBy)
	}
	if sha != report.SourceSha256 || len(sha) != 64 {
		t.Errorf("source_sha256 = %q (report %q)", sha, report.SourceSha256)
	}

	// Components persisted.
	var compCount int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM imported_components WHERE imported_catalog_id = $1`,
		report.ImportID).Scan(&compCount); err != nil {
		t.Fatalf("read imported_components: %v", err)
	}
	if compCount != 2 {
		t.Errorf("imported_components = %d, want 2", compCount)
	}

	// Claims: IAC-06 mapped to the SCF anchor; the other two NULL. EVERY claim
	// is a vendor claim ('asserted') — P0-512-1.
	var mapped, total, asserted, vendorClaims int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE c.scf_anchor_id IS NOT NULL),
		        count(*),
		        count(*) FILTER (WHERE c.claim_status = 'asserted'),
		        count(*) FILTER (WHERE c.is_vendor_claim = TRUE)
		 FROM imported_component_claims c
		 JOIN imported_components ic ON ic.id = c.imported_component_id
		 WHERE ic.imported_catalog_id = $1`,
		report.ImportID).Scan(&mapped, &total, &asserted, &vendorClaims); err != nil {
		t.Fatalf("read imported_component_claims: %v", err)
	}
	if total != 3 || mapped != 1 {
		t.Errorf("claims: mapped=%d total=%d, want mapped=1 total=3", mapped, total)
	}
	// AC-7 / P0-512-1: imported claims are vendor assertions ('asserted'),
	// NEVER auto-accepted.
	if asserted != 3 {
		t.Errorf("all 3 claims must be status 'asserted' at import, got %d", asserted)
	}
	if vendorClaims != 3 {
		t.Errorf("all 3 rows must be is_vendor_claim=TRUE, got %d", vendorClaims)
	}

	// The IAC-06 claim mapped to the SCF anchor.
	var anchorID *string
	if err := admin.QueryRow(ctx,
		`SELECT c.scf_anchor_id FROM imported_component_claims c
		 JOIN imported_components ic ON ic.id = c.imported_component_id
		 WHERE ic.imported_catalog_id = $1 AND c.control_id = 'IAC-06'`,
		report.ImportID).Scan(&anchorID); err != nil {
		t.Fatalf("read IAC-06 mapping: %v", err)
	}
	if anchorID == nil || *anchorID != "IAC-06" {
		t.Errorf("IAC-06 scf_anchor_id = %v, want IAC-06 (requirement -> SCF anchor)", anchorID)
	}

	// AC-8: a success audit row exists with the component-definition action.
	var auditAction string
	if err := admin.QueryRow(ctx,
		`SELECT action FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND catalog_id = $2`,
		tenant, report.ImportID).Scan(&auditAction); err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if auditAction != "component_definition_imported" {
		t.Errorf("audit action = %q, want component_definition_imported", auditAction)
	}

	// P0-512-5: the bundled SCF spine was not mutated by the import (the
	// claim control ids must not leak into scf_anchors).
	var leaked int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM scf_anchors WHERE scf_id IN ('ac-2','au-2')`).Scan(&leaked); err != nil {
		t.Fatalf("read scf_anchors: %v", err)
	}
	if leaked != 0 {
		t.Errorf("import leaked %d claim control ids into scf_anchors (P0-512-5 violation)", leaked)
	}
}

// ===== AC-13 (P0-512-1): an imported claim marks NO control satisfied =====

func TestImportComponentDefinition_DoesNotSatisfyAnyControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	seedCurrentSCFAnchor(t, admin, "IAC-06")

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	// Count control_evaluations before + after the import: an imported vendor
	// claim must NOT produce a control evaluation (invariant #2 / P0-512-1 —
	// ingestion never writes to the evaluation surface).
	ctx := context.Background()
	var before int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM control_evaluations WHERE tenant_id = $1`, tenant).Scan(&before); err != nil {
		t.Fatalf("count control_evaluations before: %v", err)
	}

	im := componentimport.NewImporter(app, bridge)
	report, err := im.Import(dbtest.WithTenantCtx(t, tenant), componentimport.Request{
		OscalJSON:   loadFixture(t, "component_definition.json"),
		SourceLabel: "Acme Cloud",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var after int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM control_evaluations WHERE tenant_id = $1`, tenant).Scan(&after); err != nil {
		t.Fatalf("count control_evaluations after: %v", err)
	}
	if after != before {
		t.Errorf("import wrote %d control_evaluations rows (P0-512-1 violation: a vendor claim must not satisfy a control)", after-before)
	}

	// And no claim row is 'accepted' — the operator action (existing) is what
	// can accept; the import never does.
	var accepted int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM imported_component_claims c
		 JOIN imported_components ic ON ic.id = c.imported_component_id
		 WHERE ic.imported_catalog_id = $1 AND c.claim_status = 'accepted'`,
		report.ImportID).Scan(&accepted); err != nil {
		t.Fatalf("count accepted claims: %v", err)
	}
	if accepted != 0 {
		t.Errorf("import auto-accepted %d claims (P0-512-1 violation)", accepted)
	}
}

// ===== AC-11: a malformed component-definition rolls back, nothing persists =====

func TestImportComponentDefinition_MalformedPersistsNothing(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	im := componentimport.NewImporter(app, bridge)
	_, err = im.Import(dbtest.WithTenantCtx(t, tenant), componentimport.Request{
		OscalJSON:   loadFixture(t, "component_definition_malformed.json"),
		SourceLabel: "bad",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if !errors.Is(err, componentimport.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}

	ctx := context.Background()
	var importCount, compCount, claimCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_catalogs WHERE tenant_id = $1`, tenant).Scan(&importCount); err != nil {
		t.Fatalf("count imports: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_components WHERE tenant_id = $1`, tenant).Scan(&compCount); err != nil {
		t.Fatalf("count components: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_component_claims WHERE tenant_id = $1`, tenant).Scan(&claimCount); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if importCount != 0 || compCount != 0 || claimCount != 0 {
		t.Errorf("malformed import must persist nothing: imports=%d components=%d claims=%d", importCount, compCount, claimCount)
	}

	// AC-8: a rejection audit row WAS written (separate committed tx).
	var rejectCount int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND action = 'component_definition_import_rejected'`, tenant).Scan(&rejectCount); err != nil {
		t.Fatalf("count rejections: %v", err)
	}
	if rejectCount != 1 {
		t.Errorf("component_definition_import_rejected audit rows = %d, want 1", rejectCount)
	}
}

// ===== AC-12: tenant isolation — Tenant A's import never lands under Tenant B =====

func TestImportComponentDefinition_TenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	im := componentimport.NewImporter(app, bridge)
	report, err := im.Import(dbtest.WithTenantCtx(t, tenantA), componentimport.Request{
		OscalJSON:   loadFixture(t, "component_definition.json"),
		SourceLabel: "Tenant A vendor",
		ImportedBy:  "grc-a",
		Role:        authz.RoleGRCEngineer,
	})
	if err != nil {
		t.Fatalf("Import (tenant A): %v", err)
	}

	// Tenant B, running under RLS via the app role, sees NOTHING of A's import.
	ctx := context.Background()
	bCtx := dbtest.WithTenantCtx(t, tenantB)
	tx, err := app.Begin(bCtx)
	if err != nil {
		t.Fatalf("begin tx as tenant B: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(bCtx, tx); err != nil {
		t.Fatalf("apply tenant B: %v", err)
	}
	var visibleImports, visibleComponents, visibleClaims int
	if err := tx.QueryRow(bCtx,
		`SELECT count(*) FROM imported_catalogs WHERE id = $1`, report.ImportID).Scan(&visibleImports); err != nil {
		t.Fatalf("tenant B read imports: %v", err)
	}
	if err := tx.QueryRow(bCtx,
		`SELECT count(*) FROM imported_components WHERE imported_catalog_id = $1`,
		report.ImportID).Scan(&visibleComponents); err != nil {
		t.Fatalf("tenant B read components: %v", err)
	}
	if err := tx.QueryRow(bCtx,
		`SELECT count(*) FROM imported_component_claims WHERE tenant_id = $1`,
		tenantA).Scan(&visibleClaims); err != nil {
		t.Fatalf("tenant B read claims: %v", err)
	}
	if visibleImports != 0 || visibleComponents != 0 || visibleClaims != 0 {
		t.Errorf("RLS leak: tenant B sees A's import=%d components=%d claims=%d (want 0)", visibleImports, visibleComponents, visibleClaims)
	}
}
