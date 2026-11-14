//go:build integration

// #1798 — coverage and traversal count the tenant's overlay edges. Runs
// against a real Postgres with RLS, through the API servers newFixture
// builds on the non-owner app role.
//
// Fixture: newFixture (#1797) plus a tenant B control anchored to the same
// IAC-06, so B's views are non-trivial. Tenant A adds an overlay edge
// C2 -> IAC-06 (C2 is the tested-under criterion IAC-06 does not reach in
// the catalog), and every view is read before the add, after it, and after
// retiring it.

package mappingoverlays_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/mcp"
	"github.com/mgoodric/security-atlas/internal/mcp/tools"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// coverageViews is one tenant's reads of every view #1798 names.
type coverageViews struct {
	posture         []byte // GET /v1/frameworks/posture
	requirementC1   []byte // GET /v1/requirements/{C1}/coverage
	requirementC2   []byte // GET /v1/requirements/{C2}/coverage
	control         []byte // GET /v1/controls/{id}/coverage
	anchorTraversal []byte // GET /v1/anchors/{IAC-06}/requirements
}

func rawGet(t *testing.T, e env, path string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.url+path, nil)
	req.Header.Set("Authorization", "Bearer "+e.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d (%s)", path, resp.StatusCode, b)
	}
	return b
}

type coverageFixture struct {
	fixture
	frameworkID uuid.UUID
	anchorID    uuid.UUID // IAC-06 in release B
	controlA    uuid.UUID
	controlB    uuid.UUID
}

