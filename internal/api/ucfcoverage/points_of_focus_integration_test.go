//go:build integration

package ucfcoverage_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const pofAPISlug = "pof-api-test"

// #1731: the requirement API returns a criterion's rolled-up points of
// focus, on both /coverage and /anchors, and omits the field for a
// requirement with none.
func TestRequirementAPI_ReturnsPointsOfFocus(t *testing.T) {
	ensureCatalog(t)
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	cleanup := func() {
		for _, stmt := range []string{
			"DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = $1 AND tenant_id IS NULL)",
			"DELETE FROM frameworks WHERE slug = $1 AND tenant_id IS NULL",
		} {
			if _, err := pool.Exec(ctx, stmt, pofAPISlug); err != nil {
				t.Fatalf("cleanup %q: %v", stmt, err)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	want := []soc2import.PointOfFocus{
		{Code: "CC1.1-POF1", SCFAnchors: []string{"IAC-01"}},
		{Code: "CC1.1-POF2", SCFAnchors: []string{"AST-01", "IAC-01"}},
	}
	cw := &soc2import.Crosswalk{
		SchemaVersion:     soc2import.SchemaVersion,
		FrameworkSlug:     pofAPISlug,
		FrameworkName:     "POF API test",
		FrameworkVersion:  "2017",
		SourceAttribution: "community_draft",
		Requirements: []soc2import.Requirement{
			{Code: "CC1.1", Title: "CC1.1", PointsOfFocus: want},
			{Code: "CC1.2", Title: "CC1.2"},
		},
		Mappings: []soc2import.Mapping{
			{RequirementCode: "CC1.1", SCFAnchor: "IAC-01", RelationshipType: "intersects_with", Strength: 0.5, Rationale: "test"},
			{RequirementCode: "CC1.2", SCFAnchor: "AST-01", RelationshipType: "intersects_with", Strength: 0.5, Rationale: "test"},
		},
	}
	if _, err := soc2import.Import(ctx, pool, cw); err != nil {
		t.Fatalf("Import: %v", err)
	}

	ts, bearer := setupHTTPServer(t, tenantA)
	for _, tc := range []struct {
		path string
		want []soc2import.PointOfFocus
	}{
		{"/v1/requirements/" + pofAPISlug + ":2017:CC1.1/coverage", want},
		{"/v1/requirements/" + pofAPISlug + ":2017:CC1.1/anchors", want},
		{"/v1/requirements/" + pofAPISlug + ":2017:CC1.2/coverage", nil},
	} {
		resp, body := get(t, ts, tc.path, bearer)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d; body=%s", tc.path, resp.StatusCode, body)
		}
		var got struct {
			Requirement struct {
				PointsOfFocus *[]soc2import.PointOfFocus `json:"points_of_focus"`
			} `json:"requirement"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode %s: %v", tc.path, err)
		}
		if tc.want == nil {
			if got.Requirement.PointsOfFocus != nil {
				t.Fatalf("GET %s: points_of_focus = %+v; want it omitted", tc.path, *got.Requirement.PointsOfFocus)
			}
			continue
		}
		if got.Requirement.PointsOfFocus == nil {
			t.Fatalf("GET %s: requirement.points_of_focus missing; body=%s", tc.path, body)
		}
		if !reflect.DeepEqual(*got.Requirement.PointsOfFocus, tc.want) {
			t.Fatalf("GET %s: points_of_focus = %+v; want %+v", tc.path, *got.Requirement.PointsOfFocus, tc.want)
		}
	}
}
