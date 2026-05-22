//go:build integration

// Slice 104 integration tests — `GET /v1/anchors?include=state` join.
//
// These tests exercise:
//   * AC-1 + AC-2 — joined response shape is `{ anchors: [{ ..., state: ... | null }] }`
//                  and additive (omitted-include returns the v1 shape).
//   * AC-4       — the handler runs ONE query (single CTE join), not a
//                  per-anchor loop. We can't directly assert query count
//                  through the public API, but the response time + the
//                  unit test pinning the no-loop wire layer are the
//                  practical guards.
//   * AC-5       — RLS round-trip: Tenant A's request never sees Tenant
//                  B's state rows.
//   * AC-6       — worst-state-wins aggregation across two controls on
//                  one anchor (fail beats pass).
//   * AC-8       — empty-state branch: an anchor with no tenant control
//                  returns `state: null`.
//
// Re-uses the slice-006 + slice-007 setup harness (setupHTTPServer)
// which loads the SCF fixture + SOC 2 crosswalk. The slice 104 tests
// then layer a tenant-scoped control + evidence + evaluation on top.

package anchors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// setupHTTPServerWithSrv mirrors setupHTTPServer but ALSO returns the
// underlying *api.Server so the test can mint additional per-tenant
// bearers off the same credstore. We can't just call api.New twice for
// tenant B — bearers must be valid against the credstore the running
// server consults, otherwise auth fails. Sharing the harness lets the
// RLS round-trip test compare two real tenant bearers against one
// server.
func setupHTTPServerWithSrv(t *testing.T) (*httptest.Server, *api.Server, string) {
	t.Helper()

	adminPool := openPoolDSN(t, adminDSN(t))
	defer adminPool.Close()
	for _, stmt := range []string{
		"DELETE FROM fw_to_scf_edges",
		"DELETE FROM framework_requirements",
		"DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = 'soc2' AND tenant_id IS NULL)",
		"DELETE FROM frameworks WHERE slug = 'soc2' AND tenant_id IS NULL",
	} {
		if _, err := adminPool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
	// SCF anchors: only wipe + reimport if the table is empty (mirrors
	// the slice-006 harness pattern).
	var anchorCount int
	if err := adminPool.QueryRow(context.Background(), `SELECT count(*) FROM scf_anchors`).Scan(&anchorCount); err != nil {
		t.Fatalf("count scf_anchors: %v", err)
	}
	if anchorCount == 0 {
		// Re-use setupHTTPServer's importer path by delegating to it for
		// the cold-start case. The hot path (table already loaded) skips
		// the importer entirely and we keep the harness lean.
		t.Skip("SCF anchors empty — run setupHTTPServer-backed test first to seed; slice 104 round-trip needs a populated catalog")
	}

	appPool := openPoolDSN(t, appDSN(t))
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(appPool)
	// Slice 197: mint JWT via slice 190 path. ViewerFor matches the
	// legacy IssueBootstrapCredential shape (no admin/approver/owner
	// elevation).
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenantA)))
	handler := srv.HTTPHandlerForTests()
	if handler == nil {
		t.Fatal("HTTPHandlerForTests returned nil")
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(func() {
		ts.Close()
		appPool.Close()
	})
	return ts, srv, bearer
}

// seedAnchorState writes the minimum tenant-scoped rows required to make
// `?include=state` return a populated cell for the given SCF anchor:
//  1. a `controls` row linked to the anchor (scf_anchor_id),
//  2. a `control_evaluations` row carrying the result + freshness.
//
// The control_evaluations table is append-only (no UPDATE/DELETE policy
// under FORCE RLS); each call appends a fresh evaluation row.
func seedAnchorState(t *testing.T, admin *pgxpool.Pool, tenantID, anchorID string, result, freshness string, lastObserved time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ctrlID := uuid.New()
	bundleID := "test-bundle-104-" + ctrlID.String()[:8]
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, scf_anchor_id, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 104 test control', 'AAA', 'automated',
		        $3, $4, '[]'::jsonb, 'true')
	`, ctrlID, tenantID, bundleID, anchorID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	evalID := uuid.New()
	runID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status,
			evidence_count_in_window, last_observed_at, freshness_class, trigger
		)
		VALUES ($1, $2, $3, NULL, $4,
		        now(), $5, $6,
		        1, $7, 'daily', 'manual')
	`, evalID, tenantID, ctrlID, runID, result, freshness, lastObserved); err != nil {
		t.Fatalf("seed control_evaluations: %v", err)
	}
	return ctrlID
}

