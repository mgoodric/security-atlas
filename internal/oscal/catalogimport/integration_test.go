//go:build integration

// Integration tests for slice 492: OSCAL catalog import.
//
// These run against a REAL Postgres (RLS enforced via the atlas_app role)
// and the REAL Python oscal-bridge subprocess. The bridge is optional: if
// Python / compliance-trestle is not installed the bridge-dependent tests
// skip with a clear marker (the slice-030 D2 pattern).
//
// Run with:
//   go test -tags=integration -p 1 ./internal/oscal/catalogimport/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS), seeds fixtures.
//   DATABASE_URL_APP  - application role DSN, the Importer runs under it.
// Optional env:
//   OSCAL_BRIDGE_PYTHON - python interpreter with compliance-trestle +
//                         grpcio (defaults to "python3").

package catalogimport_test

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
	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/oscal/catalogimport"
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

// freshTenant returns a fresh tenant id and registers cleanup of the three
// slice-492 tables.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM imported_catalog_audit_log WHERE tenant_id = $1`,
			`DELETE FROM imported_catalog_controls WHERE tenant_id = $1`,
			`DELETE FROM imported_catalogs WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
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
// version carrying one anchor whose scf_id matches a control id in the
// valid fixture ("IAC-06"). This proves the deterministic requirement ->
// SCF-anchor mapping (D1) without depending on a full SCF catalog import.
// scf_anchors is global (no tenant) — seeded once, idempotently.
func seedCurrentSCFAnchor(t *testing.T, admin *pgxpool.Pool, scfID string) {
	t.Helper()
	ctx := context.Background()
	fwID := uuid.NewString()
	verID := uuid.NewString()
	slug := "scf"
	// Reuse an existing 'scf' framework + current version if present;
	// otherwise create them. The unique (framework_version_id, scf_id)
	// constraint makes the anchor insert idempotent via ON CONFLICT.
	var existingVer string
	err := admin.QueryRow(ctx, `
		SELECT fv.id::text FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND fv.status = 'current' LIMIT 1`, slug).Scan(&existingVer)
	if err == nil && existingVer != "" {
		verID = existingVer
	} else {
		// Resolve (or create) the 'scf' framework. The UNIQUE constraint is
		// (tenant_id, slug); for the global tenant_id=NULL row we query-first
		// to avoid duplicate NULL-tenant frameworks (NULLs are distinct under
		// UNIQUE, so ON CONFLICT would not fire).
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

// ===== AC-9: end-to-end import of a real OSCAL catalog =====

func TestImport_ValidCatalogEndToEnd(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	seedCurrentSCFAnchor(t, admin, "IAC-06")

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	im := catalogimport.NewImporter(app, bridge)
	report, err := im.Import(ctxFor(t, tenant), catalogimport.Request{
		OscalJSON:   loadFixture(t, "catalog_minimal_valid.json"),
		SourceLabel: "NIST 800-53 test",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.ControlCount != 3 {
		t.Errorf("ControlCount = %d, want 3", report.ControlCount)
	}
	// IAC-06 deterministically matched the seeded SCF anchor (D1).
	if report.MappedCount != 1 {
		t.Errorf("MappedCount = %d, want 1 (IAC-06 -> SCF anchor)", report.MappedCount)
	}
	if report.OSCALVersion == "" {
		t.Error("OSCALVersion must be echoed from the validated document")
	}

	// The imported set is present, provenance-labeled, mapped to SCF anchors.
	ctx := context.Background()
	var (
		source, importedBy, sha, label string
		count                          int
	)
	if err := admin.QueryRow(ctx,
		`SELECT source, imported_by, source_sha256, source_label, control_count
		 FROM imported_catalogs WHERE id = $1 AND tenant_id = $2`,
		report.CatalogID, tenant).Scan(&source, &importedBy, &sha, &label, &count); err != nil {
		t.Fatalf("read imported_catalogs: %v", err)
	}
	if source != "oscal-import" {
		t.Errorf("source = %q, want oscal-import", source)
	}
	if importedBy != "grc-tester" {
		t.Errorf("imported_by = %q, want grc-tester", importedBy)
	}
	if sha != report.SourceSha256 || len(sha) != 64 {
		t.Errorf("source_sha256 = %q (report %q)", sha, report.SourceSha256)
	}

	// IAC-06 control mapped to the SCF anchor; the other two are NULL.
	var mapped, total int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE scf_anchor_id IS NOT NULL), count(*)
		 FROM imported_catalog_controls WHERE imported_catalog_id = $1`,
		report.CatalogID).Scan(&mapped, &total); err != nil {
		t.Fatalf("read imported_catalog_controls: %v", err)
	}
	if total != 3 || mapped != 1 {
		t.Errorf("controls: mapped=%d total=%d, want mapped=1 total=3", mapped, total)
	}
	var anchorID *string
	if err := admin.QueryRow(ctx,
		`SELECT scf_anchor_id FROM imported_catalog_controls
		 WHERE imported_catalog_id = $1 AND source_control_id = 'IAC-06'`,
		report.CatalogID).Scan(&anchorID); err != nil {
		t.Fatalf("read IAC-06 mapping: %v", err)
	}
	if anchorID == nil || *anchorID != "IAC-06" {
		t.Errorf("IAC-06 scf_anchor_id = %v, want IAC-06 (requirement -> SCF anchor)", anchorID)
	}

	// AC-7: a success audit row exists.
	var auditAction string
	if err := admin.QueryRow(ctx,
		`SELECT action FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND catalog_id = $2`,
		tenant, report.CatalogID).Scan(&auditAction); err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if auditAction != "catalog_imported" {
		t.Errorf("audit action = %q, want catalog_imported", auditAction)
	}

	// P0-492-4: the bundled SCF spine was not mutated by the import — the
	// imported controls live in their own table, not scf_anchors. (The one
	// scf_anchors row present is the test seed, not an import write.)
	var scfRowsForTenant int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM scf_anchors WHERE scf_id = 'ac-1' OR scf_id = 'ac-2'`).Scan(&scfRowsForTenant); err != nil {
		t.Fatalf("read scf_anchors: %v", err)
	}
	if scfRowsForTenant != 0 {
		t.Errorf("import leaked %d imported controls into scf_anchors (P0-492-4 violation)", scfRowsForTenant)
	}
}

// ===== AC-10: malformed catalog -> transactional rollback, nothing persisted =====

func TestImport_MalformedCatalogPersistsNothing(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	im := catalogimport.NewImporter(app, bridge)
	_, err = im.Import(ctxFor(t, tenant), catalogimport.Request{
		OscalJSON:   loadFixture(t, "catalog_malformed.json"),
		SourceLabel: "bad",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if !errors.Is(err, catalogimport.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}

	// AC-5 / P0-492-3: NO catalog and NO control rows were persisted.
	ctx := context.Background()
	var catCount, ctlCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_catalogs WHERE tenant_id = $1`, tenant).Scan(&catCount); err != nil {
		t.Fatalf("count catalogs: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_catalog_controls WHERE tenant_id = $1`, tenant).Scan(&ctlCount); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	if catCount != 0 || ctlCount != 0 {
		t.Errorf("transactional rollback failed: catalogs=%d controls=%d, want 0/0", catCount, ctlCount)
	}

	// AC-7: a rejection audit row WAS written (separate committed tx).
	var rejectCount int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND action = 'import_rejected'`, tenant).Scan(&rejectCount); err != nil {
		t.Fatalf("count rejections: %v", err)
	}
	if rejectCount != 1 {
		t.Errorf("import_rejected audit rows = %d, want 1", rejectCount)
	}
}

// ===== AC-11: tenant isolation — Tenant A's import never lands under Tenant B =====

func TestImport_TenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	im := catalogimport.NewImporter(app, bridge)
	report, err := im.Import(ctxFor(t, tenantA), catalogimport.Request{
		OscalJSON:   loadFixture(t, "catalog_minimal_valid.json"),
		SourceLabel: "Tenant A catalog",
		ImportedBy:  "grc-a",
		Role:        authz.RoleGRCEngineer,
	})
	if err != nil {
		t.Fatalf("Import (tenant A): %v", err)
	}

	// Tenant B, running under RLS via the app role, sees NOTHING of A's import.
	ctx := context.Background()
	bCtx := ctxFor(t, tenantB)
	tx, err := app.Begin(bCtx)
	if err != nil {
		t.Fatalf("begin tx as tenant B: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(bCtx, tx); err != nil {
		t.Fatalf("apply tenant B: %v", err)
	}
	var visibleToB int
	if err := tx.QueryRow(bCtx,
		`SELECT count(*) FROM imported_catalogs WHERE id = $1`, report.CatalogID).Scan(&visibleToB); err != nil {
		t.Fatalf("tenant B read: %v", err)
	}
	if visibleToB != 0 {
		t.Errorf("RLS leak: tenant B sees %d of tenant A's imported catalog (want 0)", visibleToB)
	}
	var controlsVisibleToB int
	if err := tx.QueryRow(bCtx,
		`SELECT count(*) FROM imported_catalog_controls WHERE imported_catalog_id = $1`,
		report.CatalogID).Scan(&controlsVisibleToB); err != nil {
		t.Fatalf("tenant B control read: %v", err)
	}
	if controlsVisibleToB != 0 {
		t.Errorf("RLS leak: tenant B sees %d of tenant A's imported controls (want 0)", controlsVisibleToB)
	}
}
