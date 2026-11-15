//go:build integration

// Integration tests for #1771: an OSCAL component-definition import binds
// its claims' control ids within a declared SCF release.
//
// They run against a real Postgres with the two #1770 fixture releases
// loaded (scfreleasetest.LoadAB: release B current, release A legacy). The
// bridge is a fake that returns a fixed projection, because what is under
// test is the Go-side release binding, not trestle's parsing.

package componentimport_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/oscal/componentimport"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// projectionBridge returns a valid projection whose one component claims
// the given control ids.
type projectionBridge struct{ controlIDs []string }

func (b projectionBridge) ImportComponentDefinition(_ context.Context, _ []byte, _ string) (*oscalv1.ImportComponentDefinitionResponse, error) {
	claims := make([]*oscalv1.VendorClaim, 0, len(b.controlIDs))
	for _, id := range b.controlIDs {
		claims = append(claims, &oscalv1.VendorClaim{ControlId: id, Statement: "vendor statement", RequirementUuid: "req-" + id})
	}
	return &oscalv1.ImportComponentDefinitionResponse{
		Valid:                    true,
		OscalVersion:             "1.1.2",
		ComponentDefinitionTitle: "Release fixture vendor",
		Components: []*oscalv1.VendorComponent{{
			ComponentUuid: "11111111-2222-4333-8444-555555555555",
			ComponentType: "service",
			Title:         "Fixture service",
			Claims:        claims,
		}},
	}, nil
}

// PRM-07 is in release A and withdrawn from B; AST-04 is in both with
// different meanings; AC-2 is not an SCF code at all.
var releaseClaims = []string{"AST-04", "PRM-07", "AC-2"}

const docWithoutProp = `{"component-definition":{"metadata":{"title":"t"}}}`

func docWithReleaseProp(release string) []byte {
	return []byte(`{"component-definition":{"metadata":{"title":"t","props":[{"name":"scf-release","value":"` + release + `"}]}}}`)
}

// claimReleases maps each claim's control id to its stored scf_anchor_id
// and scf_release ("" for NULL).
func claimReleases(t *testing.T, admin *pgxpool.Pool, tenant string) map[string][2]string {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT control_id, COALESCE(scf_anchor_id, ''), COALESCE(scf_release, '')
		FROM imported_component_claims WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var id, anchor, release string
		if err := rows.Scan(&id, &anchor, &release); err != nil {
			t.Fatalf("scan claim: %v", err)
		}
		out[id] = [2]string{anchor, release}
	}
	return out
}

func importWith(t *testing.T, tenant string, doc []byte, release string) (componentimport.Report, error) {
	t.Helper()
	im := componentimport.NewImporter(dbtest.NewAppPool(t), projectionBridge{controlIDs: releaseClaims})
	return im.Import(dbtest.WithTenantCtx(t, tenant), componentimport.Request{
		OscalJSON:   doc,
		SourceLabel: "fixture vendor",
		ImportedBy:  "tester",
		Role:        authz.RoleGRCEngineer,
		SCFRelease:  release,
	})
}

// --scf-release A binds every claim whose control id is in A to A, while B
// is current: PRM-07 (gone from B) maps, AST-04 records A, AC-2 stays
// unmapped.
func TestImportComponentDefinition_SCFReleaseFlagBindsInDeclaredRelease(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)

	report, err := importWith(t, tenant, []byte(docWithoutProp), scfreleasetest.ReleaseA)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SCFRelease != scfreleasetest.ReleaseA || len(report.Warnings) != 0 {
		t.Fatalf("report release=%q warnings=%v; want %q and none", report.SCFRelease, report.Warnings, scfreleasetest.ReleaseA)
	}
	if report.MappedCount != 2 {
		t.Fatalf("MappedCount = %d; want 2 (AST-04, PRM-07)", report.MappedCount)
	}
	got := claimReleases(t, admin, tenant)
	want := map[string][2]string{
		"AST-04": {"AST-04", scfreleasetest.ReleaseA},
		"PRM-07": {"PRM-07", scfreleasetest.ReleaseA},
		"AC-2":   {"", ""},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("claim %s = %v; want %v", id, got[id], w)
		}
	}
}

// The document's "scf-release" metadata prop declares the release too.
func TestImportComponentDefinition_SCFReleasePropBindsInDeclaredRelease(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)

	report, err := importWith(t, tenant, docWithReleaseProp(scfreleasetest.ReleaseA), "")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SCFRelease != scfreleasetest.ReleaseA {
		t.Fatalf("SCFRelease = %q; want %q", report.SCFRelease, scfreleasetest.ReleaseA)
	}
	if got := claimReleases(t, admin, tenant)["PRM-07"]; got != [2]string{"PRM-07", scfreleasetest.ReleaseA} {
		t.Fatalf("PRM-07 claim = %v; want bound in release A", got)
	}
}

// An unknown release is the resolver's ErrUnknownRelease and persists no
// claim; a flag that contradicts the prop is refused before anything runs.
func TestImportComponentDefinition_SCFReleaseErrors(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)

	if _, err := importWith(t, tenant, []byte(docWithoutProp), "no-such-release"); !errors.Is(err, scfrelease.ErrUnknownRelease) {
		t.Fatalf("unknown release: err = %v; want ErrUnknownRelease", err)
	}
	if _, err := importWith(t, tenant, docWithReleaseProp(scfreleasetest.ReleaseA), scfreleasetest.ReleaseB); !errors.Is(err, componentimport.ErrSCFReleaseConflict) {
		t.Fatalf("flag vs prop: err = %v; want ErrSCFReleaseConflict", err)
	}
	if got := claimReleases(t, admin, tenant); len(got) != 0 {
		t.Fatalf("claims persisted after refused imports: %v", got)
	}
}

// With no release declared, claims bind in the current release (B) as
// before, the release is recorded, and the report carries the deprecation
// warning. PRM-07 is not in B, so it stays unmapped.
func TestImportComponentDefinition_SCFReleaseAbsentWarnsAndBindsCurrent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)

	report, err := importWith(t, tenant, []byte(docWithoutProp), "")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Warnings) != 1 || report.Warnings[0] != componentimport.SCFReleaseDeprecationWarning {
		t.Fatalf("Warnings = %v; want the deprecation warning", report.Warnings)
	}
	got := claimReleases(t, admin, tenant)
	if got["AST-04"] != [2]string{"AST-04", scfreleasetest.ReleaseB} {
		t.Errorf("AST-04 claim = %v; want bound in current release B", got["AST-04"])
	}
	if got["PRM-07"] != [2]string{"", ""} {
		t.Errorf("PRM-07 claim = %v; want unmapped (withdrawn from B)", got["PRM-07"])
	}
}