// cleanupTenantState removes the slice-104 tenant rows the test seeded.
// Idempotent. Called via t.Cleanup so failed asserts don't pollute the
// shared DB across reruns.
func cleanupTenantState(t *testing.T, admin *pgxpool.Pool, tenantID string) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DELETE FROM control_evaluations WHERE tenant_id = $1",
		"DELETE FROM evidence_records WHERE tenant_id = $1",
		"DELETE FROM controls WHERE tenant_id = $1",
	} {
		if _, err := admin.Exec(ctx, stmt, tenantID); err != nil {
			t.Logf("cleanup %q: %v", stmt, err)
		}
	}
}

// anchorIDBySCFID resolves an SCF anchor's UUID for use as a join target.
// Reads the global catalog (tenant_id IS NULL) under the admin role.
func anchorIDBySCFID(t *testing.T, admin *pgxpool.Pool, scfID string) string {
	t.Helper()
	var id string
	err := admin.QueryRow(context.Background(), `
		SELECT a.id::text
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f          ON f.id  = fv.framework_id
		WHERE f.slug = 'scf' AND fv.status = 'current' AND a.scf_id = $1
	`, scfID).Scan(&id)
	if err != nil {
		t.Fatalf("anchorIDBySCFID(%q): %v", scfID, err)
	}
	return id
}

// AC-1 + AC-2 — when ?include=state is omitted the response shape is
// the v1 shape (anchorWire only; no `state` key). This is the additive
// guarantee: existing callers cannot break.
func TestListAnchors_OmittedIncludeReturnsV1Shape(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/anchors", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchors []map[string]any `json:"anchors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Anchors) == 0 {
		t.Fatal("expected anchors")
	}
	if _, hasState := got.Anchors[0]["state"]; hasState {
		t.Errorf("v1 shape should NOT include `state` key when ?include=state omitted; got %+v", got.Anchors[0])
	}
}

