//go:build integration

package ucfcoverage_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// TestCoverage_RejectedEdgeEarnsNoCredit pins #1755: a crosswalk edge a
// reviewer marked mapping_tier='rejected' (ADR 0018) must not connect a
// requirement to a tenant's control in any coverage traversal. Rejecting the
// only edge drops the requirement from coverage; restoring the tier brings it
// back.
//
// Self-contained: seeds its own framework + anchor + single edge so flipping
// the tier never disturbs the shared SOC 2 catalog other tests read.
func TestCoverage_RejectedEdgeEarnsNoCredit(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	uniq := uuid.NewString()[:8]

	fwID := uuid.New()
	fvID := uuid.New()
	anchorID := uuid.New()
	reqID := uuid.New()
	edgeID := uuid.New()
	anchorSCFID := "REJ-" + uniq
	if _, err := pool.Exec(ctx, `INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, '1755 rejected-tier fw', 'test', '')`, fwID, "rej-"+uniq); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2026', 'current')`, fvID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
		VALUES ($1, $2, $3, 'IAC', 'rejected-tier anchor', '')`, anchorID, fvID, anchorSCFID); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'REJ-1', 'rejected-tier requirement', '')`, reqID, fvID); err != nil {
		t.Fatalf("seed requirement: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO fw_to_scf_edges
		(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES ($1, $2, $3, 'equal', 1.0, 'community_draft', '')`, edgeID, reqID, anchorID); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM controls WHERE scf_anchor_id = $1`, anchorID)
		_, _ = pool.Exec(c, `DELETE FROM fw_to_scf_edges WHERE id = $1`, edgeID)
		_, _ = pool.Exec(c, `DELETE FROM framework_requirements WHERE id = $1`, reqID)
		_, _ = pool.Exec(c, `DELETE FROM scf_anchors WHERE id = $1`, anchorID)
		_, _ = pool.Exec(c, `UPDATE frameworks SET latest_version_id = NULL WHERE id = $1`, fwID)
		_, _ = pool.Exec(c, `DELETE FROM framework_versions WHERE framework_id = $1`, fwID)
		_, _ = pool.Exec(c, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})

	cid := seedControl(t, tenantA, anchorSCFID, "test-1755-rejected", "Rejected-tier control")
	ts, bearer := setupHTTPServer(t, tenantA)

	setTier := func(tier string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`UPDATE fw_to_scf_edges SET mapping_tier = $1::crosswalk_mapping_tier WHERE id = $2`,
			tier, edgeID); err != nil {
			t.Fatalf("set mapping_tier=%s: %v", tier, err)
		}
	}
	decode := func(path string, into any) {
		t.Helper()
		resp, body := get(t, ts, path, bearer)
		if resp.StatusCode != 200 {
			t.Fatalf("%s status = %d; body=%s", path, resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatalf("unmarshal %s: %v\nbody=%s", path, err, body)
		}
	}
	// covered reports, per traversal, whether the requirement and the
	// tenant's control are linked.
	covered := func() (forward, reverse, control bool) {
		t.Helper()
		var req struct {
			Anchors  []map[string]any `json:"anchors"`
			Controls []struct {
				ID string `json:"id"`
			} `json:"controls"`
		}
		decode("/v1/requirements/"+reqID.String()+"/coverage", &req)
		for _, c := range req.Controls {
			if c.ID == cid.String() {
				forward = true
			}
		}
		if forward != (len(req.Anchors) == 1) {
			t.Fatalf("forward traversal: anchors=%d but control linked=%v", len(req.Anchors), forward)
		}

		var rev struct {
			Requirements []struct {
				Code string `json:"code"`
			} `json:"requirements"`
		}
		decode("/v1/anchors/"+anchorID.String()+"/requirements", &rev)
		reverse = len(rev.Requirements) == 1 && rev.Requirements[0].Code == "REJ-1"

		var ctl struct {
			Requirements []struct {
				Code string `json:"code"`
			} `json:"requirements"`
		}
		decode("/v1/controls/"+cid.String()+"/coverage", &ctl)
		control = len(ctl.Requirements) == 1 && ctl.Requirements[0].Code == "REJ-1"
		return forward, reverse, control
	}

	if f, r, c := covered(); !f || !r || !c {
		t.Fatalf("draft edge: want covered on every traversal, got forward=%v reverse=%v control=%v", f, r, c)
	}

	setTier("rejected")
	if f, r, c := covered(); f || r || c {
		t.Fatalf("rejected edge: want no coverage on any traversal, got forward=%v reverse=%v control=%v", f, r, c)
	}

	setTier("draft")
	if f, r, c := covered(); !f || !r || !c {
		t.Fatalf("restored edge: want covered on every traversal, got forward=%v reverse=%v control=%v", f, r, c)
	}
}
