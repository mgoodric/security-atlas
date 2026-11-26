//go:build integration

// Integration tests for SoA contradictions reached through tenant overlay
// edges (issue #1805, parent #1735): an excluded requirement whose only path
// to a passing control is one tenant's active overlay edge is flagged for
// that tenant, never for another, and the flag clears when the edge retires.
//
// ListFrameworkScopeContradictions reads the crosswalk through
// effective_fw_to_scf_edges, whose overlay arm carries the caller tenant's
// active overlay edges only (migration 20261106000000). These tests pin that
// behaviour at the API.

package frameworkscope_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// seedPassingControl gives tenant a control on anchor whose latest
// evaluation passes. Its own cleanup runs before the fixture's, which
// cannot delete the anchor while this control references it.
func seedPassingControl(t *testing.T, admin *pgxpool.Pool, tenant string, anchor uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM control_evaluations WHERE control_id = $1`,
			`DELETE FROM controls WHERE id = $1`,
		} {
			if _, err := admin.Exec(context.Background(), sql, id); err != nil {
				t.Logf("cleanup %q: %v", sql, err)
			}
		}
	})
	if _, err := admin.Exec(ctx, `INSERT INTO controls (
			id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
			control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
			evidence_queries, manual_evidence_schema, linked_policy_ids,
			freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
			bundle_uploaded_at, bundle_uploaded_by
		) SELECT $1, $2, $3, 1, a.scf_id, a.id, 'overlay test control', '',
			'access_control', 'automated', 'security', 'active', '',
			'[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'test'
		FROM scf_anchors a WHERE a.id = $4`,
		id, tenant, "soa_1805_"+uuid.NewString()[:8], anchor); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status, evidence_count_in_window, trigger
		) VALUES ($1, $2, $3, NULL, $4, now(), 'pass', 'fresh', 1, 'manual')`,
		uuid.New(), tenant, id, uuid.New()); err != nil {
		t.Fatalf("seed evaluation: %v", err)
	}
}

// seedOverlayEdge adds an active overlay edge for tenant. Seeded in SQL
// because the create API resolves anchors by SCF release, which the SoA
// fixture's anchors are not part of; retirement goes through the API.
func seedOverlayEdge(t *testing.T, admin *pgxpool.Pool, tenant string, requirement, anchor uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `INSERT INTO fw_to_scf_overlay_edges (
			id, tenant_id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, rationale, source_ref, created_by
		) VALUES ($1, $2, $3, $4, 'equal', 1.0, 'auditor tested C1 under this requirement',
			'https://wiki.example/audit/2026', 'test')`,
		id, tenant, requirement, anchor); err != nil {
		t.Fatalf("seed overlay edge: %v", err)
	}
	return id
}

func anchorOf(t *testing.T, admin *pgxpool.Pool, control uuid.UUID) uuid.UUID {
	t.Helper()
	var anchor uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT scf_anchor_id FROM controls WHERE id = $1`, control).Scan(&anchor); err != nil {
		t.Fatalf("anchor of control: %v", err)
	}
	return anchor
}

// R-02 has no catalog edge. Tenant A's overlay edge R-02 -> A1 puts passing
// C1 behind it, so A's exclusion of R-02 is a contradiction (AC1). Tenant B
// has its own passing control on A1 and excludes R-02 too, but A's edge is
// not B's path, so B sees R-01 only (AC2). Retiring A's edge through the API
// clears A's flag (AC3).
func TestHTTP_Applicability_ContradictionThroughOverlayEdge(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	overlayTables := []string{"fw_to_scf_overlay_edge_audit_log", "fw_to_scf_overlay_edges"}
	tenantA := dbtest.SeedTenant(t, admin, append(overlayTables,
		"framework_scope_applicability", "framework_scope_workflow_log", "framework_scopes",
		"control_evaluations", "controls")...)
	tenantB := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenantA)
	a1 := anchorOf(t, admin, f.Controls["C1"])
	seedPassingControl(t, admin, tenantB, a1)

	tsA, bearerA, adminA := setupHTTPServer(t, tenantA)
	a := soaClient{t: t, base: tsA.URL, bearer: bearerA}
	scopeA := a.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(a, f, scopeA)

	tsB, bearerB, _ := setupHTTPServer(t, tenantB)
	b := soaClient{t: t, base: tsB.URL, bearer: bearerB}
	scopeB := b.createScope(f.FrameworkVersionID, "ISMS")
	b.mustSet(scopeB, f.Req["R-01"], excludedBody)
	b.mustSet(scopeB, f.Req["R-02"], excludedBody)

	assertFlags := func(c soaClient, scopeID, when, want string) {
		t.Helper()
		list := c.mustList(scopeID)
		got := strings.Join(contradicted(list), ",")
		wantCount := 0
		if want != "" {
			wantCount = len(strings.Split(want, ","))
		}
		if got != want || list.Counts.Contradictions != wantCount {
			t.Fatalf("%s: contradictions = %q (count %d), want %q (count %d)",
				when, got, list.Counts.Contradictions, want, wantCount)
		}
	}

	assertFlags(a, scopeA, "A before overlay", "R-01")
	assertFlags(b, scopeB, "B before overlay", "R-01")

	edge := seedOverlayEdge(t, admin, tenantA, f.Req["R-02"], a1)
	// A redundant overlay edge on R-01, which the catalog already reaches,
	// must not double-count it.
	seedOverlayEdge(t, admin, tenantA, f.Req["R-01"], a1)

	assertFlags(a, scopeA, "A with overlay (AC1)", "R-01,R-02")
	assertFlags(b, scopeB, "B with A's overlay (AC2)", "R-01")

	resp, payload := doJSON(t, http.MethodPost, tsA.URL+"/v1/mapping-overlays/"+edge.String()+"/retire",
		adminA, `{"reason":"auditor withdrew the finding"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retire overlay edge: status %d; body %s", resp.StatusCode, payload)
	}

	assertFlags(a, scopeA, "A after retire (AC3)", "R-01")
	assertFlags(b, scopeB, "B after retire", "R-01")
}
