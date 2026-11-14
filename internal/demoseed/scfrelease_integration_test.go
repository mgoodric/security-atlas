//go:build integration

// #1772: the posture spine adopts global SCF anchors only from the release
// its anchor slice declares. Uses the two #1770 fixture releases (A legacy,
// B current), which both carry IAC-06 as different anchors.

package demoseed_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// fixtureAnchorID returns the id of the anchor with code in a fixture
// release.
func fixtureAnchorID(t *testing.T, release, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := adminPool.QueryRow(context.Background(), `
		SELECT a.id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.tenant_id IS NULL AND fv.version = $1 AND a.scf_id = $2`,
		release, code).Scan(&id)
	if err != nil {
		t.Fatalf("fixture anchor %s %s: %v", release, code, err)
	}
	return id
}

// controlsOn counts the tenant's controls bound to anchor.
func controlsOn(t *testing.T, tenantID, anchor uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM controls WHERE tenant_id = $1 AND scf_anchor_id = $2`,
		tenantID, anchor).Scan(&n); err != nil {
		t.Fatalf("count controls on anchor: %v", err)
	}
	return n
}

func TestApply_PostureSpineAdoptsFromTheDeclaredRelease(t *testing.T) {
	scfreleasetest.LoadAB(t, adminPool)
	anchorA := fixtureAnchorID(t, scfreleasetest.ReleaseA, "IAC-06")
	anchorB := fixtureAnchorID(t, scfreleasetest.ReleaseB, "IAC-06")

	for _, tc := range []struct {
		name, slug, release string
		wantA, wantB        bool
	}{
		// A is loaded first, so adopting "the oldest anchor with this
		// code" would also pick A; B shows the release decides.
		{"declares A", "demo-it-1772-a", scfreleasetest.ReleaseA, true, false},
		{"declares B", "demo-it-1772-b", scfreleasetest.ReleaseB, false, true},
		// DemoSCFRelease is not loaded here: nothing is adopted from A or
		// B, the spine synthesizes its own anchors.
		{"declares an unloaded release", "demo-it-1772-default", demoseed.DemoSCFRelease, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanupTenant(t, tc.slug)
			seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
			if err != nil {
				t.Fatalf("NewSeeder: %v", err)
			}
			res, err := seeder.WithSCFRelease(tc.release).Apply(context.Background(), demoseed.ApplyInput{Slug: tc.slug})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := controlsOn(t, res.TenantID, anchorA) > 0; got != tc.wantA {
				t.Errorf("controls on release A's IAC-06: %v; want %v", got, tc.wantA)
			}
			if got := controlsOn(t, res.TenantID, anchorB) > 0; got != tc.wantB {
				t.Errorf("controls on release B's IAC-06: %v; want %v", got, tc.wantB)
			}
			if controlsAnchored(t, res.TenantID) == 0 {
				t.Error("no demo control is anchored")
			}
		})
	}
}
