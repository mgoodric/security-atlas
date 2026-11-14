//go:build integration

// Issue #1741 — `GET /v1/anchors?include=state&has_control=` filter,
// per-row `control_id`, and the catalog/with-control totals.
//
// What this pins:
//   - has_control=true returns only anchors the caller's tenant has an
//     active control on, each carrying that control's id (the id the
//     control detail page resolves), filtered in SQL ahead of paging.
//   - with_control_total is per tenant: Tenant B never counts or sees
//     Tenant A's control.
//   - has_control without include=state, or with a malformed value, is a
//     400 rather than a silently ignored filter.

package anchors_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

type hasControlResp struct {
	Anchors []struct {
		ID           string  `json:"id"`
		SCFID        string  `json:"scf_id"`
		ControlID    *string `json:"control_id"`
		ControlCount *int64  `json:"control_count"`
	} `json:"anchors"`
	Total            int64 `json:"total"`
	CatalogTotal     int64 `json:"catalog_total"`
	WithControlTotal int64 `json:"with_control_total"`
}

func TestListAnchors_HasControlFiltersToTenantControls(t *testing.T) {
	ts, srv, bearer := setupHTTPServerWithSrv(t)
	admin := dbtest.NewMigratePool(t)

	cleanupTenantState(t, admin, tenantA)
	t.Cleanup(func() { cleanupTenantState(t, admin, tenantA) })
	anchorUUID := anchorIDBySCFID(t, admin, "IAC-37")
	ctrlID := seedAnchorState(t, admin, tenantA, anchorUUID, "pass", "fresh", time.Now().Add(-time.Hour))

	resp, body := get(t, ts, "/v1/anchors?include=state&has_control=true", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var got hasControlResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if len(got.Anchors) != 1 || got.Anchors[0].SCFID != "IAC-37" {
		t.Fatalf("has_control=true anchors = %+v; want only IAC-37", got.Anchors)
	}
	a := got.Anchors[0]
	if a.ControlID == nil || *a.ControlID != ctrlID.String() {
		t.Fatalf("IAC-37 control_id = %v; want %s", a.ControlID, ctrlID)
	}
	if a.ControlCount == nil || *a.ControlCount != 1 {
		t.Fatalf("IAC-37 control_count = %v; want 1", a.ControlCount)
	}
	if got.Total != 1 || got.WithControlTotal != 1 {
		t.Fatalf("total=%d with_control_total=%d; want 1 and 1", got.Total, got.WithControlTotal)
	}
	if got.CatalogTotal <= got.WithControlTotal {
		t.Fatalf("catalog_total=%d; want more than with_control_total=%d", got.CatalogTotal, got.WithControlTotal)
	}

	// has_control=false excludes the anchor and reports the complement.
	resp, body = get(t, ts, "/v1/anchors?include=state&has_control=false&limit=500", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("has_control=false status = %d; body=%s", resp.StatusCode, body)
	}
	var without hasControlResp
	if err := json.Unmarshal(body, &without); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, row := range without.Anchors {
		if row.SCFID == "IAC-37" || row.ControlID != nil {
			t.Fatalf("has_control=false returned a controlled anchor: %+v", row)
		}
	}
	if without.Total != without.CatalogTotal-1 {
		t.Fatalf("has_control=false total=%d; want catalog_total-1=%d", without.Total, without.CatalogTotal-1)
	}

	// Tenant B has no controls: nothing passes the filter, and Tenant A's
	// control is neither listed nor counted.
	tenantB := "22222222-2222-2222-2222-222222222222"
	cleanupTenantState(t, admin, tenantB)
	bearerB := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenantB)))
	respB, bodyB := get(t, ts, "/v1/anchors?include=state&has_control=true", bearerB)
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("tenant B status = %d; body=%s", respB.StatusCode, bodyB)
	}
	var gotB hasControlResp
	if err := json.Unmarshal(bodyB, &gotB); err != nil {
		t.Fatalf("unmarshal B: %v", err)
	}
	if len(gotB.Anchors) != 0 || gotB.WithControlTotal != 0 {
		t.Fatalf("tenant B saw tenant A's control: anchors=%+v with_control_total=%d", gotB.Anchors, gotB.WithControlTotal)
	}
}

func TestListAnchors_HasControlRejectsBadRequests(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	for _, path := range []string{
		"/v1/anchors?has_control=true",
		"/v1/anchors?include=state&has_control=maybe",
		"/v1/anchors?include=state&has_control=true&framework_version_id=" + uuid.NewString(),
	} {
		resp, body := get(t, ts, path, bearer)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d; want 400; body=%s", path, resp.StatusCode, body)
		}
	}
}
