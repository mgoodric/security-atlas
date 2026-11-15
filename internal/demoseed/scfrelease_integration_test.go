//go:build integration

// #1772: the posture spine adopts global SCF anchors only from the release
// its anchor slice declares. Uses the two #1770 fixture releases (A legacy,
// B current), each given its own anchor for the spine's first code, so the
// same code names a different anchor in each.

package demoseed_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// addFixtureAnchor adds an anchor with code to a fixture release and
// returns its id. Anchors are frozen against UPDATE, not INSERT, and
// LoadAB's cleanup deletes the release with its anchors.
func addFixtureAnchor(t *testing.T, release, code string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	tag, err := adminPool.Exec(context.Background(), `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
		SELECT $3, fv.id, $2, 'IAC', 'Multi-Factor Authentication (MFA)', 'Release ' || $1 || ': MFA.'
		FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND fv.tenant_id IS NULL AND fv.version = $1`,
		release, code, id)
	if err == nil && tag.RowsAffected() != 1 {
		err = fmt.Errorf("inserted %d rows", tag.RowsAffected())
	}
	if err != nil {
		t.Fatalf("add fixture anchor %s %s: %v", release, code, err)
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
	code := demoseed.FirstDemoSCFAnchorCode()
	anchorA := addFixtureAnchor(t, scfreleasetest.ReleaseA, code)
	anchorB := addFixtureAnchor(t, scfreleasetest.ReleaseB, code)

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
				t.Errorf("controls on release A's anchor: %v; want %v", got, tc.wantA)
			}
			if got := controlsOn(t, res.TenantID, anchorB) > 0; got != tc.wantB {
				t.Errorf("controls on release B's anchor: %v; want %v", got, tc.wantB)
			}
			if controlsAnchored(t, res.TenantID) == 0 {
				t.Error("no demo control is anchored")
			}
		})
	}
}