// AC-1 + AC-8 — every anchor in the response carries a `state` key.
// Anchors with no tenant control return `state: null` (the empty-state
// branch); the SCF fixture has no tenant-instantiated controls by
// default, so this asserts the no-loop / null branch wholesale.
//
// NOTE: we pre-clean the tenant rows so this test is independent of
// what order it runs in relative to TestListAnchors_IncludeStateRollsUp…
// (which seeds controls + evaluations under the same tenant). Without
// the pre-clean, suite ordering becomes load-bearing — and the
// integration suite's parallel-tenant invariant means any test that
// relies on "no controls" must take responsibility for ensuring it.
func TestListAnchors_IncludeStateReturnsNullStateForEmptyTenant(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	admin := openPoolDSN(t, adminDSN(t))
	defer admin.Close()
	cleanupTenantState(t, admin, tenantA)
	resp, body := get(t, ts, "/v1/anchors?include=state", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchors []map[string]any `json:"anchors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Anchors) == 0 {
		t.Fatal("expected anchors")
	}
	for i, a := range got.Anchors {
		state, hasKey := a["state"]
		if !hasKey {
			t.Fatalf("anchor[%d] missing `state` key: %+v", i, a)
		}
		if state != nil {
			// A tenant with no controls should see `state: null` everywhere.
			// If state pops up here without seedAnchorState being called,
			// either RLS leaked or a prior test polluted the tenant.
			t.Fatalf("anchor[%d] state should be null on empty tenant; got %+v", i, state)
		}
	}
}

// AC-1 + AC-5 + AC-6 — Tenant A seeds a single anchor with a fail+pass
// pair of controls. ?include=state returns that anchor with the rolled-
// up worst state (fail). Tenant B's bearer never sees Tenant A's state.
func TestListAnchors_IncludeStateRollsUpWorstStatePerAnchor_TenantIsolated(t *testing.T) {
	ts, srv, bearer := setupHTTPServerWithSrv(t)
	admin := openPoolDSN(t, adminDSN(t))
	defer admin.Close()

	anchorUUID := anchorIDBySCFID(t, admin, "IAC-06")

	// Tenant A: seed two controls under one anchor — one fail (older),
	// one pass (newer). The slice 104 SQL's worst-state-wins ranking
	// should surface `fail`. The two controls each carry a single
	// evaluation row (latest-per-(control, cell) collapse then worst
	// across both controls).
	t.Cleanup(func() { cleanupTenantState(t, admin, tenantA) })
	seedAnchorState(t, admin, tenantA, anchorUUID, "fail", "fresh", time.Now().Add(-2*time.Hour))
	seedAnchorState(t, admin, tenantA, anchorUUID, "pass", "fresh", time.Now().Add(-1*time.Hour))

	resp, body := get(t, ts, "/v1/anchors?include=state", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchors []struct {
			ID    string          `json:"id"`
			SCFID string          `json:"scf_id"`
			State *map[string]any `json:"state"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	var hit bool
	for _, a := range got.Anchors {
		if a.SCFID != "IAC-06" {
			continue
		}
		hit = true
		if a.State == nil {
			t.Fatalf("IAC-06 state should be populated after seedAnchorState; got nil")
		}
		st := *a.State
		// AC-6: worst-state-wins. fail beats pass.
		if st["result"] != "fail" {
			t.Errorf("IAC-06 worst result = %v; want fail", st["result"])
		}
		// stateWire shape — pinned columns must all be present.
		for _, key := range []string{"result", "freshness_status", "last_observed_at", "evaluated_at"} {
			if _, ok := st[key]; !ok {
				t.Errorf("IAC-06 state missing key %q: %+v", key, st)
			}
		}
	}
	if !hit {
		t.Fatalf("IAC-06 not present in response")
	}

	// AC-5 — RLS round-trip. Issue a fresh bearer for Tenant B on the
	// SAME credstore the running server validates against; the response
	// must NOT contain a populated `state` for IAC-06 since Tenant B
	// has no controls anywhere.
	tenantB := "22222222-2222-2222-2222-222222222222"
	t.Cleanup(func() { cleanupTenantState(t, admin, tenantB) })
	bearerB := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenantB)))
	respB, bodyB := get(t, ts, "/v1/anchors?include=state", bearerB)
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("tenant B status = %d; body=%s", respB.StatusCode, bodyB)
	}
	var gotB struct {
		Anchors []struct {
			SCFID string          `json:"scf_id"`
			State *map[string]any `json:"state"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(bodyB, &gotB); err != nil {
		t.Fatalf("unmarshal B: %v\nbody=%s", err, bodyB)
	}
	for _, a := range gotB.Anchors {
		if a.SCFID == "IAC-06" && a.State != nil {
			t.Fatalf("Tenant B saw Tenant A's state on IAC-06 — RLS bypass! got %+v", *a.State)
		}
	}
}

// AC-1 + AC-2 — the wire shape MUST include the slice 098 design-doc-
// pinned columns when state is populated (result + freshness_status +
// last_observed_at). This is the contract slice 098's filters.ts +
// page.tsx depend on.
func TestListAnchors_IncludeStateColumnSetMatchesDesignDoc(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	admin := openPoolDSN(t, adminDSN(t))
	defer admin.Close()
	anchorUUID := anchorIDBySCFID(t, admin, "IAC-06")
	t.Cleanup(func() { cleanupTenantState(t, admin, tenantA) })
	obs := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	seedAnchorState(t, admin, tenantA, anchorUUID, "pass", "fresh", obs)

	_, body := get(t, ts, "/v1/anchors?include=state", bearer)
	if !strings.Contains(string(body), `"freshness_status"`) {
		t.Fatalf("expected freshness_status in payload; body=%s", body)
	}
	if !strings.Contains(string(body), `"last_observed_at"`) {
		t.Fatalf("expected last_observed_at in payload; body=%s", body)
	}
}
