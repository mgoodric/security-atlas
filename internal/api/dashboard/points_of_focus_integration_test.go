//go:build integration

package dashboard_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api/scfseed"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const pofPostureSlug = "pof-posture-test"

// #1731: SOC 2 points of focus never count toward the FrameworkPosture
// denominator. The workbook column holds criteria CC1.1 and (only through a
// point of focus) CC2.1, plus three points of focus. The tenant covers CC1.1
// alone, so coverage is 1 of 2 criteria = 50%. Had the points of focus become
// requirements it would be 1 of 4 = 25%.
func TestFrameworkPosture_PointsOfFocusNotInDenominator(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	if err := scfseed.EnsureSCFCatalog(ctx, admin); err != nil {
		t.Fatalf("EnsureSCFCatalog: %v", err)
	}
	resetPOFFramework(t, admin)
	t.Cleanup(func() { resetPOFFramework(t, admin) })

	cw, err := soc2import.LoadSCFWorkbook(writePOFWorkbook(t), soc2import.SCFWorkbookOptions{
		FrameworkSlug:    pofPostureSlug,
		FrameworkVersion: "2017",
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if _, err := soc2import.Import(ctx, admin, cw); err != nil {
		t.Fatalf("Import: %v", err)
	}

	var frameworkID, astAnchor uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT id FROM frameworks WHERE slug = $1 AND tenant_id IS NULL`, pofPostureSlug).Scan(&frameworkID); err != nil {
		t.Fatalf("lookup framework: %v", err)
	}
	if err := admin.QueryRow(ctx, `
		SELECT a.id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id AND fv.status = 'current'
		WHERE a.scf_id = 'AST-01'`).Scan(&astAnchor); err != nil {
		t.Fatalf("lookup AST-01: %v", err)
	}

	tenant := freshTenant(t, admin)
	seedControlOnAnchor(t, admin, tenant, astAnchor)
	env := testServer(t, app, tenant)

	resp, body := get(t, env, "/v1/frameworks/posture")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET posture: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["frameworks"].([]any)
	for _, r := range rows {
		m := r.(map[string]any)
		if m["framework_id"].(string) != frameworkID.String() {
			continue
		}
		if cov := m["coverage_pct"].(float64); cov != 50.0 {
			t.Fatalf("coverage_pct = %v; want 50 (1 of 2 criteria; points of focus are not requirements)", cov)
		}
		return
	}
	t.Fatalf("posture row for %s missing from %v", pofPostureSlug, rows)
}

func writePOFWorkbook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	set := func(sheet string, rows [][]any) {
		for r, row := range rows {
			for c, v := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					t.Fatalf("cell name: %v", err)
				}
				if err := f.SetCellValue(sheet, cell, v); err != nil {
					t.Fatalf("SetCellValue: %v", err)
				}
			}
		}
	}
	if err := f.SetSheetName("Sheet1", "Focal Documents"); err != nil {
		t.Fatalf("SetSheetName: %v", err)
	}
	set("Focal Documents", [][]any{
		{"SCF Column Header", "Focal Document Identifier (FDI)", "Source"},
		{"AICPA\nTSC 2017", "usa-aicpa-tsc-2017", "AICPA"},
	})
	if _, err := f.NewSheet("SCF 2026.3"); err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	set("SCF 2026.3", [][]any{
		{"SCF #", "AICPA\nTSC 2017"},
		{"AST-01", "CC1.1"},
		{"BCD-02", "CC1.1-POF1\nCC2.1-POF1"},
		{"IAC-01", "CC1.1-POF2"},
	})
	path := filepath.Join(t.TempDir(), "pof.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

func resetPOFFramework(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	for _, stmt := range []string{
		"DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = $1 AND tenant_id IS NULL)",
		"DELETE FROM frameworks WHERE slug = $1 AND tenant_id IS NULL",
	} {
		if _, err := admin.Exec(context.Background(), stmt, pofPostureSlug); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}
