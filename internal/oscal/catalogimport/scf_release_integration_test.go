//go:build integration

// Integration tests for #2264: an OSCAL catalog import matches its control
// ids within a declared SCF release.
//
// They run against a real Postgres with the two #1770 fixture releases
// loaded (scfreleasetest.LoadAB: release B current, release A legacy). The
// bridge is a fake that returns a fixed projection, because what is under
// test is the Go-side release binding, not trestle's parsing.

package catalogimport_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/oscal/catalogimport"
	"github.com/mgoodric/security-atlas/internal/oscal/scfbind"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// projectionBridge returns a valid projection holding the given control ids.
type projectionBridge struct{ controlIDs []string }

func (b projectionBridge) ImportCatalog(_ context.Context, _ []byte, _ string) (*oscalv1.ImportCatalogResponse, error) {
	controls := make([]*oscalv1.ImportedControl, 0, len(b.controlIDs))
	for _, id := range b.controlIDs {
		controls = append(controls, &oscalv1.ImportedControl{ControlId: id, Title: "control " + id})
	}
	return &oscalv1.ImportCatalogResponse{Valid: true, OscalVersion: "1.1.2", CatalogTitle: "Release fixture catalog", Controls: controls}, nil
}

// PRM-07 is only in release A; AST-04.1 is only in release B; AST-04 is in
// both; AC-2 is not an SCF code at all.
var releaseControls = []string{"PRM-07", "AST-04.1", "AST-04", "AC-2"}

const catalogWithoutProp = `{"catalog":{"metadata":{"title":"t"}}}`

func catalogWithReleaseProp(release string) []byte {
	return []byte(`{"catalog":{"metadata":{"title":"t","props":[{"name":"scf-release","value":"` + release + `"}]}}}`)
}

func importCatalogWith(t *testing.T, tenant string, doc []byte, release string) (catalogimport.Report, error) {
	t.Helper()
	im := catalogimport.NewImporter(dbtest.NewAppPool(t), projectionBridge{controlIDs: releaseControls})
	return im.Import(dbtest.WithTenantCtx(t, tenant), catalogimport.Request{
		OscalJSON:   doc,
		SourceLabel: "release fixture",
		ImportedBy:  "tester",
		Role:        authz.RoleGRCEngineer,
		SCFRelease:  release,
	})
}

// controlAnchors maps each imported control's source id to its stored
// scf_anchor_id ("" for NULL), across every catalog the tenant imported.
func controlAnchors(t *testing.T, admin *pgxpool.Pool, tenant string) map[string]string {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT source_control_id, COALESCE(scf_anchor_id, '')
		FROM imported_catalog_controls WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("read imported controls: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, anchor string
		if err := rows.Scan(&id, &anchor); err != nil {
			t.Fatalf("scan imported control: %v", err)
		}
		out[id] = anchor
	}
	return out
}

