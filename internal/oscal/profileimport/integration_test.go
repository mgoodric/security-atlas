//go:build integration

// Integration tests for slice 511: OSCAL profile import (resolve direction).
//
// These run against a REAL Postgres (RLS enforced via the atlas_app role)
// and the REAL Python oscal-bridge subprocess. The bridge is optional: if
// Python / compliance-trestle is not installed the bridge-dependent tests
// skip with a clear marker (the slice-030 D2 pattern). The load-bearing
// Go-side reconciliation logic is testable WITHOUT the bridge via the
// pure-Go helpers_test.go suite; these tests prove the end-to-end resolve.
//
// Run with:
//   go test -tags=integration -p 1 ./internal/oscal/profileimport/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS), seeds fixtures.
//   DATABASE_URL_APP  - application role DSN, the Importer runs under it.
// Optional env:
//   OSCAL_BRIDGE_PYTHON - python interpreter with compliance-trestle +
//                         grpcio (defaults to "python3").

package profileimport_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/oscal/profileimport"
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

// freshTenant returns a fresh tenant id and registers cleanup of the
// (slice-492-shared) imported tables.
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
// resolved profile ("IAC-06"). Proves the deterministic requirement -> SCF
// anchor mapping (slice-511 D3). scf_anchors is global — seeded idempotently.
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

// ===== AC-9: end-to-end resolve of a real FedRAMP-style profile =====

