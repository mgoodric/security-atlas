//go:build integration

// Issue #1826 — the anchor catalog export projects fw_to_scf_edges.mapping_tier
// so a reviewer-rejected crosswalk edge appears in the dump marked
// `rejected` instead of looking like a live mapping. The export does not
// filter by tier.

package anchors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// seedRejectedEdge adds anchor ISSUE1826-A1 in the current SCF version with
// one edge to the slice 174 seed requirement, stored at mapping_tier =
// 'rejected'. Idempotent across runs.
func seedRejectedEdge(t *testing.T) {
	t.Helper()
	seedAnchorID, requirementID := ensureSCFAnchorsSeed(t)
	ctx := context.Background()
	admin := dbtest.NewMigratePool(t)

	var scfVerID uuid.UUID
	if err := admin.QueryRow(ctx,
		`SELECT framework_version_id FROM scf_anchors WHERE id = $1`, seedAnchorID,
	).Scan(&scfVerID); err != nil {
		t.Fatalf("lookup scf version: %v", err)
	}
	anchorID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
		VALUES ($1, $2, 'ISSUE1826-A1', 'iam', 'issue1826 rejected-edge anchor', '')
		ON CONFLICT (framework_version_id, scf_id) DO NOTHING
	`, anchorID, scfVerID); err != nil {
		t.Fatalf("insert anchor: %v", err)
	}
	if err := admin.QueryRow(ctx,
		`SELECT id FROM scf_anchors WHERE framework_version_id = $1 AND scf_id = 'ISSUE1826-A1'`, scfVerID,
	).Scan(&anchorID); err != nil {
		t.Fatalf("lookup anchor: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale, mapping_tier)
		VALUES ($1, $2, $3, 'equal', 1.0, 'community_draft', 'issue1826 rejected edge', 'rejected')
		ON CONFLICT (framework_requirement_id, scf_anchor_id) DO NOTHING
	`, uuid.New(), requirementID, anchorID); err != nil {
		t.Fatalf("insert rejected edge: %v", err)
	}
}

func TestIssue1826_RejectedEdgeExportedWithRejectedTier(t *testing.T) {
	seedRejectedEdge(t)
	tenant := freshAnchorsTenant(t)

	r := newAnchorsExportRouter(t, tenant)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/anchors/export?format=json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	var anchors []struct {
		SCFID                  string `json:"scf_id"`
		FrameworkSatisfactions []struct {
			FrameworkRequirementCode string `json:"framework_requirement_code"`
			MappingTier              string `json:"mapping_tier"`
		} `json:"framework_satisfactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &anchors); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, a := range anchors {
		if a.SCFID != "ISSUE1826-A1" {
			continue
		}
		if len(a.FrameworkSatisfactions) != 1 {
			t.Fatalf("satisfactions = %d; want 1 (rejected edge must not be filtered)", len(a.FrameworkSatisfactions))
		}
		got := a.FrameworkSatisfactions[0]
		if got.FrameworkRequirementCode != "SLICE174-CC1.1" {
			t.Errorf("requirement code = %q; want SLICE174-CC1.1", got.FrameworkRequirementCode)
		}
		if got.MappingTier != "rejected" {
			t.Errorf("mapping_tier = %q; want rejected", got.MappingTier)
		}
		return
	}
	t.Fatal("anchor ISSUE1826-A1 not found in export")
}