// auditRelease reads the scf_release the success audit row recorded.
func auditRelease(t *testing.T, admin *pgxpool.Pool, tenant string) string {
	t.Helper()
	var raw []byte
	if err := admin.QueryRow(context.Background(), `
		SELECT detail FROM imported_catalog_audit_log
		WHERE tenant_id = $1 AND action = 'catalog_imported'`, tenant).Scan(&raw); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	var detail struct {
		SCFRelease string `json:"scf_release"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode audit detail: %v", err)
	}
	return detail.SCFRelease
}

func assertAnchors(t *testing.T, got, want map[string]string) {
	t.Helper()
	for id, w := range want {
		if got[id] != w {
			t.Errorf("control %s scf_anchor_id = %q; want %q", id, got[id], w)
		}
	}
}

// A declared release decides which codes map, in both directions: release A
// maps PRM-07 (gone from B) and not AST-04.1; release B the reverse. The
// flag and the metadata prop declare it alike.
func TestImportCatalog_SCFReleaseDeclaredBindsInThatRelease(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)

	inA := map[string]string{"PRM-07": "PRM-07", "AST-04.1": "", "AST-04": "AST-04", "AC-2": ""}
	inB := map[string]string{"PRM-07": "", "AST-04.1": "AST-04.1", "AST-04": "AST-04", "AC-2": ""}
	for _, tc := range []struct {
		name    string
		doc     []byte
		flag    string
		release string
		want    map[string]string
	}{
		{"flag A", []byte(catalogWithoutProp), scfreleasetest.ReleaseA, scfreleasetest.ReleaseA, inA},
		{"flag B", []byte(catalogWithoutProp), scfreleasetest.ReleaseB, scfreleasetest.ReleaseB, inB},
		{"prop A", catalogWithReleaseProp(scfreleasetest.ReleaseA), "", scfreleasetest.ReleaseA, inA},
		{"prop B", catalogWithReleaseProp(scfreleasetest.ReleaseB), "", scfreleasetest.ReleaseB, inB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := freshTenant(t, admin)
			report, err := importCatalogWith(t, tenant, tc.doc, tc.flag)
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if report.SCFRelease != tc.release || len(report.Warnings) != 0 {
				t.Fatalf("report release=%q warnings=%v; want %q and none", report.SCFRelease, report.Warnings, tc.release)
			}
			if report.MappedCount != 2 {
				t.Fatalf("MappedCount = %d; want 2", report.MappedCount)
			}
			assertAnchors(t, controlAnchors(t, admin, tenant), tc.want)
			if got := auditRelease(t, admin, tenant); got != tc.release {
				t.Errorf("audit scf_release = %q; want %q", got, tc.release)
			}
		})
	}
}

// With no release declared, control ids match in the current release (B)
// as before, and the report carries the deprecation warning.
func TestImportCatalog_SCFReleaseAbsentWarnsAndBindsCurrent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)

	report, err := importCatalogWith(t, tenant, []byte(catalogWithoutProp), "")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SCFRelease != scfreleasetest.ReleaseB {
		t.Fatalf("SCFRelease = %q; want current release %q", report.SCFRelease, scfreleasetest.ReleaseB)
	}
	if len(report.Warnings) != 1 || report.Warnings[0] != catalogimport.SCFReleaseDeprecationWarning {
		t.Fatalf("Warnings = %v; want the deprecation warning", report.Warnings)
	}
	assertAnchors(t, controlAnchors(t, admin, tenant), map[string]string{"PRM-07": "", "AST-04.1": "AST-04.1"})
}

// A prop naming a release that is not loaded falls back to the current
// release with a warning (#1771 D5): the prop is the document's statement,
// not the operator's.
func TestImportCatalog_SCFReleasePropNotLoadedFallsBack(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)

	report, err := importCatalogWith(t, tenant, catalogWithReleaseProp("no-such-release"), "")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	want := fmt.Sprintf(scfbind.PropReleaseNotLoadedWarning, "no-such-release", "no-such-release")
	if report.SCFRelease != scfreleasetest.ReleaseB || len(report.Warnings) != 1 || report.Warnings[0] != want {
		t.Fatalf("report release=%q warnings=%v; want %q and the not-loaded warning", report.SCFRelease, report.Warnings, scfreleasetest.ReleaseB)
	}
	assertAnchors(t, controlAnchors(t, admin, tenant), map[string]string{"PRM-07": "", "AST-04.1": "AST-04.1"})
}

// A flag naming a release that is not loaded never falls back, and a flag
// that contradicts the prop is refused; neither persists a control.
func TestImportCatalog_SCFReleaseErrors(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)

	if _, err := importCatalogWith(t, tenant, []byte(catalogWithoutProp), "no-such-release"); !errors.Is(err, scfrelease.ErrUnknownRelease) {
		t.Fatalf("unknown release: err = %v; want ErrUnknownRelease", err)
	}
	if _, err := importCatalogWith(t, tenant, catalogWithReleaseProp(scfreleasetest.ReleaseA), scfreleasetest.ReleaseB); !errors.Is(err, catalogimport.ErrSCFReleaseConflict) {
		t.Fatalf("flag vs prop: err = %v; want ErrSCFReleaseConflict", err)
	}
	if got := controlAnchors(t, admin, tenant); len(got) != 0 {
		t.Fatalf("controls persisted after refused imports: %v", got)
	}
}