func TestImportProfile_ResolvesEndToEnd(t *testing.T) {
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

	im := profileimport.NewImporter(app, bridge)
	report, err := im.Import(ctxFor(t, tenant), profileimport.Request{
		ProfileJSON: loadFixture(t, "profile_baseline.json"),
		Catalogs:    [][]byte{loadFixture(t, "base_catalog.json")},
		SourceLabel: "FedRAMP Moderate test",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	// include-controls selected exactly ac-1, ac-2, IAC-06 (NOT ac-3).
	if report.ControlCount != 3 {
		t.Errorf("ControlCount = %d, want 3 (import selected ac-1, ac-2, IAC-06)", report.ControlCount)
	}
	// IAC-06 deterministically matched the seeded SCF anchor (D3).
	if report.MappedCount != 1 {
		t.Errorf("MappedCount = %d, want 1 (IAC-06 -> SCF anchor)", report.MappedCount)
	}
	if report.OSCALVersion == "" {
		t.Error("OSCALVersion must be echoed from the resolved document")
	}
	if report.ProfileTitle != "FedRAMP-style Test Moderate Baseline" {
		t.Errorf("ProfileTitle = %q, want the resolved profile's declared title", report.ProfileTitle)
	}

	ctx := context.Background()
	// The baseline is persisted as a profile-kind, profile-source set.
	var (
		source, kind, importedBy, sha, profTitle string
		count                                    int
	)
	if err := admin.QueryRow(ctx,
		`SELECT source, kind, imported_by, source_sha256, profile_title, control_count
		 FROM imported_catalogs WHERE id = $1 AND tenant_id = $2`,
		report.ProfileID, tenant).Scan(&source, &kind, &importedBy, &sha, &profTitle, &count); err != nil {
		t.Fatalf("read imported_catalogs: %v", err)
	}
	if source != "oscal-profile-import" {
		t.Errorf("source = %q, want oscal-profile-import", source)
	}
	if kind != "profile" {
		t.Errorf("kind = %q, want profile", kind)
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
		report.ProfileID).Scan(&mapped, &total); err != nil {
		t.Fatalf("read imported_catalog_controls: %v", err)
	}
	if total != 3 || mapped != 1 {
		t.Errorf("controls: mapped=%d total=%d, want mapped=1 total=3", mapped, total)
	}
	var anchorID *string
	if err := admin.QueryRow(ctx,
		`SELECT scf_anchor_id FROM imported_catalog_controls
		 WHERE imported_catalog_id = $1 AND source_control_id = 'IAC-06'`,
		report.ProfileID).Scan(&anchorID); err != nil {
		t.Fatalf("read IAC-06 mapping: %v", err)
	}
	if anchorID == nil || *anchorID != "IAC-06" {
		t.Errorf("IAC-06 scf_anchor_id = %v, want IAC-06 (requirement -> SCF anchor)", anchorID)
	}
	// ac-3 was NOT selected by the import directive.
	var ac3Count int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM imported_catalog_controls
		 WHERE imported_catalog_id = $1 AND source_control_id = 'ac-3'`,
		report.ProfileID).Scan(&ac3Count); err != nil {
		t.Fatalf("read ac-3: %v", err)
	}
	if ac3Count != 0 {
		t.Errorf("ac-3 should not be in the resolved baseline (import excluded it), got %d rows", ac3Count)
	}

	// AC-7: a success audit row exists with the profile action.
	var auditAction string
	if err := admin.QueryRow(ctx,
		`SELECT action FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND catalog_id = $2`,
		tenant, report.ProfileID).Scan(&auditAction); err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if auditAction != "profile_imported" {
		t.Errorf("audit action = %q, want profile_imported", auditAction)
	}

	// P0-511-4: the bundled SCF spine was not mutated by the import.
	var leaked int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM scf_anchors WHERE scf_id IN ('ac-1','ac-2')`).Scan(&leaked); err != nil {
		t.Fatalf("read scf_anchors: %v", err)
	}
	if leaked != 0 {
		t.Errorf("import leaked %d resolved controls into scf_anchors (P0-511-4 violation)", leaked)
	}
}

// ===== AC-10: an unresolvable / EXTERNAL import.href errors WITHOUT fetching =====

func TestImportProfile_ExternalHrefRejectedWithoutFetch(t *testing.T) {
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

	im := profileimport.NewImporter(app, bridge)
	_, err = im.Import(ctxFor(t, tenant), profileimport.Request{
		ProfileJSON: loadFixture(t, "profile_external_href.json"),
		Catalogs:    [][]byte{loadFixture(t, "base_catalog.json")},
		SourceLabel: "malicious",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if !errors.Is(err, profileimport.ErrResolutionFailed) {
		t.Fatalf("expected ErrResolutionFailed for an external href, got %v", err)
	}

	ctx := context.Background()
	// AC-5 / P0-511-3: NO baseline and NO control rows persisted.
	var baseCount, ctlCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_catalogs WHERE tenant_id = $1`, tenant).Scan(&baseCount); err != nil {
		t.Fatalf("count baselines: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_catalog_controls WHERE tenant_id = $1`, tenant).Scan(&ctlCount); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	if baseCount != 0 || ctlCount != 0 {
		t.Errorf("nothing should persist on an external-href rejection: baselines=%d controls=%d", baseCount, ctlCount)
	}

	// AC-7: a rejection audit row WAS written (separate committed tx).
	var rejectCount int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND action = 'profile_import_rejected'`, tenant).Scan(&rejectCount); err != nil {
		t.Fatalf("count rejections: %v", err)
	}
	if rejectCount != 1 {
		t.Errorf("profile_import_rejected audit rows = %d, want 1", rejectCount)
	}
}

// ===== AC-11: a malformed profile rolls back, nothing persisted =====

func TestImportProfile_MalformedPersistsNothing(t *testing.T) {
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

	im := profileimport.NewImporter(app, bridge)
	_, err = im.Import(ctxFor(t, tenant), profileimport.Request{
		ProfileJSON: loadFixture(t, "profile_malformed.json"),
		Catalogs:    [][]byte{loadFixture(t, "base_catalog.json")},
		SourceLabel: "bad",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if !errors.Is(err, profileimport.ErrResolutionFailed) {
		t.Fatalf("expected ErrResolutionFailed, got %v", err)
	}

	ctx := context.Background()
	var baseCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_catalogs WHERE tenant_id = $1`, tenant).Scan(&baseCount); err != nil {
		t.Fatalf("count baselines: %v", err)
	}
	if baseCount != 0 {
		t.Errorf("malformed profile must persist nothing, got %d baselines", baseCount)
	}
}

// ===== AC-4 (slice 578): a two-level chain resolves end-to-end =====

func TestImportProfile_ChainedResolvesEndToEnd(t *testing.T) {
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

	im := profileimport.NewImporter(app, bridge)
	report, err := im.Import(ctxFor(t, tenant), profileimport.Request{
		// entry profile -> intermediate profile -> base catalog.
		ProfileJSON: loadFixture(t, "profile_chained_entry.json"),
		Profiles:    [][]byte{loadFixture(t, "profile_intermediate.json")},
		Catalogs:    [][]byte{loadFixture(t, "base_catalog.json")},
		SourceLabel: "Chained baseline test",
		ImportedBy:  "grc-tester",
		Role:        authz.RoleGRCEngineer,
	})
	if err != nil {
		t.Fatalf("Import (chained): %v", err)
	}
	// The entry narrowed the intermediate (ac-1, ac-2, ac-3, IAC-06) down to
	// ac-1, ac-2, IAC-06.
	if report.ControlCount != 3 {
		t.Errorf("ControlCount = %d, want 3 (entry narrowed the chain)", report.ControlCount)
	}
	if report.MappedCount != 1 {
		t.Errorf("MappedCount = %d, want 1 (IAC-06 -> SCF anchor)", report.MappedCount)
	}

	ctx := context.Background()
	var kind string
	if err := admin.QueryRow(ctx,
		`SELECT kind FROM imported_catalogs WHERE id = $1 AND tenant_id = $2`,
		report.ProfileID, tenant).Scan(&kind); err != nil {
		t.Fatalf("read imported_catalogs: %v", err)
	}
	if kind != "profile" {
		t.Errorf("kind = %q, want profile", kind)
	}

	// The success-audit detail records the resolved chain provenance (slice
	// 578): entry-profile + intermediate profile + catalog, each with a hash.
	var detail []byte
	if err := admin.QueryRow(ctx,
		`SELECT detail FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND catalog_id = $2 AND action = 'profile_imported'`,
		tenant, report.ProfileID).Scan(&detail); err != nil {
		t.Fatalf("read audit detail: %v", err)
	}
	if !strings.Contains(string(detail), `"chain"`) ||
		!strings.Contains(string(detail), `"entry-profile"`) ||
		!strings.Contains(string(detail), `"catalog"`) {
		t.Errorf("audit detail missing chain provenance: %s", detail)
	}
}

// ===== AC-4 (slice 578): a cyclic chain errors WITHOUT looping or fetching =====

func TestImportProfile_CyclicChainRejected(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)

	// A -> B -> A. The Go-side chain validator rejects this BEFORE the bridge
	// is ever dialed, so this is provable even without the bridge — but we
	// still start it to prove nothing is fetched / looped end-to-end.
	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	im := profileimport.NewImporter(app, bridge)
	done := make(chan error, 1)
	go func() {
		_, ierr := im.Import(ctxFor(t, tenant), profileimport.Request{
			ProfileJSON: loadFixture(t, "profile_cycle_a.json"),
			Profiles:    [][]byte{loadFixture(t, "profile_cycle_b.json")},
			Catalogs:    [][]byte{loadFixture(t, "base_catalog.json")},
			SourceLabel: "cyclic",
			ImportedBy:  "grc-tester",
			Role:        authz.RoleGRCEngineer,
		})
		done <- ierr
	}()
	select {
	case ierr := <-done:
		if !errors.Is(ierr, profileimport.ErrChainCycle) {
			t.Fatalf("expected ErrChainCycle for A->B->A, got %v", ierr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cyclic chain did not terminate within 15s (P0-578-2 — possible loop)")
	}

	// Nothing persisted (P0-578-3); a rejection audit row WAS written.
	ctx := context.Background()
	var baseCount, rejectCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM imported_catalogs WHERE tenant_id = $1`, tenant).Scan(&baseCount); err != nil {
		t.Fatalf("count baselines: %v", err)
	}
	if baseCount != 0 {
		t.Errorf("a cyclic chain must persist nothing, got %d baselines", baseCount)
	}
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM imported_catalog_audit_log
		 WHERE tenant_id = $1 AND action = 'profile_import_rejected'`, tenant).Scan(&rejectCount); err != nil {
		t.Fatalf("count rejections: %v", err)
	}
	if rejectCount != 1 {
		t.Errorf("profile_import_rejected audit rows = %d, want 1", rejectCount)
	}
}

// ===== AC-12: tenant isolation — Tenant A's baseline never lands under Tenant B =====

func TestImportProfile_TenantIsolation(t *testing.T) {
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

	im := profileimport.NewImporter(app, bridge)
	report, err := im.Import(ctxFor(t, tenantA), profileimport.Request{
		ProfileJSON: loadFixture(t, "profile_baseline.json"),
		Catalogs:    [][]byte{loadFixture(t, "base_catalog.json")},
		SourceLabel: "Tenant A baseline",
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
		`SELECT count(*) FROM imported_catalogs WHERE id = $1`, report.ProfileID).Scan(&visibleToB); err != nil {
		t.Fatalf("tenant B read: %v", err)
	}
	if visibleToB != 0 {
		t.Errorf("RLS leak: tenant B sees %d of tenant A's resolved baseline (want 0)", visibleToB)
	}
	var controlsVisibleToB int
	if err := tx.QueryRow(bCtx,
		`SELECT count(*) FROM imported_catalog_controls WHERE imported_catalog_id = $1`,
		report.ProfileID).Scan(&controlsVisibleToB); err != nil {
		t.Fatalf("tenant B control read: %v", err)
	}
	if controlsVisibleToB != 0 {
		t.Errorf("RLS leak: tenant B sees %d of tenant A's resolved controls (want 0)", controlsVisibleToB)
	}
}
