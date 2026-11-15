//go:build integration

package ucfcoverage_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// releaseCrosswalk is a two-requirement crosswalk over the #1770 fixture
// codes: R-SAME maps to IAC-06 (same title in A and B), R-RENAMED to GOV-01
// (title changed in B, so a control on it is flagged, not re-bound).
func releaseCrosswalk(slug, scfRelease string) *soc2import.Crosswalk {
	return &soc2import.Crosswalk{
		SchemaVersion:     soc2import.SchemaVersion,
		FrameworkSlug:     slug,
		FrameworkName:     "2203 release-default fw",
		FrameworkIssuer:   "test",
		FrameworkVersion:  "2026",
		SourceAttribution: "community_draft",
		SCFRelease:        scfRelease,
		Requirements: []soc2import.Requirement{
			{Code: "R-SAME", Title: "same-title requirement"},
			{Code: "R-RENAMED", Title: "renamed-anchor requirement"},
		},
		Mappings: []soc2import.Mapping{
			{RequirementCode: "R-SAME", SCFAnchor: "IAC-06", RelationshipType: "equal", Strength: 1.0, Rationale: "test"},
			{RequirementCode: "R-RENAMED", SCFAnchor: "GOV-01", RelationshipType: "equal", Strength: 1.0, Rationale: "test"},
		},
	}
}

func anchorIn(t *testing.T, admin *pgxpool.Pool, release, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := admin.QueryRow(context.Background(), `
		SELECT a.id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = $2`, release, code).Scan(&id); err != nil {
		t.Fatalf("anchor %s in %s: %v", code, release, err)
	}
	return id
}

func removeReleaseFramework(t *testing.T, admin *pgxpool.Pool, slug string) {
	t.Helper()
	c := context.Background()
	for _, stmt := range []string{
		`DELETE FROM fw_to_scf_edge_tier_transitions WHERE edge_id IN (
			SELECT e.id FROM fw_to_scf_edges e
			JOIN framework_requirements r ON r.id = e.framework_requirement_id
			JOIN framework_versions fv ON fv.id = r.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id WHERE f.slug = $1)`,
		`DELETE FROM fw_to_scf_edges WHERE framework_requirement_id IN (
			SELECT r.id FROM framework_requirements r
			JOIN framework_versions fv ON fv.id = r.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id WHERE f.slug = $1)`,
		`DELETE FROM framework_requirements WHERE framework_version_id IN (
			SELECT fv.id FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id WHERE f.slug = $1)`,
		`UPDATE frameworks SET latest_version_id = NULL WHERE slug = $1`,
		`DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = $1)`,
		`DELETE FROM frameworks WHERE slug = $1`,
	} {
		if _, err := admin.Exec(c, stmt, slug); err != nil {
			t.Errorf("remove framework %s: %v", slug, err)
		}
	}
}

// TestRequirementCoverage_DefaultPinAcrossReleaseUpgrade measures #2203: a
// crosswalk imported while SCF release A was current, then release B is
// imported, controls re-bound, and the crosswalk re-imported as boot does.
func TestRequirementCoverage_DefaultPinAcrossReleaseUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name string
		// reimportRelease is the scf_release the crosswalk is re-imported
		// with after the upgrade: "" binds in the current release, as the
		// bundled crosswalks do at boot; a release label keeps it pinned.
		reimportRelease string
	}{
		{"reimported unpinned", ""},
		{"crosswalk pinned to legacy release", scfreleasetest.ReleaseA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ensureCatalog(t)
			wipeTenantControls(t)
			admin := dbtest.NewMigratePool(t)
			ctx := context.Background()
			scfreleasetest.LoadAB(t, admin)
			slug := "rel2203-" + uuid.NewString()[:8]
			t.Cleanup(func() { removeReleaseFramework(t, admin, slug) })

			if _, err := soc2import.Import(ctx, admin, releaseCrosswalk(slug, scfreleasetest.ReleaseA)); err != nil {
				t.Fatalf("import crosswalk in A: %v", err)
			}
			same := seedControl(t, tenantA, "IAC-06", "test-2203-same", "same-title control")
			renamed := seedControl(t, tenantA, "GOV-01", "test-2203-renamed", "renamed-anchor control")
			for id, code := range map[uuid.UUID]string{same: "IAC-06", renamed: "GOV-01"} {
				if _, err := admin.Exec(ctx, `UPDATE controls SET scf_anchor_id = $2 WHERE id = $1`,
					id, anchorIn(t, admin, scfreleasetest.ReleaseA, code)); err != nil {
					t.Fatalf("bind control to A: %v", err)
				}
			}
			ts, bearer := setupHTTPServer(t, tenantA)

			linked := func(query string) map[string]bool {
				t.Helper()
				out := map[string]bool{}
				for code, cid := range map[string]uuid.UUID{"R-SAME": same, "R-RENAMED": renamed} {
					resp, body := get(t, ts, "/v1/requirements/"+slug+":2026:"+code+"/coverage"+query, bearer)
					if resp.StatusCode != 200 {
						t.Fatalf("%s status = %d; body=%s", code, resp.StatusCode, body)
					}
					var got struct {
						Controls []struct {
							ID string `json:"id"`
						} `json:"controls"`
					}
					if err := json.Unmarshal(body, &got); err != nil {
						t.Fatalf("unmarshal: %v", err)
					}
					for _, c := range got.Controls {
						if c.ID == cid.String() {
							out[code] = true
						}
					}
				}
				return out
			}

			before := linked("?scf_release=" + scfreleasetest.ReleaseA)
			t.Logf("before upgrade, pinned to A: %v", before)

			if _, err := scfimport.RebindControls(ctx, admin); err != nil {
				t.Fatalf("RebindControls: %v", err)
			}
			if _, err := soc2import.Import(ctx, admin, releaseCrosswalk(slug, tc.reimportRelease)); err != nil {
				t.Fatalf("re-import crosswalk: %v", err)
			}
			t.Logf("after upgrade, default:  %v", linked(""))
			t.Logf("after upgrade, pinned B: %v", linked("?scf_release="+scfreleasetest.ReleaseB))
		})
	}
}
