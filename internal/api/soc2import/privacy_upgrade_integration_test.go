//go:build integration

package soc2import_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const privacyUpgradeSlug = "soc2-privacy-upgrade-example"

// #1753: an install that imported the bundled SOC 2 crosswalk before the
// Privacy criteria shipped holds a frozen 2017 version with 43 requirements.
// The next boot re-imports the bundled file into that same version. The
// immutability trigger blocks only UPDATEs, so the 18 Privacy criteria are
// inserted as new requirements with their edges, the 43 stored rows are
// untouched, and nothing is reported as kept.
func TestImport_FrozenSOC2VersionGainsPrivacyCriteria(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetFrameworkCatalog(t, pool, privacyUpgradeSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, privacyUpgradeSlug) })
	ensureSCFLoaded(t, pool)
	ctx := context.Background()

	isPrivacy := func(code string) bool { return strings.HasPrefix(code, "P") && !strings.HasPrefix(code, "PI") }

	full := loadCrosswalk(t)
	full.FrameworkSlug = privacyUpgradeSlug

	before := *full
	before.Requirements = nil
	before.Mappings = nil
	for _, r := range full.Requirements {
		if !isPrivacy(r.Code) {
			before.Requirements = append(before.Requirements, r)
		}
	}
	for _, m := range full.Mappings {
		if !isPrivacy(m.RequirementCode) {
			before.Mappings = append(before.Mappings, m)
		}
	}
	if len(before.Requirements) != 43 {
		t.Fatalf("pre-#1753 crosswalk = %d requirements; want 43", len(before.Requirements))
	}
	if _, err := soc2import.Import(ctx, pool, &before); err != nil {
		t.Fatalf("pre-#1753 Import: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `
		SELECT fv.status::text FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL AND fv.version = '2017'`, privacyUpgradeSlug).Scan(&status); err != nil {
		t.Fatalf("version status: %v", err)
	}
	if status != "current" {
		t.Fatalf("version status = %q; want current (frozen)", status)
	}

	report, err := soc2import.Import(ctx, pool, full)
	if err != nil {
		t.Fatalf("upgrade Import: %v", err)
	}
	if report.IsNewVersion {
		t.Fatal("upgrade import created a new version; want the existing 2017 version")
	}
	if report.RequirementsCreated != 18 || report.RequirementsUnchanged != 43 || report.RequirementsUpdated != 0 {
		t.Fatalf("requirements created/unchanged/updated = %d/%d/%d; want 18/43/0",
			report.RequirementsCreated, report.RequirementsUnchanged, report.RequirementsUpdated)
	}
	if len(report.RequirementsDiffered) != 0 {
		t.Fatalf("RequirementsDiffered = %v; want none", report.RequirementsDiffered)
	}
	if want := len(full.Mappings) - len(before.Mappings); report.EdgesCreated != want {
		t.Fatalf("edges created = %d; want %d", report.EdgesCreated, want)
	}

	var count int32
	if err := pool.QueryRow(ctx, `
		SELECT fv.requirement_count FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL AND fv.version = '2017'`, privacyUpgradeSlug).Scan(&count); err != nil {
		t.Fatalf("requirement_count: %v", err)
	}
	if count != 61 {
		t.Fatalf("requirement_count = %d; want 61", count)
	}
}