func newCoverageFixture(t *testing.T) coverageFixture {
	t.Helper()
	ctx := context.Background()
	f := coverageFixture{fixture: newFixture(t)}
	if err := f.admin.QueryRow(ctx, `SELECT id FROM frameworks WHERE slug = $1 AND tenant_id IS NULL`, f.framework).Scan(&f.frameworkID); err != nil {
		t.Fatalf("read framework id: %v", err)
	}
	if err := f.admin.QueryRow(ctx, `
		SELECT a.id FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = 'IAC-06'`, scfreleasetest.ReleaseB).Scan(&f.anchorID); err != nil {
		t.Fatalf("read anchor id: %v", err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT id FROM controls WHERE tenant_id = $1 AND scf_anchor_id = $2`,
		f.tenantA.tenant, f.anchorID).Scan(&f.controlA); err != nil {
		t.Fatalf("read tenant A control: %v", err)
	}
	f.controlB = uuid.New()
	if _, err := f.admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
			control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
			evidence_queries, manual_evidence_schema, linked_policy_ids, freshness_class,
			bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at, bundle_uploaded_by
		)
		VALUES ($1, $2, $3, 1, 'IAC-06', $4, 'Tenant B confidentiality agreements', '',
			'human_resources', 'automated', 'security', 'active', '',
			'[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'test')`,
		f.controlB, f.tenantB.tenant, "ovl-b-"+f.framework, f.anchorID); err != nil {
		t.Fatalf("seed tenant B control: %v", err)
	}
	return f
}

func (f coverageFixture) read(t *testing.T, e env, control uuid.UUID) coverageViews {
	t.Helper()
	return coverageViews{
		posture:         rawGet(t, e, "/v1/frameworks/posture"),
		requirementC1:   rawGet(t, e, "/v1/requirements/"+f.requirement["C1"].String()+"/coverage"),
		requirementC2:   rawGet(t, e, "/v1/requirements/"+f.requirement["C2"].String()+"/coverage"),
		control:         rawGet(t, e, "/v1/controls/"+control.String()+"/coverage"),
		anchorTraversal: rawGet(t, e, "/v1/anchors/"+f.anchorID.String()+"/requirements"),
	}
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return out
}

// coveragePct is the fixture framework's coverage_pct in a posture body
// (`{"frameworks": [...]}`), or the export's framework_posture panel.
func (f coverageFixture) coveragePct(t *testing.T, rows []any) float64 {
	t.Helper()
	for _, r := range rows {
		row := r.(map[string]any)
		if row["framework_id"] == f.frameworkID.String() {
			return row["coverage_pct"].(float64)
		}
	}
	t.Fatalf("no posture row for framework %s in %v", f.frameworkID, rows)
	return 0
}

func (f coverageFixture) dashboardPct(t *testing.T, posture []byte) float64 {
	t.Helper()
	return f.coveragePct(t, decode(t, posture)["frameworks"].([]any))
}

// exportPct reads coverage_pct from GET /v1/dashboard/export?format=json.
func (f coverageFixture) exportPct(t *testing.T, e env) float64 {
	t.Helper()
	body := decode(t, rawGet(t, e, "/v1/dashboard/export?format=json"))
	panels, ok := body["panels"].(map[string]any)
	if !ok {
		t.Fatalf("export has no panels: %v", body)
	}
	return f.coveragePct(t, panels["framework_posture"].([]any))
}

// mcpPct reads coverage_pct through the MCP list_framework_posture tool.
func (f coverageFixture) mcpPct(t *testing.T, e env) float64 {
	t.Helper()
	c, err := mcp.NewClient(e.url, e.admin, "test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tools.NewListFrameworkPosture(c).Handle(context.Background(), json.RawMessage(fmt.Sprintf(`{"limit":%d}`, tools.MaxLimit)))
	if err != nil {
		t.Fatalf("mcp list_framework_posture: %v", err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return f.coveragePct(t, decode(t, b)["frameworks"].([]any))
}

// pathTo finds the entry whose key field equals want in a JSON array and
// returns it, or nil.
func pathTo(list any, key, want string) map[string]any {
	items, _ := list.([]any)
	for _, it := range items {
		m := it.(map[string]any)
		if m[key] == want {
			return m
		}
	}
	return nil
}

// assertOverlayCounted checks every per-requirement, per-control and
// traversal view of tenant A includes C2 through IAC-06, marked overlay.
func (f coverageFixture) assertOverlayCounted(t *testing.T, v coverageViews, edgeID string) {
	t.Helper()
	req := decode(t, v.requirementC2)
	anchor := pathTo(req["anchors"], "scf_id", "IAC-06")
	if anchor == nil {
		t.Fatalf("C2 coverage has no IAC-06 anchor: %s", v.requirementC2)
	}
	if anchor["origin"] != "overlay" || anchor["edge_id"] != edgeID || anchor["source_attribution"] != "org_internal" {
		t.Errorf("C2 -> IAC-06 anchor = %v; want origin overlay, edge %s, org_internal", anchor, edgeID)
	}
	if pathTo(req["controls"], "id", f.controlA.String()) == nil {
		t.Errorf("C2 coverage does not list tenant A's control: %s", v.requirementC2)
	}

	for name, body := range map[string][]byte{"control coverage": v.control, "anchor traversal": v.anchorTraversal} {
		row := pathTo(decode(t, body)["requirements"], "requirement_id", f.requirement["C2"].String())
		if row == nil {
			t.Errorf("%s does not include C2: %s", name, body)
			continue
		}
		if row["origin"] != "overlay" || row["edge_id"] != edgeID {
			t.Errorf("%s C2 row = %v; want origin overlay, edge %s", name, row, edgeID)
		}
	}

	// The catalog path is still marked catalog.
	c1 := pathTo(decode(t, v.control)["requirements"], "requirement_id", f.requirement["C1"].String())
	if c1 == nil || c1["origin"] != "catalog" || c1["edge_id"] != f.catalogC1.String() {
		t.Errorf("control coverage C1 row = %v; want the catalog edge, origin catalog", c1)
	}
}

func (f coverageFixture) assertOverlayAbsent(t *testing.T, v coverageViews) {
	t.Helper()
	if a := pathTo(decode(t, v.requirementC2)["anchors"], "scf_id", "IAC-06"); a != nil {
		t.Errorf("C2 coverage reaches IAC-06 without an active overlay: %v", a)
	}
	for name, body := range map[string][]byte{"control coverage": v.control, "anchor traversal": v.anchorTraversal} {
		if row := pathTo(decode(t, body)["requirements"], "requirement_id", f.requirement["C2"].String()); row != nil {
			t.Errorf("%s includes C2 without an active overlay: %v", name, row)
		}
	}
}

func assertSame(t *testing.T, when string, got, want coverageViews) {
	t.Helper()
	for name, pair := range map[string][2][]byte{
		"posture":          {got.posture, want.posture},
		"requirement C1":   {got.requirementC1, want.requirementC1},
		"requirement C2":   {got.requirementC2, want.requirementC2},
		"control coverage": {got.control, want.control},
		"anchor traversal": {got.anchorTraversal, want.anchorTraversal},
	} {
		if !bytes.Equal(pair[0], pair[1]) {
			t.Errorf("%s: %s changed:\n got  %s\n want %s", when, name, pair[0], pair[1])
		}
	}
}

func TestOverlayCoverage_CountsForOwnerOnlyAndRetireRestores(t *testing.T) {
	f := newCoverageFixture(t)

	beforeA := f.read(t, f.tenantA, f.controlA)
	beforeB := f.read(t, f.tenantB, f.controlB)
	f.assertOverlayAbsent(t, beforeA)
	f.assertOverlayAbsent(t, beforeB)
	// C1 of C1..C3 is reached through the catalog; C3's catalog edge is rejected.
	if got := f.dashboardPct(t, beforeA.posture); got != 33.33 {
		t.Fatalf("tenant A coverage before overlay = %v; want 33.33", got)
	}

	status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	if status != http.StatusCreated {
		t.Fatalf("create overlay: status %d (%v)", status, body)
	}
	edgeID := edgeOf(t, body)["id"].(string)

	afterA := f.read(t, f.tenantA, f.controlA)
	if got := f.dashboardPct(t, afterA.posture); got != 66.67 {
		t.Errorf("tenant A coverage with overlay = %v; want 66.67", got)
	}
	f.assertOverlayCounted(t, afterA, edgeID)
	// The MCP program read and the dashboard export agree with the dashboard API.
	if got := f.exportPct(t, f.tenantA); got != 66.67 {
		t.Errorf("tenant A dashboard export coverage = %v; want 66.67", got)
	}
	if got := f.mcpPct(t, f.tenantA); got != 66.67 {
		t.Errorf("tenant A MCP list_framework_posture coverage = %v; want 66.67", got)
	}

	// Tenant B: byte-for-byte unchanged by A's overlay edge.
	assertSame(t, "tenant B after A's overlay", f.read(t, f.tenantB, f.controlB), beforeB)
	if got := f.exportPct(t, f.tenantB); got != f.dashboardPct(t, beforeB.posture) {
		t.Errorf("tenant B export coverage = %v; want %v", got, f.dashboardPct(t, beforeB.posture))
	}
	if got := f.mcpPct(t, f.tenantB); got != f.dashboardPct(t, beforeB.posture) {
		t.Errorf("tenant B MCP coverage = %v; want %v", got, f.dashboardPct(t, beforeB.posture))
	}

	status, body = call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/"+edgeID+"/retire",
		f.tenantA.admin, map[string]any{"reason": "auditor withdrew the finding"})
	if status != http.StatusOK {
		t.Fatalf("retire overlay: status %d (%v)", status, body)
	}

	// Retiring restores A's previous numbers and views exactly; B is still unchanged.
	assertSame(t, "tenant A after retire", f.read(t, f.tenantA, f.controlA), beforeA)
	assertSame(t, "tenant B after retire", f.read(t, f.tenantB, f.controlB), beforeB)
	if got := f.exportPct(t, f.tenantA); got != 33.33 {
		t.Errorf("tenant A export coverage after retire = %v; want 33.33", got)
	}
	if got := f.mcpPct(t, f.tenantA); got != 33.33 {
		t.Errorf("tenant A MCP coverage after retire = %v; want 33.33", got)
	}
}

// The API refuses an overlay on a pair the catalog already maps, but a
// catalog import can add the pair after the overlay exists. The view then
// keeps one path, the catalog's, rather than counting the pair twice.
func TestOverlayCoverage_CatalogEdgeAddedLaterLeavesOnePath(t *testing.T) {
	f := newCoverageFixture(t)
	if status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06")); status != http.StatusCreated {
		t.Fatalf("create overlay: status %d (%v)", status, body)
	}
	catalogC2 := uuid.New()
	if _, err := f.admin.Exec(context.Background(), `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, source_attribution, mapping_tier)
		VALUES ($1, $2, $3, 'equal', 1.0, 'scf_official', 'verified')`,
		catalogC2, f.requirement["C2"], f.anchorID); err != nil {
		t.Fatalf("seed catalog edge C2 -> IAC-06: %v", err)
	}

	v := f.read(t, f.tenantA, f.controlA)
	var paths []any
	for _, a := range decode(t, v.requirementC2)["anchors"].([]any) {
		if a.(map[string]any)["scf_id"] == "IAC-06" {
			paths = append(paths, a)
		}
	}
	if len(paths) != 1 || paths[0].(map[string]any)["origin"] != "catalog" || paths[0].(map[string]any)["edge_id"] != catalogC2.String() {
		t.Errorf("C2 -> IAC-06 paths = %v; want only the catalog edge %s", paths, catalogC2)
	}
	if got := f.dashboardPct(t, v.posture); got != 66.67 {
		t.Errorf("coverage = %v; want 66.67", got)
	}
}

// The view filters overlay rows by app.current_tenant itself, so even a
// role that bypasses RLS (the migrate pool) sees only the tenant in
// context, and nothing without one.
func TestOverlayCoverage_ViewHoldsTenantFilterWithoutRLS(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := context.Background()
	for _, e := range []env{f.tenantA, f.tenantB} {
		if status, body := call(t, e, http.MethodPost, "/v1/mapping-overlays", e.admin, f.body("C2", "IAC-06")); status != http.StatusCreated {
			t.Fatalf("create overlay: status %d (%v)", status, body)
		}
	}
	count := func(tenant string) int {
		t.Helper()
		tx, err := f.admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if tenant != "" {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant); err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM effective_fw_to_scf_edges
			WHERE origin = 'overlay' AND framework_requirement_id = $1`, f.requirement["C2"]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(f.tenantA.tenant); n != 1 {
		t.Errorf("overlay rows for tenant A = %d; want 1", n)
	}
	if n := count(""); n != 0 {
		t.Errorf("overlay rows with no tenant context = %d; want 0", n)
	}
}

// A rejected catalog edge does not count; an org may still assert the pair,
// and its overlay edge is then the live path.
func TestOverlayCoverage_OverlayOnRejectedCatalogPairCounts(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := context.Background()
	var cry03 uuid.UUID
	if err := f.admin.QueryRow(ctx, `
		SELECT a.id FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = 'CRY-03'`, scfreleasetest.ReleaseB).Scan(&cry03); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(ctx, `UPDATE controls SET scf_id = 'CRY-03', scf_anchor_id = $2 WHERE id = $1`, f.controlA, cry03); err != nil {
		t.Fatal(err)
	}
	if got := f.dashboardPct(t, rawGet(t, f.tenantA, "/v1/frameworks/posture")); got != 0 {
		t.Fatalf("coverage through a rejected catalog edge = %v; want 0", got)
	}
	status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C3", "CRY-03"))
	if status != http.StatusCreated {
		t.Fatalf("create overlay: status %d (%v)", status, body)
	}
	if got := f.dashboardPct(t, rawGet(t, f.tenantA, "/v1/frameworks/posture")); got != 33.33 {
		t.Errorf("coverage with overlay on rejected pair = %v; want 33.33", got)
	}
	a := pathTo(decode(t, rawGet(t, f.tenantA, "/v1/requirements/"+f.requirement["C3"].String()+"/coverage"))["anchors"], "scf_id", "CRY-03")
	if a == nil || a["origin"] != "overlay" {
		t.Errorf("C3 -> CRY-03 anchor = %v; want the overlay path", a)
	}
}
