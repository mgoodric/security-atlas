//go:build integration

package ucfcoverage_test

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
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

// TestRequirementCoverage_DefaultPinAcrossReleaseUpgrade pins #2203: a
// crosswalk imported while SCF release A was current, then release B is
// imported, controls re-bound (#1971) and the crosswalk re-imported.
//
// On the boot path (the crosswalk declares no scf_release, so its re-import
// binds in B) the default read, the read pinned to B and the old unpinned
// traversal all agree, and the re-bound control keeps the coverage it had
// before the upgrade. The flagged control (GOV-01, retitled in B) loses its
// credit under every read: the edge moved to B and the control did not.
// That loss comes from the re-bind, not the pin.
//
// A crosswalk pinned to A on purpose keeps its edges on A, so the default
// read drops it; ?scf_release=A still reads it. That is the documented
// cost of the default (docs/audit-log/2203-coverage-default-release-decisions.md).
func TestRequirementCoverage_DefaultPinAcrossReleaseUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name string
		// reimportRelease is the scf_release the crosswalk is re-imported
		// with after the upgrade: "" binds in the current release, as the
		// bundled crosswalks do at boot; a release label keeps it pinned.
		reimportRelease string
		// wantCurrent is what the default read (and ?scf_release=B) links
		// after the upgrade; wantUnpinned is what the pre-#2203 unpinned
		// traversal links.
		wantCurrent, wantUnpinned map[string]bool
	}{
		{"boot path", "", map[string]bool{"R-SAME": true}, map[string]bool{"R-SAME": true}},
		{"crosswalk pinned to legacy release", scfreleasetest.ReleaseA, map[string]bool{}, map[string]bool{"R-RENAMED": true}},
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

			controlOf := map[string]uuid.UUID{"R-SAME": same, "R-RENAMED": renamed}
			// linked reads each requirement's coverage and reports which are
			// linked to their control, and the release every read echoed.
			linked := func(query string) (map[string]bool, string) {
				t.Helper()
				out := map[string]bool{}
				echoed := map[string]bool{}
				for code, cid := range controlOf {
					resp, body := get(t, ts, "/v1/requirements/"+slug+":2026:"+code+"/coverage"+query, bearer)
					if resp.StatusCode != 200 {
						t.Fatalf("%s status = %d; body=%s", code, resp.StatusCode, body)
					}
					var got struct {
						Controls []struct {
							ID string `json:"id"`
						} `json:"controls"`
						SCFRelease *string `json:"scf_release"`
					}
					if err := json.Unmarshal(body, &got); err != nil {
						t.Fatalf("unmarshal: %v", err)
					}
					rel := "<null>"
					if got.SCFRelease != nil {
						rel = *got.SCFRelease
					}
					echoed[rel] = true
					for _, c := range got.Controls {
						if c.ID == cid.String() {
							out[code] = true
						}
					}
				}
				if len(echoed) != 1 {
					t.Fatalf("reads %q echoed different releases: %v", query, echoed)
				}
				for rel := range echoed {
					return out, rel
				}
				return out, ""
			}
			// unpinned is the pre-#2203 default: every release's anchors.
			unpinned := func() map[string]bool {
				t.Helper()
				out := map[string]bool{}
				q := dbx.New(admin)
				for code, cid := range controlOf {
					var reqID uuid.UUID
					if err := admin.QueryRow(ctx, `
						SELECT r.id FROM framework_requirements r
						JOIN framework_versions fv ON fv.id = r.framework_version_id
						JOIN frameworks f ON f.id = fv.framework_id
						WHERE f.slug = $1 AND r.code = $2`, slug, code).Scan(&reqID); err != nil {
						t.Fatalf("requirement %s: %v", code, err)
					}
					rows, err := q.ListAnchorsForRequirementWithEdges(ctx, pgtype.UUID{Bytes: reqID, Valid: true})
					if err != nil {
						t.Fatalf("unpinned anchors for %s: %v", code, err)
					}
					var bound uuid.UUID
					if err := admin.QueryRow(ctx, `SELECT scf_anchor_id FROM controls WHERE id = $1`, cid).Scan(&bound); err != nil {
						t.Fatalf("control anchor: %v", err)
					}
					for _, r := range rows {
						if uuid.UUID(r.ScfAnchorID.Bytes) == bound {
							out[code] = true
						}
					}
				}
				return out
			}

			before, rel := linked("?scf_release=" + scfreleasetest.ReleaseA)
			if want := map[string]bool{"R-SAME": true, "R-RENAMED": true}; !maps.Equal(before, want) || rel != scfreleasetest.ReleaseA {
				t.Fatalf("before upgrade, pinned to A: linked=%v release=%q; want %v and %q", before, rel, want, scfreleasetest.ReleaseA)
			}

			report, err := scfimport.RebindControls(ctx, admin)
			if err != nil {
				t.Fatalf("RebindControls: %v", err)
			}
			has := func(list []scfimport.ControlOnLegacyAnchor, id uuid.UUID) bool {
				for _, c := range list {
					if c.ControlID == id {
						return true
					}
				}
				return false
			}
			if !has(report.Rebound, same) || !has(report.NeedsReanchoring, renamed) {
				t.Fatalf("re-bind: want the same-title control re-bound and the renamed one flagged; got %+v", report)
			}
			if _, err := soc2import.Import(ctx, admin, releaseCrosswalk(slug, tc.reimportRelease)); err != nil {
				t.Fatalf("re-import crosswalk: %v", err)
			}

			def, rel := linked("")
			if !maps.Equal(def, tc.wantCurrent) || rel != scfreleasetest.ReleaseB {
				t.Errorf("default read: linked=%v release=%q; want %v and current %q", def, rel, tc.wantCurrent, scfreleasetest.ReleaseB)
			}
			if pinned, _ := linked("?scf_release=" + scfreleasetest.ReleaseB); !maps.Equal(pinned, def) {
				t.Errorf("pinned to B: linked=%v; want it equal to the default read %v", pinned, def)
			}
			if got := unpinned(); !maps.Equal(got, tc.wantUnpinned) {
				t.Errorf("unpinned traversal: linked=%v; want %v", got, tc.wantUnpinned)
			}
			if tc.reimportRelease == "" && def["R-SAME"] != before["R-SAME"] {
				t.Errorf("boot path: the re-bound control's coverage changed across the upgrade (before %v, after %v)", before, def)
			}
			if tc.reimportRelease != "" {
				if legacy, _ := linked("?scf_release=" + tc.reimportRelease); !legacy["R-RENAMED"] {
					t.Errorf("?scf_release=%s no longer reads the legacy-pinned crosswalk: %v", tc.reimportRelease, legacy)
				}
			}
		})
	}
}
